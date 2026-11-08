package tmdb

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/egress"
	"github.com/braedonsaunders/ether/internal/httpclient"
)

// TMDB's daily ID exports list every valid movie and TV series ID with its
// adult flag, video flag and popularity. They are the catalogue's list of
// what exists: no API key, no request quota, no 10,000-results-per-query
// cap and no blind spot for undated titles.
//
// Format and terms, per https://developer.themoviedb.org/docs/daily-id-exports
// (checked 2026-11-08):
//   - files are https://files.tmdb.org/p/exports/<kind>_MM_DD_YYYY.json.gz;
//   - "The export job runs every day starting at around 7:00 AM UTC, and all
//     files are available by 8:00 AM UTC." Until then the day's file answers
//     403 AccessDenied (S3), not 404, so both mean "not published";
//   - gzip, one JSON object per line ("These files themselves are not a
//     valid JSON object. Instead, each line is.");
//   - adult titles are in separate adult_* files (since 2023-07-05);
//   - files are deleted after 3 months; they are not full data exports and
//     carry no authentication, which TMDB says may change.
//
// Measured 11_07_2026: movie_ids 1,271,894 lines (28 MB), tv_series_ids
// 236,129 (5 MB), adult_movie_ids 3 MB.

// ExportKind names one daily export file.
type ExportKind string

const (
	ExportMovies      ExportKind = "movie_ids"
	ExportTV          ExportKind = "tv_series_ids"
	ExportAdultMovies ExportKind = "adult_movie_ids"
	ExportAdultTV     ExportKind = "adult_tv_series_ids"
)

// DefaultExportBaseURL is where TMDB publishes the daily exports.
const DefaultExportBaseURL = "https://files.tmdb.org/p/exports"

// ExportRow is one line of an export. Titles and names are not decoded.
type ExportRow struct {
	ID         int64   `json:"id"`
	Popularity float64 `json:"popularity"`
	Adult      bool    `json:"adult"`
	Video      bool    `json:"video"`
}

// ExportInfo describes the file one Each call read.
type ExportInfo struct {
	Kind ExportKind
	// Day is the export's publication day (UTC midnight).
	Day       time.Time
	Rows      int
	Malformed int
	// Cached is true when this call read the copy kept from an earlier
	// download: it is today's, or no newer day could be downloaded.
	Cached bool
}

// ErrExportUnavailable means no export of the kind could be downloaded and
// no recent enough copy is kept. Callers fall back to /discover.
var ErrExportUnavailable = errors.New("tmdb: daily export unavailable")

// Bounds. The movie export is 28 MB compressed and about 120 bytes a line.
const (
	exportMaxBytes    = 512 << 20
	exportMaxLine     = 64 << 10
	exportLookback    = 3                   // today and the 3 days before
	exportMaxAge      = 14 * 24 * time.Hour // older copies are not used
	exportMaxBadShare = 0.01                // more malformed lines rejects the file
	exportAttempts    = 3
)

// Exports downloads, verifies and keeps the latest good copy of each daily
// export in dir, and streams it. Safe for concurrent use.
type Exports struct {
	dir     string
	base    string
	client  *http.Client
	log     *slog.Logger
	now     func() time.Time
	backoff time.Duration

	mu    sync.Mutex
	kinds map[ExportKind]*sync.Mutex
}

// ExportOption customises Exports.
type ExportOption func(*Exports)

// WithExportBaseURL points Exports at another host (tests, mirrors).
func WithExportBaseURL(u string) ExportOption {
	return func(e *Exports) { e.base = strings.TrimRight(u, "/") }
}

// WithExportHTTPClient replaces the download client.
func WithExportHTTPClient(c *http.Client) ExportOption { return func(e *Exports) { e.client = c } }

// WithExportClock sets the clock that picks the export day.
func WithExportClock(now func() time.Time) ExportOption { return func(e *Exports) { e.now = now } }

// WithExportBackoff sets the first retry delay of a failed download.
func WithExportBackoff(d time.Duration) ExportOption { return func(e *Exports) { e.backoff = d } }

// NewExports keeps downloaded exports in dir (created on first use).
func NewExports(dir string, log *slog.Logger, opts ...ExportOption) *Exports {
	if log == nil {
		log = slog.Default()
	}
	e := &Exports{dir: dir, base: DefaultExportBaseURL, log: log, now: time.Now, backoff: 5 * time.Second,
		client: egress.Client(egress.CapMetadata, 10*time.Minute), kinds: map[ExportKind]*sync.Mutex{}}
	for _, fn := range opts {
		fn(e)
	}
	return e
}

