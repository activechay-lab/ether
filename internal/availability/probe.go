package availability

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// probeDue executes one indexed due-items query against
// availability_title_checks_due and searches at most probeBatch gated titles
// per cycle within the configured daily allowances, popular/watchlisted first.
func (s *Service) probeDue(ctx context.Context) error {
	_, cfg := s.discoverySnap()
	if s.d.Search == nil || !cfg.Monitor {
		return nil
	}
	if err := s.seedDueChecks(ctx, cfg); err != nil {
		return err
	}
	if !s.hasGateTable {
		return nil
	}

	now := s.now()
	nowTS := now.Unix()
	rows, err := s.db.QueryContext(ctx, `SELECT rating_key,kind,anchor_ts,window_end_ts,priority FROM availability_title_checks WHERE next_check>0 AND next_check<=? ORDER BY priority DESC,next_check ASC LIMIT ?`, nowTS, probeBatch)
	if err != nil {
		return err
	}
	type dueRow struct {
		key       core.RatingKey
		kind      string
		anchor    int64
		windowEnd int64
		priority  float64
	}
	var due []dueRow
	for rows.Next() {
		var k int64
		var r dueRow
		if err := rows.Scan(&k, &r.kind, &r.anchor, &r.windowEnd, &r.priority); err != nil {
			rows.Close()
			return err
		}
		r.key = core.RatingKey(k)
		due = append(due, r)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(due) == 0 {
		return err
	}

	gp := s.currentGatePolicy()
	for _, item := range due {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		basePrio := item.priority
		if basePrio >= firstLookPriorityBoost {
			basePrio -= firstLookPriorityBoost
		}
		if item.windowEnd > 0 && nowTS >= item.windowEnd {
			_, _ = s.db.ExecContext(ctx, `UPDATE availability_title_checks SET next_check=0,priority=? WHERE rating_key=?`, basePrio, int64(item.key))
			continue
		}
		if s.isGateOpen(ctx, gp, item.key) {
			_, _ = s.db.ExecContext(ctx, `UPDATE availability_title_checks SET next_check=0,state='found',reason='',priority=? WHERE rating_key=?`, basePrio, int64(item.key))
			continue
		}
		t, e := s.d.Catalog.CachedTitle(ctx, item.key)
		if e != nil || t == nil || t.Adult {
			_, _ = s.db.ExecContext(ctx, `UPDATE availability_title_checks SET next_check=0,priority=? WHERE rating_key=?`, basePrio, int64(item.key))
			continue
		}
		if !t.IsReleased(now) {
			nextAt := nextRetryAfter(item.anchor, item.windowEnd, nowTS)
			if !t.Released.IsZero() && t.Released.Unix() > nowTS && (nextAt == 0 || t.Released.Unix() < nextAt) {
				nextAt = t.Released.Unix()
			}
			_, _ = s.db.ExecContext(ctx, `UPDATE availability_title_checks SET next_check=? WHERE rating_key=?`, nextAt, int64(item.key))
			continue
		}
		err := s.probeOneDue(ctx, *t, item.anchor, item.windowEnd, basePrio)
		if err != nil {
			if benign(err) {
				return err
			}
			s.log.Warn("title probe failed", "title", t.Title, "error", err)
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

// probeOneDue searches the selected sources (and search-only addons) for t one
// at a time in priority order, stopping at the first profile-accepted release.
func (s *Service) probeOneDue(ctx context.Context, t core.Title, anchorTS, windowEndTS int64, basePrio float64) error {
	if t.Type == core.TypeEpisode && t.ShowKey != 0 {
		if show, err := s.d.Catalog.CachedTitle(ctx, t.ShowKey); err == nil && show != nil {
			t.ShowIDs = show.IDs
			if t.ShowTitle == "" {
				t.ShowTitle = show.Title
			}
			t.ShowOriginalTitle = show.OriginalTitle
			t.ShowYear = show.Year
			if t.Language == "" {
				t.Language = show.Language
			}
		}
	}
	req := core.SearchRequest{
		Type:              t.Type,
		IDs:               t.IDs,
		Title:             t.Title,
		OriginalTitle:     t.OriginalTitle,
		Year:              t.Year,
		AlternativeTitles: t.AlternativeTitles,
		Released:          t.Released,
	}
	if t.Type == core.TypeEpisode {
		req.IDs = t.ShowIDs
		req.Title = cmp.Or(t.ShowTitle, t.Title)
		req.OriginalTitle = t.ShowOriginalTitle
		req.Year = cmp.Or(t.ShowYear, t.Year)
		req.Season = t.Season
		req.Episode = t.Episode
		req.EpisodeTitle = t.Title
	}

	sources := s.d.Feeds.FeedSources()
	_, cfg := s.discoverySnap()
	ids := cfg.IndexerIDs
	policy := s.policy()
	quality := ""
	asked, denied, failed := 0, 0, 0

	var targets []core.ReleaseFeed
	for _, f := range sources {
		if selected(ids, f.ID) {
			targets = append(targets, f)
		}
	}
	// Support search-only installations (Stremio add-ons, debrid scrapers) that
	// have no Newznab/Torznab feed sources registered.
	if len(sources) == 0 && len(ids) == 0 {
		targets = []core.ReleaseFeed{{ID: "probe", Name: "probe", Generation: "probe", Supported: true}}
	}

	for _, f := range targets {
		var skip map[string]bool
		if len(sources) > 0 {
			skip = make(map[string]bool, len(sources))
			for _, other := range sources {
				if other.ID != f.ID {
					skip[other.ID] = true
				}
			}
		}
		bctx, cancel := context.WithTimeout(core.WithUnattendedSearch(core.WithIndexerSkip(s.budgetContext(ctx, titleSearch), skip)), probeSourceTimeout)
		releases, err := s.d.Search(bctx, req)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && len(releases) == 0 {
			if errors.Is(err, core.ErrBudgetExhausted) || errors.Is(err, context.Canceled) {
				denied++
			} else {
				failed++
			}
			continue
		}
		asked++
		kept, verifiedHashes := s.cachedTorrentsWithVerified(ctx, releases)
		for _, r := range kept {
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
				src = core.ReleaseFeed{ID: "probe", Name: accepted.Indexer, Generation: "probe", Protocol: accepted.Protocol, Supported: true}
			}
			if e = s.remember(ctx, src, t, accepted, policy); e != nil {
				return e
			}
			if accepted.InfoHash != "" && verifiedHashes[accepted.InfoHash] {
				_ = s.openGate(ctx, s.currentGatePolicy(), t.Key, src.ID, s.now().Unix())
			}
			if s.isGateOpen(ctx, s.currentGatePolicy(), t.Key) {
				return s.saveTitleCheckDue(ctx, t.Key, "found", "", 0, basePrio)
			}
			// Pre-official-date unverified match: queued in availability_releases
			// for verification; keep bounded retry active until verified or date passes.
			nextAt := nextEpisodeRetry(anchorTS, windowEndTS, s.now().Unix(), t.Type == core.TypeEpisode && basePrio >= 3000)
			return s.saveTitleCheckDue(ctx, t.Key, "searching", "", nextAt, basePrio)
		}
	}
	if asked == 0 && failed == 0 && denied > 0 {
		return core.ErrBudgetExhausted
	}
	now := s.now()
	nowTS := now.Unix()
	nextAt := nextEpisodeRetry(anchorTS, windowEndTS, nowTS, t.Type == core.TypeEpisode && basePrio >= 3000)
	if failed > 0 {
		backoff := now.Add(probeRetry).Unix()
		if nextAt == 0 || backoff > nextAt {
			if windowEndTS == 0 || backoff < windowEndTS {
				nextAt = backoff
			}
		}
	}
	if denied > 0 {
		u := now.UTC()
		reset := time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC).Add(time.Duration(uint64(t.Key)*2654435761%3600) * time.Second).Unix()
		if nextAt == 0 || reset > nextAt {
			if windowEndTS == 0 || reset < windowEndTS {
				nextAt = reset
			}
		}
	}
	switch {
	case quality != "":
		return s.saveTitleCheckDue(ctx, t.Key, "quality", quality, nextAt, basePrio)
	case asked == 0:
		return s.saveTitleCheckDue(ctx, t.Key, "none", "Search could not be completed", nextAt, basePrio)
	}
	return s.saveTitleCheckDue(ctx, t.Key, "none", "", nextAt, basePrio)
}

func (s *Service) cachedTorrentsWithVerified(ctx context.Context, rs []core.Release) ([]core.Release, map[string]bool) {
	if s.d.CacheCheck == nil {
		return rs, nil
	}
	var hashes []string
	for _, r := range rs {
		if r.Protocol == core.ProtocolTorrent && r.InfoHash != "" {
			hashes = append(hashes, r.InfoHash)
		}
	}
	if len(hashes) == 0 {
		return rs, nil
	}
	bctx, cancel := context.WithTimeout(s.budgetContext(ctx, verifyCheck), 15*time.Second)
	cached, definitive := s.d.CacheCheck(bctx, hashes)
	cancel()
	if !definitive {
		return rs, nil
	}
	verified := map[string]bool{}
	out := rs[:0]
	for _, r := range rs {
		if r.Protocol == core.ProtocolTorrent && r.InfoHash != "" {
			if !cached[r.InfoHash] {
				continue
			}
			verified[r.InfoHash] = true
		}
		out = append(out, r)
	}
	return out, verified
}

func feedFor(feeds []core.ReleaseFeed, r core.Release) core.ReleaseFeed {
	for _, f := range feeds {
		if f.Name == r.Indexer || f.ID == r.Indexer {
			return f
		}
	}
	return core.ReleaseFeed{}
}

// titleCheck is a title's last probe: when, what it found, and when the next
// scheduled retry runs (zero: nothing pending).
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

func (s *Service) saveTitleCheckDue(ctx context.Context, key core.RatingKey, state, reason string, nextAt int64, priority float64) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO availability_title_checks(rating_key,checked_at,next_check,state,reason,priority) VALUES(?,?,?,?,?,?)
 ON CONFLICT(rating_key) DO UPDATE SET checked_at=excluded.checked_at,next_check=excluded.next_check,state=excluded.state,reason=excluded.reason,priority=excluded.priority`,
		int64(key), s.now().Unix(), nextAt, state, reason, priority)
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
