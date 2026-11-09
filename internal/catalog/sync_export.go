package catalog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// The catalogue's list of titles comes from TMDB's daily ID exports
// (tmdb/exports.go): every movie and series id with its popularity, at no
// API cost and with no per-query cap. One pass per type:
//
//  1. load the stored ids and popularity of that type into a sorted array
//     (key-range reads, about 16 bytes a title);
//  2. stream the export. A stored title gets its popularity written only
//     when rating_drift.go says it matters; one that now meets the browse
//     bar is promoted. A title not stored is a candidate when the
//     "Hide obscure titles" preset's popularity clause admits it (every
//     title under Off);
//  3. the candidates, most popular first, are the fill backlog
//     (sync_fill.go). Titles the preset admits by votes alone come from
//     the /discover vote walk, since the export has no vote counts.
//
// A stored title missing from today's export (deleted or merged on TMDB)
// is never removed or demoted here; the pass only counts and logs them.

// ExportSource streams TMDB's daily ID exports; *tmdb.Exports is one.
type ExportSource interface {
	Each(ctx context.Context, kind tmdb.ExportKind, fn func(tmdb.ExportRow) error) (tmdb.ExportInfo, error)
	// EachNamed is Each with each row's original title or name.
	EachNamed(ctx context.Context, kind tmdb.ExportKind, fn func(tmdb.ExportRow, string) error) (tmdb.ExportInfo, error)
}

// WithExports syncs the title list from TMDB's daily ID exports. Without
// it, or when no export is available, the sync walks /discover.
func WithExports(src ExportSource) Option { return func(c *Catalog) { c.exports = src } }

// includeVideoReleases admits titles TMDB flags video=true (recorded
// concerts, compilations; 65,642 of 1,271,894 movies on 2026-11-07).
// /discover leaves them out by default, and so does the export pass until
// the owner decides otherwise.
const includeVideoReleases = false

// storedSet is one type's stored titles, sorted by TMDB id.
type storedSet struct {
	ids       []uint32
	pop       []float64
	browsable []bool
	seen      []bool
}

func (s *storedSet) find(id int64) int {
	if id <= 0 || id > math.MaxUint32 {
		return -1
	}
	i, ok := slices.BinarySearch(s.ids, uint32(id))
	if !ok {
		return -1
	}
	return i
}

func kindRange(tv bool) (lo, hi core.RatingKey) {
	if tv {
		return core.ShowKey(0), core.ShowKey(0) + (core.ShowKey(0) - core.MovieKey(0))
	}
	return core.MovieKey(0), core.ShowKey(0)
}

func titleKey(tv bool, id int64) core.RatingKey {
	if tv {
		return core.ShowKey(id)
	}
	return core.MovieKey(id)
}

