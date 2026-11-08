package catalog

import (
	"bufio"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// The export carries only a title's original name. The names file adds,
// for the titles Ether has read from TMDB, the display title, the release
// date and the poster path (or that TMDB has none). It is filled by the
// names walk, a background /discover walk over every dated title (about
// 64k requests, monthly), and by lean fetches of ids new since the walk.
// It is read only when the tail index is built.
//
// File: <dir>/tail-names.json.gz, gzip JSON lines sorted by key:
//
//	{"k":1000000000000078,"t":"Blade Runner","d":"1982-06-25","p":"/x.jpg"}
//	{"k":...,"n":1}   flags: 1 no poster, 2 no date

const tailNamesFile = "tail-names.json.gz"

// tailNames is the names file in memory, sorted by key.
type tailNames struct {
	keys       []int64
	titleAt    []uint32
	titleBlob  string
	posterAt   []uint32
	posterBlob string
	released   []int32 // dayNumber; 0 not known
	flags      []uint8
}

type nameRec struct {
	title, poster string
	released      int32
	year          int
	flags         uint8
}

func (n *tailNames) len() int {
	if n == nil {
		return 0
	}
	return len(n.keys)
}

func (n *tailNames) lookup(key core.RatingKey) (nameRec, bool) {
	if n == nil {
		return nameRec{}, false
	}
	i, ok := slices.BinarySearch(n.keys, int64(key))
	if !ok {
		return nameRec{}, false
	}
	r := nameRec{title: blobAt(n.titleBlob, n.titleAt, i), poster: blobAt(n.posterBlob, n.posterAt, i), released: n.released[i], flags: n.flags[i]}
	if r.released > 0 {
		r.year = dayTime(r.released).Year()
	}
	return r, true
}

// namesLine is one line of the names file.
type namesLine struct {
	Key    int64  `json:"k"`
	Title  string `json:"t,omitempty"`
	Date   string `json:"d,omitempty"`
	Poster string `json:"p,omitempty"`
	Flags  uint8  `json:"n,omitempty"`
}

// namesBuilder accumulates name records, newest wins per key.
type namesBuilder struct {
	keys     []int64
	titles   blobBuilder
	titleAt  []uint32
	posters  blobBuilder
	posterAt []uint32
	released []int32
	flags    []uint8
}

// tailEpoch is day 1 of the stored dates: before every dated title, so a
// stored day number is always positive and 0 means "no date".
var tailEpoch = time.Date(1800, 1, 1, 0, 0, 0, 0, time.UTC)

func dayNumber(t time.Time) int32 {
	if t.IsZero() || t.Before(tailEpoch) {
		return 0
	}
	return int32(t.Sub(tailEpoch)/(24*time.Hour)) + 1
}

func dayTime(d int32) time.Time {
	if d <= 0 {
		return time.Time{}
	}
	return tailEpoch.AddDate(0, 0, int(d-1))
}

func (b *namesBuilder) add(key core.RatingKey, title string, released time.Time, poster string, flags uint8) {
	b.keys = append(b.keys, int64(key))
	b.titleAt = append(b.titleAt, b.titles.add(title))
	b.posterAt = append(b.posterAt, b.posters.add(poster))
	b.released = append(b.released, dayNumber(released))
	b.flags = append(b.flags, flags)
}

// addTitle records what a summary says about a title.
func (b *namesBuilder) addTitle(t core.Title) {
	var flags uint8
	if t.PosterPath == "" {
		flags |= tailPosterNone
	}
	if t.Released.IsZero() {
		flags |= tailUndated
	}
	b.add(t.Key, t.Title, t.Released, t.PosterPath, flags)
}

// build sorts the records by key, the last added winning for a key.
func (b *namesBuilder) build() *tailNames {
	n := len(b.keys)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return b.keys[order[i]] < b.keys[order[j]] })
	titleBlob, posterBlob := b.titles.String(), b.posters.String()
	titleAt := append(b.titleAt, b.titles.end())
	posterAt := append(b.posterAt, b.posters.end())
	out := &tailNames{}
	var titles, posters blobBuilder
	for k, i := range order {
		if k+1 < n && b.keys[order[k+1]] == b.keys[i] {
			continue // a later record for this key follows
		}
		out.keys = append(out.keys, b.keys[i])
		out.titleAt = append(out.titleAt, titles.add(titleBlob[titleAt[i]:titleAt[i+1]]))
		out.posterAt = append(out.posterAt, posters.add(posterBlob[posterAt[i]:posterAt[i+1]]))
		out.released = append(out.released, b.released[i])
		out.flags = append(out.flags, b.flags[i])
	}
	out.titleAt = append(out.titleAt, titles.end())
	out.posterAt = append(out.posterAt, posters.end())
	out.titleBlob, out.posterBlob = titles.String(), posters.String()
	return out
}

// addAll copies every record of n into b (before newer records).
func (b *namesBuilder) addAll(n *tailNames) {
	for i := 0; i < n.len(); i++ {
		b.keys = append(b.keys, n.keys[i])
		b.titleAt = append(b.titleAt, b.titles.add(blobAt(n.titleBlob, n.titleAt, i)))
		b.posterAt = append(b.posterAt, b.posters.add(blobAt(n.posterBlob, n.posterAt, i)))
		b.released = append(b.released, n.released[i])
		b.flags = append(b.flags, n.flags[i])
	}
}