func (e *Exports) kindLock(kind ExportKind) *sync.Mutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	m := e.kinds[kind]
	if m == nil {
		m = &sync.Mutex{}
		e.kinds[kind] = m
	}
	return m
}

// Each calls fn for every row of the newest available export of kind: the
// newest published day it can download, else the kept copy if it is at most
// two weeks old. The file is verified (complete gzip, at most 1% malformed
// lines) before any row is passed on. fn's error stops the stream.
func (e *Exports) Each(ctx context.Context, kind ExportKind, fn func(ExportRow) error) (ExportInfo, error) {
	return e.each(ctx, kind, func(r io.Reader) (int, int, error) { return ParseExport(r, fn) })
}

// EachNamed is Each with each row's original title or name.
func (e *Exports) EachNamed(ctx context.Context, kind ExportKind, fn func(ExportRow, string) error) (ExportInfo, error) {
	return e.each(ctx, kind, func(r io.Reader) (int, int, error) { return ParseExportNamed(r, fn) })
}

func (e *Exports) each(ctx context.Context, kind ExportKind, parse func(io.Reader) (int, int, error)) (ExportInfo, error) {
	path, info, err := e.latest(ctx, kind)
	if err != nil {
		return info, err
	}
	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer f.Close()
	rows, bad, err := parse(f)
	info.Rows, info.Malformed = rows, bad
	return info, err
}

func (e *Exports) latest(ctx context.Context, kind ExportKind) (string, ExportInfo, error) {
	lock := e.kindLock(kind)
	lock.Lock()
	defer lock.Unlock()
	info := ExportInfo{Kind: kind}
	if err := os.MkdirAll(e.dir, 0o755); err != nil {
		return "", info, err
	}
	keptPath, keptDay := e.kept(kind)
	today := e.now().UTC().Truncate(24 * time.Hour)
	var lastErr error
	for back := 0; back <= exportLookback; back++ {
		day := today.AddDate(0, 0, -back)
		if !keptDay.IsZero() && !day.After(keptDay) {
			break // the kept copy is this day or newer
		}
		path, err := e.download(ctx, kind, day)
		if err == nil {
			if keptPath != "" && keptPath != path {
				_ = os.Remove(keptPath)
			}
			info.Day = day
			return path, info, nil
		}
		if ctx.Err() != nil {
			return "", info, ctx.Err()
		}
		var nf *exportNotPublished
		if !errors.As(err, &nf) {
			lastErr = err
			e.log.Warn("tmdb: daily export download", "kind", kind, "day", day.Format("2006-01-02"), "err", err)
		}
	}
	if keptPath != "" && today.Sub(keptDay) <= exportMaxAge {
		info.Day, info.Cached = keptDay, true
		return keptPath, info, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no file published in the last %d days", exportLookback+1)
	}
	return "", info, fmt.Errorf("%w: %s: %v", ErrExportUnavailable, kind, lastErr)
}

type exportNotPublished struct{ status int }

func (e *exportNotPublished) Error() string { return fmt.Sprintf("not published (HTTP %d)", e.status) }

func exportFileName(kind ExportKind, day time.Time) string {
	return fmt.Sprintf("%s_%s.json.gz", kind, day.Format("01_02_2006"))
}

// kept finds the newest verified copy of kind in dir.
func (e *Exports) kept(kind ExportKind) (string, time.Time) {
	matches, _ := filepath.Glob(filepath.Join(e.dir, string(kind)+"_??_??_????.json.gz"))
	var best string
	var bestDay time.Time
	for _, m := range matches {
		name := strings.TrimSuffix(filepath.Base(m), ".json.gz")
		day, err := time.Parse("01_02_2006", strings.TrimPrefix(name, string(kind)+"_"))
		if err != nil {
			continue
		}
		if day.After(bestDay) {
			best, bestDay = m, day
		}
	}
	return best, bestDay
}

