package catalog

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// The fill fetches titles the export lists and the mirror lacks, one id a
// request, most popular first. It asks for the lean /movie/{id} and
// /tv/{id} (no append_to_response) and stores a summary row, the shape a
// /discover result gives: title, original title, overview, release date,
// popularity, votes, rating, adult, original language, genres, poster and
// backdrop, plus the IMDb id, runtime and status. Credits, keywords,
// content rating, logos, collections, alternative titles, TVDB ids and
// seasons arrive when WarmDetails or a Get fetches details, as for any
// discovered title.
//
// A fetched title that does not meet the browse bar (unreleased, undated)
// is stored too, not browsable, so it is not fetched again;
// promoteReleased lists it when its date passes, and /changes refreshes it
// when TMDB edits it.

// summarySource is the lean per-id fetch (*tmdb.Client).
type summarySource interface {
	MovieSummary(ctx context.Context, id int64) (*tmdb.MovieDetails, error)
	TVSummary(ctx context.Context, id int64) (*tmdb.TVDetails, error)
}

// fillWorkers keeps TMDB's 40 requests a second busy at ~200 ms latency.
const fillWorkers = 8

// syncFillBudget is how many ids a sync fetches itself; the rest of the
// backlog is fetched by fillLoop between syncs, so a first sync under Off
// (about 1.5M ids) does not hold the sync for hours.
const syncFillBudget = 5000

// bulkFillGap is the backlog from which a /discover walk (20 titles a
// request) runs before the per-id fill (1 a request).
const bulkFillGap = 2000

// fillState is the backlog left for fillLoop.
type fillState struct {
	mu      sync.Mutex
	backlog map[bool][]uint64 // by tv; packFill, most popular first
	missed  map[core.RatingKey]bool
	wake    chan struct{}
}

func (f *fillState) set(tv bool, v []uint64) {
	f.mu.Lock()
	if f.backlog == nil {
		f.backlog = map[bool][]uint64{}
	}
	f.backlog[tv] = v
	wake := f.wake
	f.mu.Unlock()
	if wake != nil && len(v) > 0 {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
}

// take removes up to n ids of one type, movies first.
func (f *fillState) take(n int) (bool, []uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, tv := range []bool{false, true} {
		v := f.backlog[tv]
		if len(v) == 0 {
			continue
		}
		n = min(n, len(v))
		out := append([]uint64(nil), v[:n]...)
		f.backlog[tv] = v[n:]
		return tv, out
	}
	return false, nil
}

func (f *fillState) pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.backlog[false]) + len(f.backlog[true])
}

func (f *fillState) miss(k core.RatingKey) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.missed == nil || len(f.missed) >= 100000 {
		f.missed = map[core.RatingKey]bool{}
	}
	f.missed[k] = true
}

func (f *fillState) wasMissed(k core.RatingKey) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.missed[k]
}

// FillPending is how many export ids the mirror still has to fetch.
func (c *Catalog) FillPending() int { return c.fill.pending() }

