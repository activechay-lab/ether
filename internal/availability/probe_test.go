package availability

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func probeFixture(t *testing.T, digital time.Duration) (*fixture, *int, *error) {
	t.Helper()
	x := setup(t)
	x.c.Availability.Verify = false
	x.c.Availability.CatchUp = false
	title := x.cat.titles[core.MovieKey(1)]
	title.Released = x.now.Add(-20 * 24 * time.Hour)
	title.IDs.IMDB = "tt0000001"
	x.cat.titles[title.Key] = title
	x.s.d.Catalog = probeCatalog{fakeCatalog: x.cat, homes: map[core.RatingKey]time.Time{title.Key: x.now.Add(digital)}}
	searches := new(int)
	fail := new(error)
	x.s.d.Search = func(context.Context, core.SearchRequest) ([]core.Release, error) {
		*searches++
		return nil, *fail
	}
	return x, searches, fail
}

func (x *fixture) cycleFor(t *testing.T, d time.Duration) {
	t.Helper()
	for end := x.now.Add(d); x.now.Before(end); x.now = x.now.Add(time.Minute) {
		if err := x.s.Cycle(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
}

// A movie whose digital anchor has arrived without a playable release follows
// the bounded retry ladder [0, 30m, 1h, 90m, 2h, 150m, 3h, 4h, 8h] and then
// once per 24h until the evidence window ends, never every minute.
func TestProbeBoundedRetrySchedule(t *testing.T) {
	// TMDB anchors episodes at midnight, rather than at broadcast time.
	anchor := int64(1794441600)
	end := anchor + 86400
	for _, hour := range []int64{8, 16, 22} {
		now := anchor + hour*3600
		if got := nextEpisodeRetry(anchor, end, now, true); got != now+3600 {
			t.Fatalf("followed episode at hour %d: next=%d", hour, got)
		}
	}
	if got := nextEpisodeRetry(anchor, end, anchor+23*3600, true); got != 0 {
		t.Fatalf("retry beyond evidence window: %d", got)
	}
	if got := nextEpisodeRetry(anchor, end, anchor+8*3600, false); got != 0 {
		t.Fatalf("unfollowed episode gained extra searches: %d", got)
	}

	x, searches, _ := probeFixture(t, 0)
	x.cycleFor(t, 8*time.Hour+time.Minute)
	if *searches != 9 {
		t.Fatalf("searched %d times across 8h ladder, want 9", *searches)
	}
	x.cycleFor(t, 15*time.Hour)
	if *searches != 9 {
		t.Fatalf("searched %d times between +8h and +23h, want no extra retries", *searches)
	}
	x.cycleFor(t, 2*time.Hour)
	if *searches != 10 {
		t.Fatalf("searched %d times across +24h boundary, want 10", *searches)
	}
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{x.cat.titles[core.MovieKey(1)]}); len(got) != 0 {
		t.Fatal("a movie without a playable release was ungated")
	}
}

// A future digital date waits for that anchor after the first look instead of
// retrying early, then runs the bounded schedule once the date arrives.
func TestProbeWaitsForAFutureDigitalDate(t *testing.T) {
	x, searches, _ := probeFixture(t, 10*time.Hour)
	x.cycleFor(t, 9*time.Hour)
	if *searches != 1 {
		t.Fatalf("searched %d times before the digital date, want 1", *searches)
	}
	x.cycleFor(t, 75*time.Minute)
	if *searches != 2 {
		t.Fatalf("searched %d times at digital arrival, want 2", *searches)
	}
}

// A search that timed out backs off for probeRetry; a spent budget stays due.
func TestProbeTimeoutIsNotRetriedEveryCycle(t *testing.T) {
	x, searches, fail := probeFixture(t, -time.Hour)
	*fail = context.DeadlineExceeded
	x.cycleFor(t, 30*time.Minute)
	if *searches != 1 {
		t.Fatalf("timed-out search retried %d times in 30 minutes", *searches)
	}

	y, budgetSearches, budgetFail := probeFixture(t, -time.Hour)
	*budgetFail = core.ErrBudgetExhausted
	y.cycleFor(t, 3*time.Minute)
	if *budgetSearches != 3 {
		t.Fatalf("budget-denied title asked %d times, want it to stay due", *budgetSearches)
	}
}