// download fetches one day's file to a temporary file, verifies it and
// renames it into place. Transient failures are retried with backoff.
func (e *Exports) download(ctx context.Context, kind ExportKind, day time.Time) (string, error) {
	name := exportFileName(kind, day)
	final := filepath.Join(e.dir, name)
	delay := e.backoff
	var err error
	for attempt := 1; attempt <= exportAttempts; attempt++ {
		err = e.fetchOnce(ctx, e.base+"/"+name, final)
		var nf *exportNotPublished
		if err == nil || errors.As(err, &nf) || ctx.Err() != nil {
			return final, err
		}
		if attempt < exportAttempts {
			t := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				t.Stop()
				return "", ctx.Err()
			case <-t.C:
			}
			delay *= 4
		}
	}
	return "", err
}

func (e *Exports) fetchOnce(ctx context.Context, url, final string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", httpclient.UserAgent)
	resp, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return &exportNotPublished{status: resp.StatusCode}
	case resp.StatusCode != http.StatusOK:
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > exportMaxBytes {
		return fmt.Errorf("export is %d bytes, over the %d limit", resp.ContentLength, exportMaxBytes)
	}
	tmp, err := os.CreateTemp(filepath.Dir(final), ".download-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	keep := false
	defer func() {
		if !keep {
			tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	n, err := io.Copy(tmp, io.LimitReader(resp.Body, exportMaxBytes+1))
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if n > exportMaxBytes {
		return fmt.Errorf("export over the %d byte limit", exportMaxBytes)
	}
	if resp.ContentLength >= 0 && n != resp.ContentLength {
		return fmt.Errorf("download truncated: %d of %d bytes", n, resp.ContentLength)
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	rows, bad, err := ParseExport(tmp, nil)
	if err != nil {
		return fmt.Errorf("verify: %w", err)
	}
	if rows == 0 || float64(bad) > exportMaxBadShare*float64(rows+bad) {
		return fmt.Errorf("verify: %d rows, %d malformed lines", rows, bad)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		return err
	}
	keep = true
	return nil
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ParseExport streams a gzip JSON Lines export, calling fn (if not nil) for
// each well-formed row with a positive id. Blank lines are skipped; lines
// that do not decode, or are longer than 64 KiB, are counted as malformed
// and skipped. A UTF-8 BOM and CRLF line ends are accepted. A truncated or
// corrupt gzip stream is an error (the gzip trailer's CRC and length are
// checked at the end). Memory is bounded by one line.
func ParseExport(r io.Reader, fn func(ExportRow) error) (rows, malformed int, err error) {
	var named func(ExportRow, string) error
	if fn != nil {
		named = func(row ExportRow, _ string) error { return fn(row) }
	}
	return parseExport(r, named, false)
}

// ParseExportNamed is ParseExport that also passes each row's original
// title (movies) or original name (series).
func ParseExportNamed(r io.Reader, fn func(ExportRow, string) error) (rows, malformed int, err error) {
	return parseExport(r, fn, true)
}

type exportLine struct {
	ExportRow
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
}

func parseExport(r io.Reader, fn func(ExportRow, string) error, names bool) (rows, malformed int, err error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return 0, 0, fmt.Errorf("export: %w", err)
	}
	defer zr.Close()
	br := bufio.NewReaderSize(zr, exportMaxLine)
	for {
		line, rerr := br.ReadSlice('\n')
		if errors.Is(rerr, bufio.ErrBufferFull) {
			// Over-long line: skip to its end.
			malformed++
			for errors.Is(rerr, bufio.ErrBufferFull) {
				_, rerr = br.ReadSlice('\n')
			}
			if rerr != nil && !errors.Is(rerr, io.EOF) {
				return rows, malformed, fmt.Errorf("export: %w", rerr)
			}
			if rerr != nil {
				return rows, malformed, nil
			}
			continue
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return rows, malformed, fmt.Errorf("export: %w", rerr)
		}
		line = bytes.TrimPrefix(bytes.TrimSpace(line), utf8BOM)
		if len(line) > 0 {
			var row exportLine
			var derr error
			if names {
				derr = json.Unmarshal(line, &row)
			} else {
				derr = json.Unmarshal(line, &row.ExportRow)
			}
			if derr != nil || row.ID <= 0 {
				malformed++
			} else {
				rows++
				if fn != nil {
					name := row.OriginalTitle
					if name == "" {
						name = row.OriginalName
					}
					if err := fn(row.ExportRow, name); err != nil {
						return rows, malformed, err
					}
				}
			}
		}
		if rerr != nil {
			return rows, malformed, nil
		}
	}
}
