package availability

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A title probe searches by id for a movie inside the evidence window. A
// release counts only when it would pass the owner's quality profile.
//
// Release feeds do the detecting: each indexer's feed is read once per sync
// and matched locally against every waiting title, at no cost per title.
// A title is searched only on events, never on a timer:
//   - once when it enters the window (its releases predate the feeds),
//   - once when its digital or physical release date arrives (when releases
//     land, including on sources without a feed),
//   - once after its found release proved unplayable,
//   - and to finish a search an outage or the allowance cut short.
//
// Playback still searches every source when a viewer opens or plays it.
// Each probe asks one source at a time in priority order and stops at the
// first acceptable release.
func (s *Service) probeDue(ctx context.Context) error {
	if s.d.Search == nil || !s.cfg().Availability.Monitor {
		return nil
	}
	cat, ok := s.d.Catalog.(interface {
		RecentMovies(context.Context, time.Time) ([]core.Title, error)
	})
	if !ok {
		return nil
	}
	cfg := s.cfg().Availability
	since := s.now().Add(-time.Duration(cfg.EffectiveMovieEvidenceDays()) * 24 * time.Hour)
	movies, err := cat.RecentMovies(ctx, since)
	if err != nil || len(movies) == 0 {
		return err
	}
	homes := map[core.RatingKey]time.Time{}
	checked := map[core.RatingKey]bool{}
	if h, ok := s.d.Catalog.(interface {
		HomeReleases(context.Context, []core.RatingKey) (map[core.RatingKey]time.Time, error)
	}); ok {
		keys := make([]core.RatingKey, len(movies))
		for i := range movies {
			keys[i] = movies[i].Key
		}
		if got, e := h.HomeReleases(ctx, keys); e == nil {
			homes = got
			for k := range got {
				checked[k] = true
			}
		}
	}
	// Titles whose release has since been found unplayable are looked for
	// again, whatever their cadence.
	unavailable := map[core.RatingKey]time.Time{}
	if v, ok := s.d.Validator.(interface {
		KnownUnavailableSince(context.Context, []core.RatingKey) map[core.RatingKey]time.Time
	}); ok {
		keys := make([]core.RatingKey, len(movies))
		for i := range movies {
			keys[i] = movies[i].Key
		}
		unavailable = v.KnownUnavailableSince(ctx, keys)
	}
	watch := map[core.RatingKey]bool{}
	if s.d.Watchlist != nil {
		if keys, e := s.d.Watchlist(ctx); e == nil {
			watch = keys
		}
	}
	type item struct {
		t     core.Title
		score float64
		check titleCheck
	}
	var due []item
	for _, t := range movies {
		if !t.IsReleased(s.now()) {
			continue
		}
		check, err := s.titleCheck(ctx, t.Key)
		if err != nil {
			return err
		}
		state := check.state
		digital := time.Time{}
		if checked[t.Key] {
			digital = homes[t.Key]
		}
		// First look, an unfinished search, or a digital date that arrived
		// since the last look.
		dueNow := state == "" || (!check.next.IsZero() && !s.now().Before(check.next)) ||
			(!digital.IsZero() && !digital.After(s.now()) && digital.After(check.checked))
		if state == "found" {
			// A found title is available like any other; playback searches
			// for itself. It is looked for again only if its release turned
			// out unplayable, which hides it until another is found.
			since, ok := unavailable[t.Key]
			dueNow = ok && since.After(check.checked)
		}
		if !dueNow {
			continue
		}
		score := t.Popularity
		if watch[t.Key] {
			score += 1000
		}
		if !digital.IsZero() && !digital.After(s.now()) {
			score += 2000
		} else if !digital.IsZero() && digital.Sub(s.now()) < 72*time.Hour {
			score += 800
		}
		due = append(due, item{t, score, check})
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].score > due[j].score })
	// At most probeBatch titles per one-minute cycle, most important first:
	// a backlog (a first start, a long stop) drains over minutes instead of
	// bursting at every indexer at once.
	for i, it := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i >= probeBatch {
			break
		}
		err := s.probeOne(ctx, it.t)
		if err != nil {
			if benign(err) {
				return err
			}
			s.log.Warn("title probe failed", "title", it.t.Title, "error", err)
		}
	}
	return nil
}

const (
	// probeBatch bounds the titles probed per monitor cycle (a minute).
	probeBatch = 10
	// probeSourceTimeout bounds one source's answer to a probe.
	probeSourceTimeout = 20 * time.Second
	// probeRetry is when a search an outage cut short is finished.
	probeRetry = 6 * time.Hour
)