// loadTailNames reads the names file; a missing file is no names.
func loadTailNames(path string) (*tailNames, error) {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(bufio.NewReaderSize(f, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("catalog: tail names: %w", err)
	}
	b := &namesBuilder{}
	dec := json.NewDecoder(zr)
	for {
		var l namesLine
		if err := dec.Decode(&l); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("catalog: tail names: %w", err)
		}
		b.add(core.RatingKey(l.Key), l.Title, tmdb.ParseDate(l.Date), l.Poster, l.Flags)
	}
	return b.build(), nil
}

// writeTailNames replaces the names file, through a temporary file.
func writeTailNames(path string, n *tailNames) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tail-names-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	bw := bufio.NewWriterSize(f, 1<<20)
	zw := gzip.NewWriter(bw)
	enc := json.NewEncoder(zw)
	for i := 0; i < n.len(); i++ {
		l := namesLine{Key: n.keys[i], Title: blobAt(n.titleBlob, n.titleAt, i), Poster: blobAt(n.posterBlob, n.posterAt, i), Flags: n.flags[i]}
		if d := n.released[i]; d > 0 {
			l.Date = dayTime(d).Format("2006-01-02")
		}
		if err := enc.Encode(l); err != nil {
			f.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// namesSink collects a /discover walk's titles into a names builder.
type namesSink struct {
	b    *namesBuilder
	hook func(context.Context) error
}

func (s *namesSink) add(_ context.Context, t core.Title, _ bool) error {
	s.b.addTitle(t)
	return nil
}
func (s *namesSink) pending() int { return 0 }
func (s *namesSink) flush(ctx context.Context) error {
	if s.hook != nil {
		return s.hook(ctx)
	}
	return nil
}
func (s *namesSink) setFlushHook(fn func(context.Context) error) { s.hook = fn }

// tailNamesEvery is how often the names walk re-reads TMDB, for titles
// renamed, redated or given a poster since.
const tailNamesEvery = 30 * 24 * time.Hour

// namesWalk reads the display title, date and poster of every dated title
// from /discover, sorted by original title (a popularity order shifts
// while a 30-minute walk pages through it), and rewrites the names file.
// Records the walk did not see (undated titles read by a lean fetch) are
// kept.
func (c *Catalog) namesWalk(ctx context.Context, dir string) error {
	path := filepath.Join(dir, tailNamesFile)
	old, err := loadTailNames(path)
	if err != nil {
		c.log.Warn("catalog: tail names unreadable; rebuilding", "err", err)
		old = nil
	}
	b := &namesBuilder{}
	b.addAll(old)
	sink := &namesSink{b: b}
	cfg := c.config()
	for _, tv := range []bool{false, true} {
		spec := walkSpec{tv: tv, pass: "names", video: true, keep: func(t core.Title) bool { return !t.Adult || cfg.IncludeAdult },
			params: tmdb.DiscoverParams{SortBy: "original_title.asc", IncludeAdult: cfg.IncludeAdult, Extra: url.Values{"include_video": {"true"}}}}
		if err := c.walkDiscover(ctx, spec, sink); err != nil {
			return err
		}
	}
	names := b.build()
	if err := writeTailNames(path, names); err != nil {
		return err
	}
	c.log.Info("catalog: tail names walk", "titles", names.len())
	return c.setMeta(ctx, "tail_names_walked", c.now().UTC().Format(time.RFC3339))
}

// tailNamesBudget bounds the lean fetches a build makes for ids the names
// file does not know: the ids new since the walk (about 670 a day) and the
// undated ones no /discover query returns (about 230k, read over two
// weeks). 20,000 is about 8 minutes of TMDB at 40 requests a second.
const tailNamesBudget = 20000

// fillNewNames lean-fetches export ids the names file lacks, newest ids
// first, and adds them to the names file. Before the first names walk it
// does nothing: that walk reads them 20 a request.
func (c *Catalog) fillNewNames(ctx context.Context, dir string, names *tailNames, missing []core.RatingKey) (*tailNames, error) {
	if names.len() == 0 || len(missing) == 0 || !c.canEnrichTail() {
		return names, nil
	}
	slices.SortFunc(missing, func(a, b core.RatingKey) int { return int(b.TMDB() - a.TMDB()) })
	missing = missing[:min(len(missing), tailNamesBudget)]
	b := &namesBuilder{}
	b.addAll(names)
	added := 0
	for _, k := range missing {
		if ctx.Err() != nil {
			break
		}
		t, err := c.fetchSummary(ctx, k.Kind() == core.KindShow, k.TMDB())
		if errors.Is(err, core.ErrNotFound) {
			b.add(k, "", time.Time{}, "", tailPosterNone|tailUndated)
			continue
		}
		if err != nil {
			break // TMDB unavailable: the next build carries on
		}
		b.addTitle(t)
		added++
	}
	if added == 0 {
		return names, nil
	}
	out := b.build()
	if err := writeTailNames(filepath.Join(dir, tailNamesFile), out); err != nil {
		return names, err
	}
	return out, nil
}
