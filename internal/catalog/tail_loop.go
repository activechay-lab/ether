package catalog

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/schedule"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// The tail index's life on one node. Every node that serves search runs
// RunTail (not only the cluster leader): each builds its own file from the
// same public export, in its own data directory, so no node depends on
// another's cache.
//
//   - Start: remove temporary files, map the newest file that verifies
//     (deleting any that do not), delete older versions. Search works
//     before this finishes: the library index and TMDB's live search answer.
//   - Every hour: when the export has a newer day than the index, build a
//     new version from it, write it under a new name, map it and make it
//     current. The previous version is unmapped and deleted when its last
//     reader is done; a deletion that fails (a virus scanner holding the
//     file on Windows) is retried later and never fails the swap.
//   - Monthly: the names walk, then a rebuild.
//
// A failed build (no export, disk full, mapping refused) keeps the current
// version, records LastError and retries next hour.

// WithTailIndex keeps the long-tail search index in dir (the data
// directory's tmdb-exports). free reports the free bytes of a path; nil
// skips the check.
func WithTailIndex(dir string, free func(string) (int64, error)) Option {
	return func(c *Catalog) {
		c.tail.dir = dir
		c.tail.free = free
	}
}

// RunTail keeps this node's tail index current until ctx ends. Run it on
// every node that serves search; nodes that never search (source roles)
// do not, and report the index as disabled.
func (c *Catalog) RunTail(ctx context.Context) {
	ctx = tmdb.Background(ctx) // the names walk and lean fetches yield to viewers
	if c.tail.dir == "" || c.exports == nil {
		return
	}
	c.tail.enabled.Store(true)
	if err := os.MkdirAll(c.tail.dir, 0o755); err != nil {
		c.setTailError(err)
		return
	}
	c.loadNewestTail()
	for ctx.Err() == nil {
		if c.tmdb() != nil {
			if k, ok := c.tmdb().(interface{ HasKey() bool }); !ok || k.HasKey() {
				if err := c.refreshTail(ctx); err != nil && ctx.Err() == nil {
					c.log.Warn("catalog: long-tail index", "err", err)
				}
			}
		}
		c.reapTailFiles()
		if !schedule.Wait(ctx, c.now, time.Hour) {
			return
		}
	}
}

func (c *Catalog) setTailError(err error) {
	c.tail.mu.Lock()
	if err == nil {
		c.tail.lastErr = ""
	} else {
		c.tail.lastErr = err.Error()
	}
	c.tail.mu.Unlock()
}

// loadNewestTail maps the newest valid index file.
func (c *Catalog) loadNewestTail() {
	dir := c.tail.dir
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if isTailTemp(e.Name()) || strings.HasPrefix(e.Name(), ".tail-names-") {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
	files := tailFiles(dir)
	for i, path := range files {
		t, unmap, err := openTailFile(path)
		if err != nil {
			c.log.Warn("catalog: long-tail index file rejected", "file", filepath.Base(path), "err", err)
			_ = os.Remove(path)
			continue
		}
		c.installTail(c.tailHandle(t, unmap, path))
		for _, older := range files[i+1:] {
			c.retireTailFile(older)
		}
		c.tail.mu.Lock()
		c.tail.path = path
		c.tail.mu.Unlock()
		return
	}
}

// tailHandle wraps a mapped version: closing it unmaps the file, then
// deletes it.
func (c *Catalog) tailHandle(t *tailIndex, unmap func() error, path string) *tailHandle {
	return &tailHandle{t: t, close: func() {
		if err := unmap(); err != nil {
			c.log.Warn("catalog: unmap long-tail index", "file", filepath.Base(path), "err", err)
		}
		c.retireTailFile(path)
	}}
}

// retireTailFile deletes an old version, or remembers it for a later try.
func (c *Catalog) retireTailFile(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.tail.mu.Lock()
		c.tail.stale = append(c.tail.stale, path)
		c.tail.mu.Unlock()
	}
}

// reapTailFiles retries deleting retired versions.
func (c *Catalog) reapTailFiles() {
	c.tail.mu.Lock()
	stale := c.tail.stale
	c.tail.stale = nil
	c.tail.mu.Unlock()
	for _, p := range stale {
		c.retireTailFile(p)
	}
}