// fillLoop fetches the backlog while no sync runs.
func (c *Catalog) fillLoop(ctx context.Context) {
	c.fill.mu.Lock()
	if c.fill.wake == nil {
		c.fill.wake = make(chan struct{}, 1)
	}
	wake := c.fill.wake
	c.fill.mu.Unlock()
	for ctx.Err() == nil {
		if c.syncing.Load() {
			if !sleepCtx(ctx, 5*time.Second) {
				return
			}
			continue
		}
		tv, chunk := c.fill.take(500)
		if len(chunk) == 0 {
			select {
			case <-ctx.Done():
				return
			case <-wake:
			}
			continue
		}
		if _, err := c.fillByID(ctx, tv, chunk); err != nil && ctx.Err() == nil {
			c.log.Warn("catalog: fill titles from the TMDB export", "tv", tv, "err", err)
			// Put the chunk back and wait: TMDB is down or the disk is full.
			c.fill.mu.Lock()
			c.fill.backlog[tv] = append(chunk, c.fill.backlog[tv]...)
			c.fill.mu.Unlock()
			if !sleepCtx(ctx, time.Minute) {
				return
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// fillByID fetches and stores ids (packFill values) of one type, with
// fillWorkers requests in flight and one writer, so fetching and writing
// overlap. A 404 is remembered and skipped for the life of the process.
// It returns how many titles it stored.
func (c *Catalog) fillByID(ctx context.Context, tv bool, ids []uint64) (int, error) {
	if len(ids) == 0 || c.tmdb() == nil {
		return 0, nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int64)
	type result struct {
		t   core.Title
		err error
		key core.RatingKey
	}
	results := make(chan result, fillWorkers*2)
	var wg sync.WaitGroup
	for range fillWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				t, err := c.fetchSummary(ctx, tv, id)
				select {
				case results <- result{t: t, err: err, key: titleKey(tv, id)}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, v := range ids {
			k := titleKey(tv, fillID(v))
			if c.fill.wasMissed(k) {
				continue
			}
			select {
			case jobs <- fillID(v):
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() { wg.Wait(); close(results) }()
	w := newSyncWriter(c)
	stored, failures := 0, 0
	var firstErr error
	for r := range results {
		if r.err != nil {
			if errors.Is(r.err, core.ErrNotFound) {
				c.fill.miss(r.key)
				continue
			}
			failures++
			if firstErr == nil {
				firstErr = r.err
			}
			// TMDB unavailable (breaker open, quota): stop and retry later.
			if failures >= 20 || errors.Is(r.err, tmdb.ErrUnavailable) || errors.Is(r.err, tmdb.ErrNoKey) {
				cancel()
				for range results {
				}
				break
			}
			continue
		}
		if err := w.add(ctx, r.t, c.meetsThreshold(r.t)); err != nil {
			cancel()
			for range results {
			}
			return stored, err
		}
		stored++
		c.setProgress(func(p *SyncProgress) { p.TitlesSeen++ })
	}
	if err := w.flush(context.WithoutCancel(ctx)); err != nil {
		return stored, err
	}
	if failures >= 20 || errors.Is(firstErr, tmdb.ErrUnavailable) || errors.Is(firstErr, tmdb.ErrNoKey) {
		return stored, fmt.Errorf("catalog: fill by id: %w", firstErr)
	}
	return stored, nil
}

// fetchSummary is the lean fetch of one title as a summary row.
func (c *Catalog) fetchSummary(ctx context.Context, tv bool, id int64) (core.Title, error) {
	client := c.tmdb()
	lean, ok := client.(summarySource)
	if tv {
		var d *tmdb.TVDetails
		var err error
		if ok {
			d, err = lean.TVSummary(ctx, id)
		} else {
			d, err = client.TVDetails(ctx, id)
		}
		if err != nil {
			return core.Title{}, err
		}
		if d.ID == 0 {
			return core.Title{}, fmt.Errorf("%w: tv %d", core.ErrNotFound, id)
		}
		return c.tvSummaryToTitle(ctx, d), nil
	}
	var d *tmdb.MovieDetails
	var err error
	if ok {
		d, err = lean.MovieSummary(ctx, id)
	} else {
		d, err = client.MovieDetails(ctx, id)
	}
	if err != nil {
		return core.Title{}, err
	}
	if d.ID == 0 {
		return core.Title{}, fmt.Errorf("%w: movie %d", core.ErrNotFound, id)
	}
	return c.movieSummaryToTitle(ctx, d), nil
}

// movieSummaryToTitle is a /discover-shaped row from a lean movie fetch.
func (c *Catalog) movieSummaryToTitle(ctx context.Context, d *tmdb.MovieDetails) core.Title {
	t := c.movieToTitle(ctx, d.Movie)
	t.IDs.IMDB = d.IMDBID
	t.Status = d.Status
	if d.Runtime > 0 {
		t.Runtime = time.Duration(d.Runtime) * time.Minute
	}
	return t
}

// tvSummaryToTitle is a /discover-shaped row from a lean series fetch.
func (c *Catalog) tvSummaryToTitle(ctx context.Context, d *tmdb.TVDetails) core.Title {
	t := c.tvToTitle(ctx, d.TVShow)
	t.Status = d.Status
	t.LastAirDate = tmdb.ParseDate(d.LastAirDate)
	if len(d.EpisodeRunTime) > 0 && d.EpisodeRunTime[0] > 0 {
		t.Runtime = time.Duration(d.EpisodeRunTime[0]) * time.Minute
	}
	return t
}

// syncWriter batches synced titles into upsertMany transactions sized to
// hold the write lock about syncWriteHold, behind WAL back-pressure.
// Browsable and not-yet-browsable titles go in separate upserts (the
// browsable flag is per call). onFlush runs after each stored batch (the
// /discover walk saves its resume point there).
type syncWriter struct {
	c        *Catalog
	show     []core.Title
	hide     []core.Title
	onFlush  func(context.Context) error
	onStored func(core.Title)
}

func newSyncWriter(c *Catalog) *syncWriter { return &syncWriter{c: c} }

func (w *syncWriter) limit() int {
	if n := int(w.c.syncBatch.Load()); n > 0 {
		return n
	}
	return syncWriteBatch
}

func (w *syncWriter) add(ctx context.Context, t core.Title, browsable bool) error {
	if browsable {
		w.show = append(w.show, t)
	} else {
		w.hide = append(w.hide, t)
	}
	if len(w.show)+len(w.hide) >= w.limit() {
		return w.flush(ctx)
	}
	return nil
}

func (w *syncWriter) pending() int { return len(w.show) + len(w.hide) }

func (w *syncWriter) setFlushHook(fn func(context.Context) error) { w.onFlush = fn }

func (w *syncWriter) flush(ctx context.Context) error {
	n := len(w.show) + len(w.hide)
	if n > 0 {
		// Back-pressure, not failure: wait for a long read to finish and
		// the checkpoint to drain.
		if err := store.WaitWALBudget(ctx, w.c.db); err != nil {
			w.show, w.hide = w.show[:0], w.hide[:0] // the retry fetches them again
			return err
		}
		started := time.Now()
		if err := w.c.upsertMany(ctx, w.show, upsertOpts{browsable: true}); err != nil {
			w.show, w.hide = w.show[:0], w.hide[:0]
			return err
		}
		if err := w.c.upsertMany(ctx, w.hide, upsertOpts{}); err != nil {
			w.show, w.hide = w.show[:0], w.hide[:0]
			return err
		}
		w.c.syncBatch.Store(int64(nextSyncBatch(w.limit(), n, time.Since(started))))
		added := len(w.show)
		if w.onStored != nil {
			for _, t := range w.show {
				w.onStored(t)
			}
			for _, t := range w.hide {
				w.onStored(t)
			}
		}
		w.show, w.hide = w.show[:0], w.hide[:0]
		w.c.setProgress(func(p *SyncProgress) { p.TitlesAdded += added })
		w.c.clearSyncError(ctx)
	}
	if w.onFlush != nil {
		return w.onFlush(ctx)
	}
	return nil
}