// probeOne searches the selected sources for t one at a time, in priority
// order, and stops at the first release the owner's profile accepts.
func (s *Service) probeOne(ctx context.Context, t core.Title) error {
	req := core.SearchRequest{Type: t.Type, IDs: t.IDs, Title: t.Title, OriginalTitle: t.OriginalTitle, Year: t.Year, AlternativeTitles: t.AlternativeTitles, Released: t.Released}
	sources := s.d.Feeds.FeedSources()
	ids := s.cfg().Availability.IndexerIDs
	policy := s.policy()
	quality := ""
	asked, denied, failed := 0, 0, 0
	for _, f := range sources {
		// Background work stays on the indexers the owner gave it
		// (Availability.IndexerIDs); playback still searches every indexer.
		if !selected(ids, f.ID) {
			continue
		}
		skip := make(map[string]bool, len(sources))
		for _, other := range sources {
			if other.ID != f.ID {
				skip[other.ID] = true
			}
		}
		bctx, cancel := context.WithTimeout(core.WithUnattendedSearch(core.WithIndexerSkip(s.budgetContext(ctx, titleSearch), skip)), probeSourceTimeout)
		releases, err := s.d.Search(bctx, req)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && len(releases) == 0 {
			// A spent allowance decides nothing; a slow or failing source
			// is asked again later, never on the next minute's cycle.
			if errors.Is(err, core.ErrBudgetExhausted) || errors.Is(err, context.Canceled) {
				denied++
			} else {
				failed++
			}
			continue
		}
		asked++
		for _, r := range s.cachedTorrents(ctx, releases) {
			accepted, reason, e := s.d.Validator.FeedCandidate(ctx, &t, r)
			if e != nil {
				return e
			}
			if reason != "" {
				if quality == "" {
					quality = reason
				}
				continue
			}
			src := feedFor(sources, accepted)
			if src.ID == "" {
				src = core.ReleaseFeed{ID: "probe", Name: accepted.Indexer, Generation: "probe", Supported: true}
			}
			if e = s.remember(ctx, src, t, accepted, policy); e != nil {
				return e
			}
			return s.saveTitleCheck(ctx, t.Key, "found", "", time.Time{})
		}
	}
	if asked == 0 && failed == 0 && denied > 0 {
		// Nothing was asked: the title stays due and the pass ends.
		return core.ErrBudgetExhausted
	}
	// A search cut short is finished later: after a source failure, or
	// once the daily allowances reset (UTC midnight).
	next := time.Time{}
	if failed > 0 {
		next = s.now().Add(probeRetry)
	}
	if denied > 0 {
		now := s.now().UTC()
		next = time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC).Add(time.Duration(uint64(t.Key)*2654435761%3600) * time.Second)
	}
	switch {
	case quality != "":
		return s.saveTitleCheck(ctx, t.Key, "quality", quality, next)
	case asked == 0:
		return s.saveTitleCheck(ctx, t.Key, "none", "Search could not be completed", next)
	}
	return s.saveTitleCheck(ctx, t.Key, "none", "", next)
}

func (s *Service) cachedTorrents(ctx context.Context, rs []core.Release) []core.Release {
	if s.d.CacheCheck == nil {
		return rs
	}
	var hashes []string
	for _, r := range rs {
		if r.Protocol == core.ProtocolTorrent && r.InfoHash != "" {
			hashes = append(hashes, r.InfoHash)
		}
	}
	if len(hashes) == 0 {
		return rs
	}
	bctx, cancel := context.WithTimeout(s.budgetContext(ctx, verifyCheck), 15*time.Second)
	cached, definitive := s.d.CacheCheck(bctx, hashes)
	cancel()
	if !definitive {
		return rs
	}
	out := rs[:0]
	for _, r := range rs {
		if r.Protocol == core.ProtocolTorrent && r.InfoHash != "" && !cached[r.InfoHash] {
			continue
		}
		out = append(out, r)
	}
	return out
}

func feedFor(feeds []core.ReleaseFeed, r core.Release) core.ReleaseFeed {
	for _, f := range feeds {
		if f.Name == r.Indexer || f.ID == r.Indexer {
			return f
		}
	}
	return core.ReleaseFeed{}
}

// titleCheck is a title's last probe: when, what it found, and when an
// unfinished search is to be finished (zero: nothing pending).
type titleCheck struct {
	checked, next time.Time
	state, reason string
}

func (s *Service) titleCheck(ctx context.Context, key core.RatingKey) (titleCheck, error) {
	var checked, next int64
	var c titleCheck
	err := s.db.QueryRowContext(ctx, `SELECT checked_at,next_check,state,reason FROM availability_title_checks WHERE rating_key=?`, int64(key)).Scan(&checked, &next, &c.state, &c.reason)
	if errors.Is(err, sql.ErrNoRows) {
		return titleCheck{}, nil
	}
	if err != nil {
		return titleCheck{}, err
	}
	c.checked = time.Unix(checked, 0)
	if next > 0 {
		c.next = time.Unix(next, 0)
	}
	return c, nil
}

// saveTitleCheck records a probe; next is when an unfinished search is
// finished (zero: nothing pending).
func (s *Service) saveTitleCheck(ctx context.Context, key core.RatingKey, state, reason string, next time.Time) error {
	nextAt := int64(0)
	if !next.IsZero() {
		nextAt = next.Unix()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO availability_title_checks(rating_key,checked_at,next_check,state,reason) VALUES(?,?,?,?,?)
 ON CONFLICT(rating_key) DO UPDATE SET checked_at=excluded.checked_at,next_check=excluded.next_check,state=excluded.state,reason=excluded.reason`,
		int64(key), s.now().Unix(), nextAt, state, reason)
	return err
}

func catalogHomeReleaseAdmission(earliest time.Time, offsetDays int) time.Time {
	if earliest.IsZero() || offsetDays == 0 {
		return earliest
	}
	return earliest.Add(time.Duration(offsetDays) * 24 * time.Hour)
}

func (s *Service) acceptDigitalReleases(ctx context.Context, keys []core.RatingKey, now time.Time, offsetDays int, acceptable map[core.RatingKey]bool) {
	h, ok := s.d.Catalog.(interface {
		HomeReleases(context.Context, []core.RatingKey) (map[core.RatingKey]time.Time, error)
	})
	if !ok {
		return
	}
	got, err := h.HomeReleases(ctx, keys)
	if err != nil {
		return
	}
	for k, at := range got {
		if !at.IsZero() && !catalogHomeReleaseAdmission(at, offsetDays).After(now) {
			acceptable[k] = true
		}
	}
}