// loadStoredSet reads one type's keys, popularity and browsable flag in
// primary-key ranges of storedSetChunk rows: each statement is an index
// range read, and no single read holds a snapshot for the whole table.
func (c *Catalog) loadStoredSet(ctx context.Context, tv bool) (*storedSet, error) {
	lo, hi := kindRange(tv)
	s := &storedSet{}
	last := int64(lo)
	for {
		rows, err := c.db.QueryContext(ctx, `SELECT key, popularity, browsable FROM catalog_titles WHERE key>? AND key<? ORDER BY key LIMIT ?`, last, int64(hi), storedSetChunk)
		if err != nil {
			return nil, err
		}
		n := 0
		for rows.Next() {
			var key int64
			var pop float64
			var browsable int
			if err := rows.Scan(&key, &pop, &browsable); err != nil {
				rows.Close()
				return nil, err
			}
			n++
			last = key
			id := core.RatingKey(key).TMDB()
			if id <= 0 || id > math.MaxUint32 {
				continue
			}
			s.ids = append(s.ids, uint32(id))
			s.pop = append(s.pop, pop)
			s.browsable = append(s.browsable, browsable != 0)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if n < storedSetChunk {
			break
		}
	}
	s.seen = make([]bool, len(s.ids))
	return s, nil
}

const storedSetChunk = 20000

// exportPass is one type's export result.
type exportPass struct {
	tv      bool
	day     time.Time
	cached  bool
	stored  *storedSet
	backlog []uint64 // candidates not stored: packFill(pop, id), most popular first
	rows    int
	video   int
	absent  int
	popRows int // popularity writes
	moved   int // promoted to browsable
}

func exportKinds(tv bool, cfg config.Catalog) []tmdb.ExportKind {
	kinds := []tmdb.ExportKind{tmdb.ExportMovies}
	if tv {
		kinds = []tmdb.ExportKind{tmdb.ExportTV}
	}
	if cfg.IncludeAdult {
		if tv {
			kinds = append(kinds, tmdb.ExportAdultTV)
		} else {
			kinds = append(kinds, tmdb.ExportAdultMovies)
		}
	}
	return kinds
}

// exportCandidate reports whether a title the library does not hold is
// worth fetching on its export row alone: at or above the popularity bar.
// Titles admitted only by votes come from the /discover vote walk; the
// rest of the export is the long tail, found by search, never stored.
func exportCandidate(cfg config.Catalog, row tmdb.ExportRow) bool {
	minPop, _ := cfg.Bars()
	return minPop > 0 && row.Popularity >= minPop
}

// errNoExport means the export pass could not read today's list; the sync
// walks /discover instead.
var errNoExport = errors.New("catalog: no TMDB export")

// runExportPass streams one type's exports. It returns errNoExport when
// there is no export source or no export of the primary kind.
func (c *Catalog) runExportPass(ctx context.Context, tv bool) (*exportPass, error) {
	if c.exports == nil {
		return nil, errNoExport
	}
	cfg := c.config()
	stored, err := c.loadStoredSet(ctx, tv)
	if err != nil {
		return nil, err
	}
	res := &exportPass{tv: tv, stored: stored}
	now := c.now()
	cuts := c.ratingCuts()
	pw := &popWriter{c: c}
	var crossed []core.RatingKey
	for i, kind := range exportKinds(tv, cfg) {
		info, err := c.exports.Each(ctx, kind, func(row tmdb.ExportRow) error {
			res.rows++
			if row.Video && !includeVideoReleases {
				res.video++
				return nil
			}
			if (row.Adult || kind == tmdb.ExportAdultMovies || kind == tmdb.ExportAdultTV) && !cfg.IncludeAdult {
				return nil
			}
			if j := stored.find(row.ID); j >= 0 {
				stored.seen[j] = true
				key := titleKey(tv, row.ID)
				old := stored.pop[j]
				if !popularityMatters(cuts, old, row.Popularity, ratingExact(key, now)) {
					return nil
				}
				if !stored.browsable[j] && cfg.MinPopularity > 0 && old < cfg.MinPopularity && row.Popularity >= cfg.MinPopularity {
					crossed = append(crossed, key)
				}
				stored.pop[j] = row.Popularity
				return pw.add(ctx, key, row.Popularity)
			}
			if exportCandidate(cfg, row) {
				res.backlog = append(res.backlog, packFill(row.Popularity, row.ID))
			}
			return nil
		})
		if err != nil {
			if i == 0 && errors.Is(err, tmdb.ErrExportUnavailable) {
				c.log.Warn("catalog: TMDB daily export unavailable; walking /discover", "kind", kind, "err", err)
				return nil, errNoExport
			}
			if errors.Is(err, tmdb.ErrExportUnavailable) {
				// The adult list is optional for a day: its titles wait.
				c.log.Warn("catalog: TMDB daily export unavailable", "kind", kind, "err", err)
				continue
			}
			return nil, fmt.Errorf("catalog: read %s export: %w", kind, err)
		}
		if i == 0 {
			res.day, res.cached = info.Day, info.Cached
		}
		if info.Malformed > 0 {
			c.log.Info("catalog: TMDB export lines skipped", "kind", kind, "malformed", info.Malformed, "rows", info.Rows)
		}
	}
	if err := pw.flush(ctx); err != nil {
		return nil, err
	}
	res.popRows = pw.written
	if len(crossed) > 0 {
		n, err := c.promoteKeys(ctx, crossed)
		if err != nil {
			return nil, err
		}
		res.moved = n
	}
	for _, s := range stored.seen {
		if !s {
			res.absent++
		}
	}
	sortFill(res.backlog)
	c.log.Info("catalog: TMDB export pass", "tv", tv, "day", res.day.Format("2006-01-02"), "cached", res.cached,
		"rows", res.rows, "stored", len(stored.ids), "candidates", len(res.backlog), "popularity_writes", res.popRows,
		"promoted", res.moved, "video_skipped", res.video, "stored_not_in_export", res.absent)
	return res, nil
}

// packFill orders fill candidates by popularity in one word: a
// non-negative float32's bits sort like the number.
func packFill(pop float64, id int64) uint64 {
	if pop < 0 || math.IsNaN(pop) {
		pop = 0
	}
	return uint64(math.Float32bits(float32(pop)))<<32 | uint64(uint32(id))
}

func fillID(v uint64) int64 { return int64(uint32(v)) }

// sortFill puts the most popular first, then the lower id.
func sortFill(v []uint64) {
	sort.Slice(v, func(i, j int) bool {
		pi, pj := v[i]>>32, v[j]>>32
		if pi != pj {
			return pi > pj
		}
		return uint32(v[i]) < uint32(v[j])
	})
}

// popWriter writes export popularity in key order, a batch a transaction,
// behind the same WAL back-pressure as every sync write.
type popWriter struct {
	c       *Catalog
	pending []popUpdate
	limit   int
	written int
}

type popUpdate struct {
	key core.RatingKey
	pop float64
}

func (w *popWriter) add(ctx context.Context, key core.RatingKey, pop float64) error {
	w.pending = append(w.pending, popUpdate{key, pop})
	if w.limit <= 0 {
		w.limit = popWriteBatch
	}
	if len(w.pending) >= w.limit {
		return w.flush(ctx)
	}
	return nil
}

// popWriteBatch: a popularity update rewrites one row and the entries of
// the indexes holding popularity, so batches are larger than a full
// upsert's; the hold-time rule below still shrinks them on a slow disk.
const popWriteBatch = 1000

func (w *popWriter) flush(ctx context.Context) error {
	if len(w.pending) == 0 {
		return nil
	}
	if err := store.WaitWALBudget(ctx, w.c.db); err != nil {
		return err
	}
	slices.SortFunc(w.pending, func(a, b popUpdate) int {
		switch {
		case a.key < b.key:
			return -1
		case a.key > b.key:
			return 1
		}
		return 0
	})
	started := time.Now()
	tx, err := w.c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `UPDATE catalog_titles SET popularity=? WHERE key=? AND popularity<>?`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, u := range w.pending {
		if _, err := stmt.ExecContext(ctx, u.pop, int64(u.key), u.pop); err != nil {
			return fmt.Errorf("catalog: export popularity %d: %w", u.key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	w.c.writeGen.Add(1)
	w.written += len(w.pending)
	w.limit = nextBatch(w.limit, len(w.pending), time.Since(started), popWriteBatch)
	w.pending = w.pending[:0]
	return nil
}

// promoteKeys makes stored titles browsable that now meet the browse bar
// on their stored values, as a sync upsert would. Browsable is never
// removed by the sync.
func (c *Catalog) promoteKeys(ctx context.Context, keys []core.RatingKey) (int, error) {
	promoted := 0
	now := c.now()
	for start := 0; start < len(keys); start += 200 {
		chunk := keys[start:min(start+200, len(keys))]
		args := make([]any, len(chunk))
		for i, k := range chunk {
			args[i] = int64(k)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT key, type, popularity, vote_count, released_ts, adult FROM catalog_titles WHERE browsable=0 AND key IN (`+placeholders(len(chunk))+`)`, args...)
		if err != nil {
			return promoted, err
		}
		var promote []any
		for rows.Next() {
			var key, releasedTS int64
			var typ string
			var t core.Title
			if err := rows.Scan(&key, &typ, &t.Popularity, &t.VoteCount, &releasedTS, &t.Adult); err != nil {
				rows.Close()
				return promoted, err
			}
			t.Key, t.Type = core.RatingKey(key), core.MediaType(typ)
			if releasedTS != 0 {
				t.Released = time.Unix(releasedTS, 0).UTC()
			}
			if MeetsThreshold(c.config(), t, now) {
				promote = append(promote, key)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return promoted, err
		}
		if len(promote) == 0 {
			continue
		}
		if err := store.WaitWALBudget(ctx, c.db); err != nil {
			return promoted, err
		}
		res, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET browsable=1, added_at=CASE WHEN added_at=0 THEN ? ELSE added_at END WHERE browsable=0 AND key IN (`+placeholders(len(promote))+`)`,
			append([]any{now.Unix()}, promote...)...)
		if err != nil {
			return promoted, err
		}
		n, _ := res.RowsAffected()
		promoted += int(n)
		c.writeGen.Add(1)
	}
	return promoted, nil
}

// promoteReleased makes stored titles browsable whose release date has
// passed since the last pass and that meet the bar: titles the fill stored
// before release are not listed again by /discover. The read is a range of
// catalog_titles_type_browse_released (type, browsable, released_ts).
func (c *Catalog) promoteReleased(ctx context.Context) (int, error) {
	now := c.now().Unix()
	var since int64
	if v := c.getMeta(ctx, "promote_released_ts"); v != "" {
		fmt.Sscan(v, &since)
	}
	total := 0
	for _, typ := range []core.MediaType{core.TypeMovie, core.TypeShow} {
		afterTS, afterKey := since, int64(0)
		for {
			rows, err := c.db.QueryContext(ctx, `SELECT key, released_ts FROM catalog_titles
				WHERE type=? AND browsable=0 AND released_ts<=? AND (released_ts>? OR (released_ts=? AND key>?))
				ORDER BY released_ts, key LIMIT 500`, string(typ), now, afterTS, afterTS, afterKey)
			if err != nil {
				return total, err
			}
			var keys []core.RatingKey
			for rows.Next() {
				if err := rows.Scan(&afterKey, &afterTS); err != nil {
					rows.Close()
					return total, err
				}
				keys = append(keys, core.RatingKey(afterKey))
			}
			rows.Close()
			if err := rows.Err(); err != nil {
				return total, err
			}
			n, err := c.promoteKeys(ctx, keys)
			total += n
			if err != nil {
				return total, err
			}
			if len(keys) < 500 {
				break
			}
		}
	}
	if err := c.setMeta(ctx, "promote_released_ts", fmt.Sprint(now)); err != nil {
		return total, err
	}
	return total, nil
}
