package availability

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Background indexer traffic follows Radarr's model. Each indexer's release
// feed is read once per sync interval (plus the pages a gap needs) and
// matched locally against every waiting title; a title is searched by id
// only on events (see probeDue). Every request is admitted against two daily
// allowances: the owner's total (feedRequests) and the indexer's own
// (config.Availability.IndexerAllowance: its share of the indexer's API
// limit, or a conservative default). Scheduled head polls are reserved in
// both, so gap pages and title searches can never starve the feeds.

// requestKind is what a background indexer request is for.
type requestKind int

const (
	headPoll      requestKind = iota // a feed's scheduled first page
	feedPage                         // a further page closing a gap, or catch-up
	titleSearch                      // a targeted search for one title
	verifyCheck                      // optional source verification
	channelSearch                    // explicit active-channel demand
)

// indexerBucket is the availability_budget resource for one indexer.
func indexerBucket(id string) string { return "indexer:" + id }

func (s *Service) budgetContext(ctx context.Context, kind requestKind) context.Context {
	cfg := s.cfg().Availability
	feed := kind != verifyCheck
	// Feed polls are re-tagged as feed by the indexer manager.
	ctx = core.WithIndexerPurpose(ctx, core.IndexerPurposeAvailability)
	ctx = core.WithForegroundReserve(ctx, cfg.ReservePercent)
	return core.WithBackgroundBudget(ctx, func(ctx context.Context, resource, source string, n int64) error {
		live := s.cfg().Availability
		if s.d.Active != nil && s.d.Active() && !feed {
			return context.Canceled
		}
		if (feed && kind != channelSearch && !live.Monitor) || (!feed && !live.Verify) {
			return context.Canceled
		}
		switch resource {
		case "bytes":
			return s.spend(ctx, "bytes", n, live.CheckBytes)
		case "grab":
			return s.spend(ctx, "grabs", n, int64(live.Grabs))
		case "debrid":
			if !live.Debrid {
				return core.ErrBudgetExhausted
			}
		}
		budgets := s.budgets()
		sources := s.d.Feeds.FeedSources()
		if f, ok := sourceByID(sources, source); ok && resource == "hit" {
			limit := int64(budgets.IndexerAllowance(f.DailyLimit))
			if kind != headPoll {
				left, err := s.headPollsLeft(ctx, budgets, []core.ReleaseFeed{f})
				if err != nil {
					return err
				}
				limit -= left
			}
			if err := s.spend(ctx, indexerBucket(f.ID), n, max(0, limit)); err != nil {
				return err
			}
			if err := s.spendTotal(ctx, kind, budgets, sources, n); err != nil {
				s.refund(ctx, indexerBucket(f.ID), n)
				return err
			}
			return s.pace(ctx, f.ID)
		}
		return s.spendTotal(ctx, kind, budgets, sources, n)
	})
}

// spendTotal admits n requests against the owner's daily total.
func (s *Service) spendTotal(ctx context.Context, kind requestKind, budgets config.Availability, sources []core.ReleaseFeed, n int64) error {
	if kind == verifyCheck {
		return s.spend(ctx, "checkRequests", n, int64(budgets.CheckRequests))
	}
	limit := int64(budgets.FeedRequests)
	if kind != headPoll {
		var feeds []core.ReleaseFeed
		for _, f := range sources {
			if f.Supported && selected(budgets.IndexerIDs, f.ID) {
				feeds = append(feeds, f)
			}
		}
		left, err := s.headPollsLeft(ctx, budgets, feeds)
		if err != nil {
			return err
		}
		limit -= left
	}
	return s.spend(ctx, "feedRequests", n, max(0, limit))
}