// refreshTail builds a new version when the export (or the names file)
// is newer than the current index, and runs the monthly names walk.
func (c *Catalog) refreshTail(ctx context.Context) error {
	day, err := c.tailExportDay(ctx)
	if err != nil {
		c.setTailError(err)
		return err
	}
	cur, release := c.acquireTail()
	stale := cur == nil || cur.day.Before(day)
	release()
	if stale {
		if err := c.buildTail(ctx); err != nil {
			c.setTailError(err)
			return err
		}
	}
	walked, _ := time.Parse(time.RFC3339, c.getMeta(ctx, "tail_names_walked"))
	if c.now().Sub(walked) >= tailNamesEvery {
		if err := c.namesWalk(ctx, c.tail.dir); err != nil {
			return fmt.Errorf("names walk: %w", err)
		}
		return c.buildTail(ctx)
	}
	return nil
}

// tailExportDay is the day of the newest movie export available.
func (c *Catalog) tailExportDay(ctx context.Context) (time.Time, error) {
	info, err := c.exports.Each(ctx, tmdb.ExportMovies, func(tmdb.ExportRow) error { return errStopExport })
	if err != nil && !errors.Is(err, errStopExport) {
		return time.Time{}, err
	}
	return info.Day, nil
}

var errStopExport = errors.New("stop")

// tailBuildMargin is the free space a build needs beyond two versions.
const tailBuildMargin = 64 << 20

// buildTail builds, writes, maps and installs a new index version.
func (c *Catalog) buildTail(ctx context.Context) error {
	if !c.tail.building.CompareAndSwap(false, true) {
		return nil
	}
	defer c.tail.building.Store(false)
	dir := c.tail.dir
	if c.tail.free != nil {
		need := int64(tailBuildMargin) + 160<<20 // a full-export version
		if cur, release := c.acquireTail(); cur != nil {
			need = tailBuildMargin + cur.bytes()
			release()
		}
		if free, err := c.tail.free(dir); err == nil && free < need {
			return fmt.Errorf("not enough disk space for the long-tail index: %d MB free, %d MB needed", free>>20, need>>20)
		}
	}
	started := time.Now()
	names, err := loadTailNames(filepath.Join(dir, tailNamesFile))
	if err != nil {
		c.log.Warn("catalog: tail names unreadable", "err", err)
		names = nil
	}
	cfg := c.config()
	b := &tailBuilder{}
	var missing []core.RatingKey
	for _, tv := range []bool{false, true} {
		for _, kind := range exportKinds(tv, cfg) {
			info, err := c.exports.EachNamed(ctx, kind, func(row tmdb.ExportRow, name string) error {
				b.add(tv, row, name)
				return nil
			})
			if err != nil {
				if kind == tmdb.ExportAdultMovies || kind == tmdb.ExportAdultTV {
					continue
				}
				return err
			}
			if kind == tmdb.ExportMovies {
				b.day = info.Day
			}
		}
	}
	if names.len() > 0 {
		for _, r := range b.rows {
			if _, ok := names.lookup(core.RatingKey(r.key)); !ok {
				missing = append(missing, core.RatingKey(r.key))
			}
		}
		if names, err = c.fillNewNames(ctx, dir, names, missing); err != nil {
			c.log.Warn("catalog: tail names", "err", err)
		}
	}
	day := b.day
	seq := 1
	for _, p := range tailFiles(dir) {
		if strings.HasPrefix(filepath.Base(p), "tail-"+day.UTC().Format("2006-01-02")+"-") {
			var n int
			fmt.Sscanf(filepath.Base(p)[len("tail-2006-01-02-"):], "%03d", &n)
			seq = max(seq, n+1)
		}
	}
	w, err := newTailFileWriter(dir)
	if err != nil {
		return fmt.Errorf("write long-tail index: %w", err)
	}
	n, err := b.write(names, w)
	b = nil
	if err != nil {
		w.abort()
		return fmt.Errorf("write long-tail index: %w", err)
	}
	path, err := w.finish(day, n, seq)
	if err != nil {
		return fmt.Errorf("write long-tail index: %w", err)
	}
	runtime.GC() // the build's heap goes before the mapped version arrives
	mapped, unmap, err := openTailFile(path)
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("map long-tail index: %w", err)
	}
	c.installTail(c.tailHandle(mapped, unmap, path))
	c.tail.mu.Lock()
	c.tail.builtAt, c.tail.lastErr, c.tail.path = c.now(), "", path
	c.tail.mu.Unlock()
	c.log.Info("catalog: long-tail index built", "day", mapped.day.Format("2006-01-02"), "titles", mapped.len(),
		"bytes", mapped.bytes(), "names", names.len(), "took", time.Since(started).Round(time.Millisecond))
	return nil
}
