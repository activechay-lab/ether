package catalog

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/flight"
	"github.com/braedonsaunders/ether/internal/lru"
)

// Posters for titles the catalogue has no row for.
//
// A search hit from the long tail carries the export's original name and,
// when the names file has it, a poster path. For the rest, FillPosters asks
// TMDB once per title with the lean /movie/{id} or /tv/{id} (one request,
// ~1.5 KB, carries poster_path), under a short deadline, at most
// posterLookupsPerPage titles a page, posterLookupWorkers at a time.
// Lookups are deduplicated per key and outlive the request that started
// them: a poster that misses the deadline is cached for the next request,
// and the frontends draw Ether's generated poster for this one. Lookups
// never write rows; opening the title promotes it (Get).
//
// Ranking never sees any of this: callers fill posters on the final,
// already ordered and limited page.

const (
	// posterWait is how long a search waits for poster lookups. TMDB's lean
	// fetch answers in ~100-250 ms, so most lookups miss it and land for the
	// next request; the search budget (150 ms warm p95) is not spent here.
	posterWait = 60 * time.Millisecond
	// posterLookupsPerPage caps the TMDB requests one search page starts.
	posterLookupsPerPage = 8
	// posterLookupWorkers bounds concurrent poster lookups across all
	// requests: one page's lookups run in one round trip. The client's
	// shared limiter (40 a second) still paces them against the sync.
	posterLookupWorkers = posterLookupsPerPage
	// posterQueueWait is how long a lookup waits for a worker before it
	// gives up; the title keeps its generated poster and a later page asks
	// again.
	posterQueueWait     = 3 * time.Second
	posterLookupTimeout = 10 * time.Second
	// posterCacheEntries holds about 5 MB of keys and short paths.
	posterCacheEntries = 64 << 10
	posterFoundTTL     = 30 * 24 * time.Hour
	posterNoneTTL      = 3 * 24 * time.Hour // TMDB has none: ask again in a few days
	posterFailTTL      = 10 * time.Minute   // lookup failed: retry soon
)

// posterInfo is what a lookup learned about one title.
type posterInfo struct {
	Path string // "" when TMDB has no poster
	// title is the lean summary trimmed to what artwork needs (names, year,
	// dates, flags), so a hit's artwork still resolves after its list
	// summary expired. Zero when the lookup failed.
	title   core.Title
	expires time.Time
}

type posterResolver struct {
	mu      sync.Mutex
	cache   *lru.Cache[core.RatingKey, posterInfo]
	flights flight.Group[core.RatingKey, posterInfo]
	workers chan struct{}

	lookups atomic.Int64 // TMDB requests started (tests and measurement)
}

func (r *posterResolver) init() {
	if r.cache == nil {
		r.cache = lru.New(lru.Options[core.RatingKey, posterInfo]{MaxEntries: posterCacheEntries})
		r.workers = make(chan struct{}, posterLookupWorkers)
	}
}

func (r *posterResolver) get(key core.RatingKey, now time.Time) (posterInfo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	info, ok := r.cache.Get(key)
	if ok && !info.expires.After(now) {
		r.cache.Remove(key)
		return posterInfo{}, false
	}
	return info, ok
}

func (r *posterResolver) put(key core.RatingKey, info posterInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	r.cache.Add(key, info)
}

func (r *posterResolver) slots() chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.init()
	return r.workers
}

// PosterLookups reports how many TMDB poster lookups this catalogue started.
func (c *Catalog) PosterLookups() int64 { return c.posters.lookups.Load() }