// headPollsLeft counts the head polls still scheduled today (UTC) for the
// feeds among sources: what the rest of today's allowance must keep back.
func (s *Service) headPollsLeft(ctx context.Context, budgets config.Availability, sources []core.ReleaseFeed) (int64, error) {
	now := s.now().UTC()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
	var left int64
	for _, f := range sources {
		if !f.Supported || !selected(budgets.IndexerIDs, f.ID) {
			continue
		}
		st, err := s.feedState(ctx, f)
		if err != nil {
			return 0, err
		}
		at := st.polled
		interval := pollInterval(f, budgets)
		next := time.Unix(at, 0).Add(interval)
		if at == 0 || next.Before(now) {
			next = now
		}
		if next.Before(midnight) {
			left += int64((midnight.Sub(next)-1)/interval) + 1
		}
	}
	return left, nil
}

// pollInterval is how often a feed's head is read: the owner's interval,
// stretched so head polls take at most half of the indexer's allowance.
func pollInterval(f core.ReleaseFeed, budgets config.Availability) time.Duration {
	interval := time.Duration(max(5, budgets.PollMinutes)) * time.Minute
	if polls := budgets.IndexerAllowance(f.DailyLimit) / 2; polls > 0 {
		interval = max(interval, (24*time.Hour+time.Duration(polls)-1)/time.Duration(polls))
	} else {
		interval = max(interval, 24*time.Hour)
	}
	return interval
}

func sourceByID(sources []core.ReleaseFeed, id string) (core.ReleaseFeed, bool) {
	if id == "" {
		return core.ReleaseFeed{}, false
	}
	for _, f := range sources {
		if f.ID == id {
			return f, true
		}
	}
	return core.ReleaseFeed{}, false
}

// pageRoom reports whether f may spend on a gap page now: its own
// allowance and the owner's total both have room beyond today's remaining
// head polls. A gap waits for the next head poll otherwise, instead of
// asking for a denial every minute.
func (s *Service) pageRoom(ctx context.Context, f core.ReleaseFeed, budgets config.Availability) (bool, error) {
	own, err := s.headPollsLeft(ctx, budgets, []core.ReleaseFeed{f})
	if err != nil {
		return false, err
	}
	if s.used(ctx, indexerBucket(f.ID))+own >= int64(budgets.IndexerAllowance(f.DailyLimit)) {
		return false, nil
	}
	all, err := s.headPollsLeft(ctx, budgets, s.d.Feeds.FeedSources())
	if err != nil {
		return false, err
	}
	return s.used(ctx, "feedRequests")+all < int64(budgets.FeedRequests), nil
}

// used is today's count for an allowance.
func (s *Service) used(ctx context.Context, kind string) int64 {
	var n int64
	_ = s.db.QueryRowContext(ctx, `SELECT used FROM availability_budget WHERE day=? AND resource=?`, s.now().UTC().Format("2006-01-02"), kind).Scan(&n)
	return n
}

// pace waits until the indexer's next background slot (Deps.Pace apart).
func (s *Service) pace(ctx context.Context, id string) error {
	if s.d.Pace <= 0 {
		return nil
	}
	s.paceMu.Lock()
	if s.paced == nil {
		s.paced = map[string]time.Time{}
	}
	now := time.Now()
	at := s.paced[id]
	if at.Before(now) {
		at = now
	}
	s.paced[id] = at.Add(s.d.Pace)
	s.paceMu.Unlock()
	if wait := at.Sub(now); wait > 0 {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}

// refund returns n admitted requests that were not made after all.
func (s *Service) refund(ctx context.Context, kind string, n int64) {
	day := s.now().UTC().Format("2006-01-02")
	if _, err := s.db.ExecContext(ctx, `UPDATE availability_budget SET used=CASE WHEN used>? THEN used-? ELSE 0 END WHERE day=? AND resource=?`, n, n, day, kind); err != nil {
		s.log.Debug("availability: refund allowance", "resource", kind, "err", err)
	}
}

// ChannelCheckContext admits demand-driven channel checks through the same
// per-indexer allowances and reserved foreground capacity as title probes.
func (s *Service) ChannelCheckContext(ctx context.Context) context.Context {
	return s.budgetContext(ctx, channelSearch)
}
