package catalog

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/schedule"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// SyncProgress describes a running or last-finished catalogue sync.
type SyncProgress struct {
	Running     bool      `json:"running"`
	Phase       string    `json:"phase,omitempty"` // e.g. "movie/popularity", "tv/votes/1994"
	PagesDone   int       `json:"pagesDone"`
	TitlesSeen  int       `json:"titlesSeen"`
	TitlesAdded int       `json:"titlesAdded"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
	FinishedAt  time.Time `json:"finishedAt,omitempty"`
	LastError   string    `json:"lastError,omitempty"`
}

// Stats is the admin-UI snapshot of the mirror.
type Stats struct {
	Trending           []TrendStatus     `json:"trending"`
	Movies             int               `json:"movies"`
	Shows              int               `json:"shows"`
	Seasons            int               `json:"seasons"`
	Episodes           int               `json:"episodes"`
	HomeReleaseChecked int               `json:"homeReleaseChecked"`
	HomeReleasePending int               `json:"homeReleasePending"`
	BrowsableMovies    int               `json:"browsableMovies"`
	BrowsableShows     int               `json:"browsableShows"`
	Collections        int               `json:"collections"`
	LastSync           time.Time         `json:"lastSync,omitempty"`
	Sync               SyncProgress      `json:"sync"`
	YearCorrection     MovieYearProgress `json:"yearCorrection"`
	TierMigration      TierMigration     `json:"tierMigration,omitzero"`
	// Tail is this node's long-tail search index.
	Tail TailStatus `json:"tail"`
}

func cloneStats(s Stats) Stats {
	if s.Trending != nil {
		tr := make([]TrendStatus, len(s.Trending))
		copy(tr, s.Trending)
		s.Trending = tr
	}
	return s
}

// Stats returns mirror counts, last sync time and current sync progress.
// The counts come from catalog_stats_tally, so they are exact on every
// call at a cost that does not grow with the library. The last snapshot
// answers when the database cannot (a busy or timed-out status call).
func (c *Catalog) Stats(ctx context.Context) (Stats, error) {
	s, yearPending, err := c.loadStats(ctx)
	if err != nil {
		c.cacheMu.RLock()
		fallback := c.statsCache
		c.cacheMu.RUnlock()
		if fallback.ready {
			return c.finishStats(ctx, cloneStats(fallback.stats), fallback.yearPending), nil
		}
		return s, err
	}
	c.cacheMu.Lock()
	c.statsCache = cachedStats{stats: cloneStats(s), yearPending: yearPending, ready: true}
	c.cacheMu.Unlock()
	return c.finishStats(ctx, s, yearPending), nil
}

func (c *Catalog) finishStats(ctx context.Context, s Stats, yearPending int) Stats {
	s.Sync = c.SyncProgress()
	if s.Sync.LastError == "" && !s.Sync.Running {
		s.Sync.LastError = c.getMeta(ctx, "sync_error")
	}
	s.YearCorrection = c.movieYearProgressFrom(yearPending)
	s.TierMigration = c.TierMigrationStatus(ctx)
	s.Tail = c.TailStatus()
	return s
}

func (c *Catalog) loadStats(ctx context.Context) (Stats, int, error) {
	var s Stats
	yearPending, err := c.tallyStats(ctx, &s)
	if err != nil {
		return s, 0, err
	}
	s.Trending = c.TrendStatus(ctx)
	if v := c.getMeta(ctx, "last_sync"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil {
			s.LastSync = time.Unix(n, 0).UTC()
		}
	}
	return s, yearPending, nil
}

func (c *Catalog) setProgress(fn func(*SyncProgress)) {
	c.progressMu.Lock()
	fn(&c.progress)
	c.progressMu.Unlock()
}

// SyncProgress returns the current sync progress snapshot.
func (c *Catalog) SyncProgress() SyncProgress {
	c.progressMu.RLock()
	defer c.progressMu.RUnlock()
	p := c.progress
	p.Running = c.syncing.Load()
	return p
}

// Run performs an initial sync and then refreshes on RefreshInterval until ctx ends.
// Intended to be run in its own goroutine by the app wiring. With
// WithResumeSchedule the initial sync waits until the interval has passed
// since the last recorded sync.
func (c *Catalog) Run(ctx context.Context) {
	// Everything Run starts is bulk work: it yields TMDB's budget to
	// viewer-facing requests (tmdb.Background).
	ctx = tmdb.Background(ctx)
	if err := c.rankRatings(ctx); err != nil && ctx.Err() == nil {
		c.log.Error("catalog: re-rank content ratings", "err", err)
	}
	go func() {
		if err := c.backfillChildRatings(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn("catalog: season and episode ratings", "err", err)
		}
	}()
	go c.refreshRatingsLoop(ctx)
	go c.refreshShowAddedLoop(ctx)
	go c.foldLibraryTalliesLoop(ctx)
	go c.correctMovieYears(ctx)
	go c.fillLoop(ctx)
	go c.runTierMigration(ctx)
	go c.rankLoop(ctx)
	full := schedule.New(c.now(), c.untilDue(ctx))
	// Preserve the existing resume behavior while the gate is off. If the
	// owner enables it during the wait, start background backfill promptly.
	for !(c.config().ReleaseMode == config.ModeDigital) && full.Remaining(c.now()) > 0 {
		if !schedule.Wait(ctx, c.now, min(schedule.MaxWait, full.Remaining(c.now()))) {
			return
		}
	}
	warm := schedule.New(c.now(), 90*time.Second)
	recent := schedule.New(c.now(), 4*time.Hour)
	failures := 0
	for ctx.Err() == nil {
		if full.Remaining(c.now()) == 0 {
			err := c.Sync(ctx)
			delay := c.refreshInterval()
			switch {
			case err == nil || ctx.Err() != nil:
				failures = 0
			case errors.Is(err, tmdb.ErrNoKey):
				// A fresh install before setup: expected, not an error.
				c.log.Info("catalog: waiting for a TMDB API key (add it in setup or Integrations)")
				delay = min(delay, 5*time.Minute)
			default:
				failures++
				delay = syncRetryDelay(failures)
				c.log.Error("catalog: refresh sync", "err", err, "retry_after", delay)
			}
			full = schedule.New(c.now(), delay)
		}
		if ctx.Err() != nil {
			return
		}
		if recent.Remaining(c.now()) == 0 {
			if !c.syncing.Load() {
				c.syncNewReleases(ctx)
			}
			recent = schedule.New(c.now(), 4*time.Hour)
		}
		if ctx.Err() != nil {
			return
		}
		if warm.Remaining(c.now()) == 0 {
			if !c.syncing.Load() {
				if c.config().ReleaseMode == config.ModeDigital {
					_, _ = c.BackfillHomeReleases(ctx, 150)
					_, _ = c.WarmDetails(ctx, 10)
				} else {
					_, _ = c.WarmDetails(ctx, 25)
				}
			}
			warm = schedule.New(c.now(), 90*time.Second)
		}
		delay := min(schedule.MaxWait, full.Remaining(c.now()), warm.Remaining(c.now()), recent.Remaining(c.now()))
		if !schedule.Wait(ctx, c.now, delay) {
			return
		}
	}
}

func (c *Catalog) refreshInterval() time.Duration {
	if d := c.config().RefreshInterval.D(); d > 0 {
		return d
	}
	return 24 * time.Hour
}

// untilDue is how long Run waits before its first sync: zero unless
// resuming the recorded schedule and the last sync is recent.
func (c *Catalog) untilDue(ctx context.Context) time.Duration {
	if c.getMeta(ctx, "sync_resume") != "" {
		return 0
	}
	if !c.resumeSchedule {
		return 0
	}
	n, err := strconv.ParseInt(c.getMeta(ctx, "last_sync"), 10, 64)
	if err != nil || n <= 0 {
		return 0 // never synced: sync now
	}
	return min(c.refreshInterval(), max(time.Unix(n, 0).Add(c.refreshInterval()).Sub(c.now().Round(0)), 0))
}

// Sync (re)builds the browsable set from TMDB discover. A second call while
// one is running returns ErrSyncInProgress; use SyncProgress to poll.
var ErrSyncInProgress = fmt.Errorf("catalog: sync already in progress")

func (c *Catalog) Sync(ctx context.Context) error {
	ctx = tmdb.Background(ctx)
	if !c.syncing.CompareAndSwap(false, true) {
		return ErrSyncInProgress
	}
	defer c.syncing.Store(false)
	c.setProgress(func(p *SyncProgress) {
		*p = SyncProgress{Running: true, StartedAt: c.now(), LastError: c.getMeta(ctx, "sync_error")}
	})
	resumeCtx, err := c.resumeSync(ctx)
	if err == nil {
		if c.tmdb() == nil {
			err = fmt.Errorf("catalog: sync: no TMDB client")
		} else {
			err = c.syncLocked(resumeCtx)
		}
	}
	if err == nil {
		tx, txErr := c.db.BeginTx(ctx, nil)
		if txErr == nil {
			_, txErr = tx.ExecContext(ctx, `DELETE FROM catalog_meta WHERE k IN ('sync_resume','sync_error')`)
			if txErr == nil {
				_, txErr = tx.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES('last_sync',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, strconv.FormatInt(c.now().Unix(), 10))
			}
			if txErr == nil {
				txErr = tx.Commit()
			} else {
				_ = tx.Rollback()
			}
		}
		err = txErr
	}
	if err != nil {
		_ = c.setMeta(ctx, "sync_error", err.Error())
	}
	c.setProgress(func(p *SyncProgress) {
		p.Running = false
		p.FinishedAt = c.now()
		p.Phase = ""
		if err != nil {
			p.LastError = err.Error()
		} else {
			p.LastError = ""
		}
	})
	return err
}

// clearSyncError forgets the previous attempt's error once this sync has
// stored a page: a retry that makes progress is no longer failing. The
// error comes back if this attempt fails too.
func (c *Catalog) clearSyncError(ctx context.Context) {
	cleared := false
	c.setProgress(func(p *SyncProgress) {
		if p.LastError != "" {
			p.LastError, cleared = "", true
		}
	})
	if cleared {
		if _, err := c.db.ExecContext(ctx, `DELETE FROM catalog_meta WHERE k='sync_error'`); err != nil {
			c.log.Warn("catalog: clear sync error", "err", err)
		}
	}
}

// syncLocked is one sync pass, per type:
//
//  1. the export pass (sync_export.go): today's TMDB id list, popularity
//     for stored titles, and the backlog of titles to fetch;
//  2. under a vote bar, the /discover vote walk: the export has no vote
//     counts, so titles admitted by votes alone come from there;
//  3. a backlog of bulkFillGap or more (a first sync, a lowered bar) is
//     read by the /discover popularity walk first, 20 titles a request;
//  4. the rest of the backlog is fetched by id, most popular first: up to
//     syncFillBudget here, the remainder by fillLoop.
//
// Without an export the sync walks /discover alone, as it did before the
// exports: the popularity walk and, under a vote bar, the vote walk. That
// path cannot see undated titles.
//
// Then /changes refreshes stored titles TMDB edited, stored titles whose
// release date has passed are promoted, and the live release feeds run.
func (c *Catalog) syncLocked(ctx context.Context) error {
	if k, ok := c.tmdb().(interface{ HasKey() bool }); ok && !k.HasKey() {
		return tmdb.ErrNoKey // before any download: a fresh install waits for setup
	}
	cfg := c.config()
	for _, tv := range []bool{false, true} {
		typ := "movie"
		if tv {
			typ = "tv"
		}
		c.setProgress(func(p *SyncProgress) { p.Phase = typ + "/export" })
		pass, err := c.runExportPass(ctx, tv)
		if errors.Is(err, errNoExport) {
			if err := c.walkFallback(ctx, tv); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		stored := map[int64]bool{}
		note := func(t core.Title) { stored[t.IDs.TMDB] = true }
		if cfg.MinVotes > 0 {
			w := newSyncWriter(c)
			w.onStored = note
			if err := c.walkDiscover(ctx, c.voteWalk(tv), w); err != nil {
				return err
			}
		}
		backlog := dropStored(pass.backlog, stored)
		if len(backlog) >= bulkFillGap {
			w := newSyncWriter(c)
			w.onStored = note
			if err := c.walkDiscover(ctx, c.popularityWalk(tv), w); err != nil {
				return err
			}
			backlog = dropStored(backlog, stored)
		}
		now := backlog[:min(len(backlog), syncFillBudget)]
		c.setProgress(func(p *SyncProgress) { p.Phase = typ + "/fill" })
		if _, err := c.fillByID(ctx, tv, now); err != nil {
			return err
		}
		c.fill.set(tv, append([]uint64(nil), backlog[len(now):]...))
	}
	if err := c.syncChanges(ctx); err != nil {
		return err
	}
	if _, err := c.promoteReleased(ctx); err != nil {
		return err
	}
	c.syncNewReleases(ctx)
	// Demote browsable titles that no longer meet the (possibly raised) threshold?
	// Deliberately not done: searched titles joined on demand and owners raising
	// the bar expect new syncs to stop adding, not to hide known titles. The
	// browsable flag is only ever added by sync/search.
	return nil
}

// dropStored removes the ids a walk stored from a fill backlog.
func dropStored(backlog []uint64, stored map[int64]bool) []uint64 {
	if len(stored) == 0 {
		return backlog
	}
	out := backlog[:0:0]
	for _, v := range backlog {
		if !stored[fillID(v)] {
			out = append(out, v)
		}
	}
	return out
}

// popularityWalk reads titles by popularity; under a popularity bar each
// slice stops at the first title under both bars.
func (c *Catalog) popularityWalk(tv bool) walkSpec {
	cfg := c.config()
	return walkSpec{tv: tv, pass: "popularity", stopBelow: cfg.MinPopularity > 0,
		params: tmdb.DiscoverParams{SortBy: "popularity.desc", IncludeAdult: cfg.IncludeAdult}}
}

// voteWalk reads every title at or above the vote bar.
func (c *Catalog) voteWalk(tv bool) walkSpec {
	cfg := c.config()
	return walkSpec{tv: tv, pass: "votes",
		params: tmdb.DiscoverParams{SortBy: "vote_count.desc", VoteCountGTE: cfg.MinVotes, IncludeAdult: cfg.IncludeAdult}}
}

// walkFallback is the sync without an export: /discover alone. A
// votes-only bar needs no popularity walk.
func (c *Catalog) walkFallback(ctx context.Context, tv bool) error {
	cfg := c.config()
	if cfg.MinPopularity > 0 || cfg.MinVotes <= 0 {
		if err := c.walkDiscover(ctx, c.popularityWalk(tv), newSyncWriter(c)); err != nil {
			return err
		}
	}
	if cfg.MinVotes > 0 {
		return c.walkDiscover(ctx, c.voteWalk(tv), newSyncWriter(c))
	}
	return nil
}

// syncWriteBatch is the most synced titles one transaction stores. On the
// lab catalogue: 20 a transaction wrote 20 WAL pages a title, 100 wrote 15,
// 500 wrote 6.7.
const syncWriteBatch = 250

// syncWriteHold is the write-lock hold a sync transaction aims for: every
// other writer (playback progress, receipts) waits behind it. On a large
// catalogue on a spinning disk a title's index pages are mostly not cached
// and each costs a seek, so the batch shrinks until one transaction takes
// about this long, and grows back when the disk keeps up.
const syncWriteHold = 500 * time.Millisecond

func nextSyncBatch(limit, stored int, took time.Duration) int {
	return nextBatch(limit, stored, took, syncWriteBatch)
}

// nextBatch sizes the next write batch so one transaction holds the write
// lock about syncWriteHold, between 1 and most.
func nextBatch(limit, stored int, took time.Duration, most int) int {
	if stored <= 0 {
		return limit
	}
	perTitle := took / time.Duration(stored)
	if perTitle <= 0 {
		return most
	}
	want := int(syncWriteHold / perTitle)
	// Move gradually: halve at most, grow by half at most.
	want = max(want, limit/2, 1)
	want = min(want, limit+limit/2+1, most)
	return want
}

// qualifiesNewRelease admits a recently released title (last 45 days) that
// already has real viewer traction even if its cumulative vote count is still
// building toward the long-term MinVotes bar.
func (c *Catalog) qualifiesNewRelease(t core.Title) bool {
	if c.meetsThreshold(t) {
		return true
	}
	now := c.now()
	if (t.Type != core.TypeMovie && t.Type != core.TypeShow) || t.Adult || !t.IsReleased(now) {
		return false
	}
	if now.Sub(t.Released) > 45*24*time.Hour {
		return false
	}
	cfg := c.config()
	minPop := math.Max(2.5, cfg.MinPopularity*0.6)
	minVotes := max(10, cfg.MinVotes/5)
	return t.Popularity >= minPop && t.VoteCount >= minVotes
}

// syncNewReleases pulls TMDB's live release feeds (now_playing, airing_today,
// on_the_air, trending_day, trending_week) so brand-new theatrical, streaming
// and broadcast releases enter the browsable mirror without waiting for
// long-term vote counts to accumulate. Errors on optional endpoints are
// ignored so minimal test fixtures are unaffected.
func (c *Catalog) syncNewReleases(ctx context.Context) {
	if c.tmdb() == nil || ctx.Err() != nil {
		return
	}
	var batch []core.Title
	seen := map[core.RatingKey]bool{}
	collect := func(ts []core.Title) {
		for _, t := range ts {
			if t.Key == 0 || seen[t.Key] || !c.qualifiesNewRelease(t) {
				continue
			}
			seen[t.Key] = true
			batch = append(batch, t)
		}
	}
	for _, list := range []string{"now_playing", "trending_day", "trending_week"} {
		if items, err := c.fetchList(ctx, list, core.TypeMovie, 40); err == nil {
			collect(items)
		}
	}
	for _, list := range []string{"airing_today", "on_the_air", "trending_day", "trending_week"} {
		if items, err := c.fetchList(ctx, list, core.TypeShow, 40); err == nil {
			collect(items)
		}
	}
	if len(batch) > 0 {
		_ = c.upsertMany(ctx, batch, upsertOpts{browsable: true})
	}
}

// WarmDetails enriches up to limit browsable titles whose detailed metadata
// (credits, crew, keywords, studios, runtime, content rating, seasons and
// crowd recommendations) has not been fetched yet, plus returning series
// whose detail TTL has expired. New releases (<120 days old) are warmed first,
// followed by the most popular and most-voted titles. Settled titles are
// marked detail=1 and never re-queried.
func (c *Catalog) WarmDetails(ctx context.Context, limit int) (int, error) {
	if c.tmdb() == nil || limit <= 0 {
		return 0, nil
	}
	now := c.now()
	recentCutoff := now.Add(-120 * 24 * time.Hour).Unix()
	staleCutoff := now.Add(-c.staleAfter).Unix()
	// Three bounded reads, in priority order: undetailed recent releases
	// (the last 120 days of the release index), undetailed titles by
	// popularity (catalog_titles_browse_warm is in that order, ties by key),
	// then stale returning series (catalog_titles_warm_stale holds returning
	// series by detail time, so only the stale ones are read). One query
	// ordering all three read every browsable title on every pass: 1.3M
	// rows every 90 seconds on a full mirror.
	const eligible = ` browsable=1 AND adult=0 AND type IN ('movie','show') AND release_known=1 AND released_ts<=?`
	var keys []core.RatingKey
	seen := map[core.RatingKey]bool{}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`SELECT key FROM catalog_titles WHERE detail=0 AND` + eligible + ` AND released_ts>=?
			ORDER BY popularity DESC, vote_count DESC, key ASC LIMIT ?`, []any{now.Unix(), recentCutoff}},
		{`SELECT key FROM catalog_titles` + c.indexHint("catalog_titles_browse_warm") + ` WHERE detail=0 AND` + eligible + `
			ORDER BY popularity DESC, vote_count DESC, key ASC LIMIT ?`, []any{now.Unix()}},
		{`SELECT key FROM catalog_titles INDEXED BY catalog_titles_warm_stale WHERE type='show' AND status='Returning Series' AND detail=1 AND detail_at>0 AND detail_at<? AND` + eligible + `
			ORDER BY popularity DESC, vote_count DESC, key ASC LIMIT ?`, []any{staleCutoff, now.Unix()}},
	} {
		if len(keys) >= limit {
			break
		}
		stmt := q.sql
		if store.DialectOf(c.db) == store.Postgres {
			stmt = strings.NewReplacer(" INDEXED BY catalog_titles_warm_stale", "").Replace(stmt)
		}
		// The full limit each time: the recent releases come round again
		// in the popularity read.
		rows, err := c.db.QueryContext(ctx, stmt, append(q.args, limit)...)
		if err != nil {
			return 0, err
		}
		for rows.Next() {
			var k int64
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return 0, err
			}
			if key := core.RatingKey(k); !seen[key] && len(keys) < limit {
				seen[key] = true
				keys = append(keys, key)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return 0, err
		}
	}
	return c.warmKeys(ctx, keys)
}

// warmDetailsWorkers is how many detail fetches WarmDetails keeps in flight.
// One at a time, a fill ran at TMDB's round-trip time (5-10 titles a
// second) instead of its rate limit (40 requests a second); each fetch's
// write still takes the write gate on its own, briefly.
const warmDetailsWorkers = 8

func (c *Catalog) warmKeys(ctx context.Context, keys []core.RatingKey) (int, error) {
	var warmed atomic.Int64
	next := make(chan core.RatingKey)
	var wg sync.WaitGroup
	for range min(warmDetailsWorkers, len(keys)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range next {
				if _, err := c.Get(ctx, k); err == nil {
					warmed.Add(1)
				}
			}
		}()
	}
feed:
	for _, k := range keys {
		select {
		case next <- k:
		case <-ctx.Done():
			break feed
		}
	}
	close(next)
	wg.Wait()
	return int(warmed.Load()), ctx.Err()
}

type personCreditsTMDB interface {
	PersonCombinedCredits(ctx context.Context, id int64) (*tmdb.PersonCombinedCredits, error)
}

// ensurePersonCredits hydrates a person's filmography from TMDB's
// /person/{id}/combined_credits on first browse when the local mirror holds
// only a few detail-enriched titles for them. The read never waits for it:
// detail pages browse three people each, and a synchronous fetch put up to
// three TMDB round trips and their catalogue writes on every cold detail
// and post-play request. The browse serves the credits already stored, and
// the next read has the rest.
func (c *Catalog) ensurePersonCredits(ctx context.Context, personID int64) {
	client := c.tmdb()
	if personID <= 0 || client == nil {
		return
	}
	pc, ok := client.(personCreditsTMDB)
	if !ok {
		return
	}
	c.personMu.Lock()
	if at, seen := c.personHydrated[personID]; seen && c.now().Sub(at) < 24*time.Hour {
		c.personMu.Unlock()
		return
	}
	c.personMu.Unlock()
	_ = c.hydrate(ctx, "person:"+strconv.FormatInt(personID, 10), 0, func(ctx context.Context) error {
		c.hydratePersonCredits(ctx, client, pc, personID)
		return nil
	})
}

func (c *Catalog) hydratePersonCredits(ctx context.Context, client TMDB, pc personCreditsTMDB, personID int64) {
	now := c.now()
	// Stamp only after a successful hydration: stamping before the
	// work would suppress retries for 24h when the credit insert
	// transaction below fails and rolls back.
	stampHydrated := func() {
		c.personMu.Lock()
		if len(c.personHydrated) >= 2048 {
			c.personHydrated = map[int64]time.Time{}
		}
		c.personHydrated[personID] = now
		c.personMu.Unlock()
	}

	var have int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT DISTINCT key FROM catalog_credits WHERE person_tmdb=? LIMIT 8)`, personID).Scan(&have); err == nil && have >= 8 {
		stampHydrated()
		return
	}
	bctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	credits, err := pc.PersonCombinedCredits(bctx, personID)
	if err != nil || credits == nil {
		return
	}
	var personName, personThumb, personIMDB string
	_ = c.db.QueryRowContext(ctx, `SELECT name, thumb FROM catalog_credits WHERE person_tmdb=? AND name<>'' LIMIT 1`, personID).Scan(&personName, &personThumb)
	if personName == "" {
		if pd, ok := client.(personDetailsSource); ok {
			if p, err := pd.PersonDetails(bctx, personID); err == nil && p != nil {
				personName, personThumb = p.Name, p.ProfilePath
				if p.ID == personID && validPersonIMDB(p.IMDBID) {
					personIMDB = p.IMDBID
				}
			}
		}
	}
	type creditLink struct {
		key  core.RatingKey
		role string
		kind string
		ord  int
	}
	var summaries []core.Title
	seenTitle := map[core.RatingKey]bool{}
	var links []creditLink
	addWork := func(w tmdb.PersonCredit, kind, role string, ord int) {
		var t core.Title
		switch w.MediaType {
		case "movie":
			t = c.movieToTitle(ctx, w.AsMovie())
		case "tv":
			t = c.tvToTitle(ctx, w.AsTV())
		default:
			return
		}
		if t.Key == 0 || t.Adult || !t.IsReleased(now) || !c.meetsThreshold(t) {
			return
		}
		if !seenTitle[t.Key] {
			seenTitle[t.Key] = true
			summaries = append(summaries, t)
		}
		links = append(links, creditLink{key: t.Key, role: role, kind: kind, ord: ord})
	}
	for i, w := range credits.Cast {
		addWork(w, "cast", w.Character, i)
	}
	for i, w := range credits.Crew {
		job := strings.ToLower(strings.TrimSpace(w.Job))
		switch job {
		case "director":
			addWork(w, "director", w.Job, i)
		case "writer", "screenplay", "author", "novel", "story", "teleplay":
			addWork(w, "writer", w.Job, i)
		case "director of photography", "cinematography":
			addWork(w, "crew", "Cinematographer", i)
		case "original music composer", "music", "composer":
			addWork(w, "crew", "Composer", i)
		case "creator", "series composition", "showrunner":
			addWork(w, "crew", "Creator", i)
		}
	}
	if len(summaries) > 0 {
		_ = c.upsertMany(ctx, summaries, upsertOpts{browsable: true})
	}
	if len(links) == 0 && personIMDB == "" {
		// Hydration succeeded but there was nothing to persist.
		stampHydrated()
	}
	if len(links) > 0 || personIMDB != "" {
		tx, err := c.db.BeginTx(ctx, nil)
		if err != nil {
			c.log.Warn("catalog: begin person credits", "person", personID, "err", err)
		} else {
			defer tx.Rollback() // after Commit this returns ErrTxDone, ignorable
			var insertErr error
			if personIMDB != "" {
				_, insertErr = tx.ExecContext(ctx, `INSERT INTO catalog_people(person_tmdb,imdb) VALUES(?,?) ON CONFLICT(person_tmdb) DO UPDATE SET imdb=excluded.imdb`, personID, personIMDB)
			}
			for _, l := range links {
				if insertErr != nil {
					break
				}
				if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_credits(key,person_tmdb,name,role,kind,ord,thumb) VALUES(?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
					int64(l.key), personID, personName, l.role, l.kind, l.ord, personThumb); err != nil {
					insertErr = err
					break
				}
			}
			if insertErr != nil {
				c.log.Warn("catalog: insert person credits", "person", personID, "err", insertErr)
			} else if err := tx.Commit(); err != nil {
				c.log.Warn("catalog: commit person credits", "person", personID, "err", err)
			} else {
				c.writeGen.Add(1)
				stampHydrated()
			}
		}
	}
}

func validPersonIMDB(id string) bool {
	if len(id) < 9 || len(id) > 14 || !strings.HasPrefix(id, "nm") {
		return false
	}
	nonzero := false
	for _, c := range id[2:] {
		if c < '0' || c > '9' {
			return false
		}
		nonzero = nonzero || c != '0'
	}
	return nonzero
}