// FillPosters sets PosterPath on movies and shows that have none and whose
// poster is unknown (unknown reports which), from the poster cache or a TMDB
// lookup that answers within wait. Titles without one after that keep an
// empty path; frontends render Ether's generated poster for them. It never
// reorders titles and never writes rows.
func (c *Catalog) FillPosters(ctx context.Context, titles []core.Title, unknown func(*core.Title) bool, wait time.Duration) {
	now := c.now()
	var pending []int
	for i := range titles {
		t := &titles[i]
		if t.PosterPath != "" || t.Key == 0 || (t.Type != core.TypeMovie && t.Type != core.TypeShow) {
			continue
		}
		if info, ok := c.posters.get(t.Key, now); ok {
			t.PosterPath = info.Path
			continue
		}
		if unknown != nil && !unknown(t) {
			continue
		}
		if len(pending) < posterLookupsPerPage {
			pending = append(pending, i)
		}
	}
	if len(pending) == 0 || c.tmdb() == nil {
		return
	}
	type found struct {
		i    int
		path string
	}
	// Every lookup starts, even with no time to wait for it (a prefetch):
	// only the waiting is bounded. Late answers go to the buffered channel
	// and the poster cache, never to titles.
	results := make(chan found, len(pending))
	for _, i := range pending {
		key := titles[i].Key
		go func() {
			info, err := c.posters.flights.DoDetached(ctx, key, func(bg context.Context) (posterInfo, error) {
				return c.lookupPoster(bg, key)
			})
			if err != nil {
				results <- found{i: -1}
				return
			}
			results <- found{i, info.Path}
		}()
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for range pending {
		select {
		case f := <-results:
			if f.i >= 0 && f.path != "" {
				titles[f.i].PosterPath = f.path
			}
		case <-timer.C:
			return
		case <-ctx.Done():
			return
		}
	}
}

// lookupPoster asks TMDB for one title's poster and caches the answer.
func (c *Catalog) lookupPoster(ctx context.Context, key core.RatingKey) (posterInfo, error) {
	slots := c.posters.slots()
	queue := time.NewTimer(posterQueueWait)
	defer queue.Stop()
	select {
	case slots <- struct{}{}:
	case <-queue.C:
		return posterInfo{}, errPosterBusy
	case <-ctx.Done():
		return posterInfo{}, ctx.Err()
	}
	defer func() { <-slots }()
	// The answer may have landed while this lookup queued.
	if info, ok := c.posters.get(key, c.now()); ok {
		return info, nil
	}
	ctx, cancel := context.WithTimeout(ctx, posterLookupTimeout)
	defer cancel()
	c.posters.lookups.Add(1)
	t, err := c.fetchSummary(ctx, key.Kind() == core.KindShow, key.TMDB())
	now := c.now()
	switch {
	case err == nil:
		info := posterInfo{Path: t.PosterPath, title: artworkSummary(t), expires: now.Add(posterFoundTTL)}
		if info.Path == "" {
			info.expires = now.Add(posterNoneTTL)
		}
		c.posters.put(key, info)
		return info, nil
	case errors.Is(err, core.ErrNotFound):
		info := posterInfo{expires: now.Add(posterNoneTTL)}
		c.posters.put(key, info)
		return info, nil
	default:
		c.posters.put(key, posterInfo{expires: now.Add(posterFailTTL)})
		return posterInfo{}, err
	}
}

var errPosterBusy = errors.New("catalog: poster lookups busy")

// artworkSummary keeps the fields artwork authorization and Ether's
// generated poster read.
func artworkSummary(t core.Title) core.Title {
	return core.Title{Key: t.Key, Type: t.Type, Title: t.Title, OriginalTitle: t.OriginalTitle, Year: t.Year,
		Released: t.Released, Adult: t.Adult, PosterPath: t.PosterPath, BackdropPath: t.BackdropPath,
		IDs: core.IDs{TMDB: t.IDs.TMDB}}
}

// tailPosterUnknown reports a search hit nobody has a poster answer for: a
// long-tail title without a row whose names-file entry has no poster and is
// not marked as having none. TMDB search hits (held as list summaries) and
// stored rows carry TMDB's own answer, so they are never looked up.
func (c *Catalog) tailPosterUnknown(ctx context.Context, t *core.Title) bool {
	c.listSummaryMu.Lock()
	_, held := c.listSummaries[t.Key]
	c.listSummaryMu.Unlock()
	if held {
		return false
	}
	e, ok := c.tailEntry(t.Key)
	if !ok || e.Title.PosterPath != "" || e.PosterNone {
		return false
	}
	_, err := c.loadByKey(ctx, t.Key)
	return errors.Is(err, core.ErrNotFound)
}

// prefetchTailPosters starts poster lookups for the best long-tail hits as
// soon as the tail is searched, so they run while the search waits for
// TMDB; the final page's FillPosters then mostly finds them cached. Hits
// whose poster is already cached get it here.
func (c *Catalog) prefetchTailPosters(ctx context.Context, hits []core.Title) {
	c.FillPosters(ctx, hits, func(t *core.Title) bool { return c.tailPosterUnknown(ctx, t) }, 0)
}
