package livetv

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A show source with a known-unavailable episode baked in would otherwise
// sit in a repeating channel forever, hitting the live broadcast's runtime
// fallback on every single cycle. The resolver should drop it once here.
func TestExpandProgramSourceDropsKnownUnavailableEpisodes(t *testing.T) {
	showKey := core.RatingKey(500)
	unavailable := core.EpisodeKey(int64(showKey), 1, 2)
	s := testService(t, Options{
		Catalog: &showcaseTestCatalog{},
		Availability: availFunc(func(key core.RatingKey) (bool, bool) {
			if key == unavailable {
				return true, false // known, not available
			}
			return false, false // unknown either way - never filtered
		}),
	})

	items, err := s.expandProgramSource(context.Background(), &ProgramSource{Kind: "show", ShowKey: showKey}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("expected 3 remaining episodes, got %d: %+v", len(items), items)
	}
	for _, it := range items {
		if it.Key == unavailable {
			t.Fatalf("known-unavailable episode %v was not dropped: %+v", unavailable, items)
		}
	}
}

// A dropped episode must not stay dropped forever: once it finishes caching
// and the recheck floor has passed, a tune (or guide request) should pick
// it back up without anyone touching the channel's source selection.
func TestRecheckChannelAvailabilityPicksUpRecoveredTitle(t *testing.T) {
	showKey := core.RatingKey(700)
	target := core.EpisodeKey(int64(showKey), 1, 2)
	available := false
	s := testService(t, Options{
		Catalog: &showcaseTestCatalog{},
		Availability: availFunc(func(key core.RatingKey) (bool, bool) {
			if key == target {
				return true, available
			}
			return false, false
		}),
	})

	c := testChannel()
	c.ID = "binge"
	c.Items = nil
	c.Sources = []ProgramSource{{Kind: "show", ShowKey: showKey}}

	saved, err := s.SaveChannel(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Items) != 3 || saved.DroppedUnavailable != 1 {
		t.Fatalf("expected 3 items / 1 dropped after save, got %d items dropped=%d: %+v", len(saved.Items), saved.DroppedUnavailable, saved.Items)
	}

	// The episode finishes caching, but the floor hasn't passed yet - a
	// tune's maybeRecheckAvailability call right now must be a no-op.
	available = true
	s.maybeRecheckAvailability(saved)
	time.Sleep(20 * time.Millisecond)
	reloaded, err := s.Channel(context.Background(), "binge")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DroppedUnavailable != 1 {
		t.Fatalf("expected the drop to still stand before the floor passes, got dropped=%d", reloaded.DroppedUnavailable)
	}

	// Past the floor, maybeRecheckAvailability (as Tune/guide would call it)
	// should actually trigger a re-expand that picks the title back up.
	s.o.Now = func() time.Time { return time.Now().Add(availabilityRecheckFloor + time.Minute) }
	s.maybeRecheckAvailability(reloaded)
	if !waitFor(t, func() bool {
		c, err := s.Channel(context.Background(), "binge")
		return err == nil && c.DroppedUnavailable == 0
	}) {
		t.Fatal("recovered title was never picked back up")
	}
	reloaded, err = s.Channel(context.Background(), "binge")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Items) != 4 {
		t.Fatalf("expected 4 items after recovery, got %d: %+v", len(reloaded.Items), reloaded.Items)
	}
	found := false
	for _, it := range reloaded.Items {
		if it.Key == target {
			found = true
		}
	}
	if !found {
		t.Fatalf("recovered episode %v missing from items: %+v", target, reloaded.Items)
	}
}

// A still-dead drop must not churn the stored row or the guide on every
// retry - only LastExpandedAt should move, holding off the next attempt.
func TestRecheckChannelAvailabilitySkipsRebuildWhenUnchanged(t *testing.T) {
	showKey := core.RatingKey(702)
	target := core.EpisodeKey(int64(showKey), 1, 2)
	s := testService(t, Options{
		Catalog: &showcaseTestCatalog{},
		Availability: availFunc(func(key core.RatingKey) (bool, bool) {
			if key == target {
				return true, false // known, still unavailable
			}
			return false, false
		}),
	})

	// A deterministic, strictly-advancing clock: LastExpandedAt must move
	// between the save and the recheck even if both run within the same
	// wall-clock millisecond.
	var clockMS int64 = 1700000000000
	s.o.Now = func() time.Time {
		clockMS++
		return time.UnixMilli(clockMS)
	}

	c := testChannel()
	c.ID = "still-dead"
	c.Items = nil
	c.Sources = []ProgramSource{{Kind: "show", ShowKey: showKey}}
	saved, err := s.SaveChannel(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	firstExpandedAt := saved.LastExpandedAt

	s.recheckChannelAvailability(context.Background(), "still-dead")

	reloaded, err := s.Channel(context.Background(), "still-dead")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Items) != 3 || reloaded.DroppedUnavailable != 1 {
		t.Fatalf("unchanged recheck must not alter programming, got %d items dropped=%d", len(reloaded.Items), reloaded.DroppedUnavailable)
	}
	if reloaded.LastExpandedAt <= firstExpandedAt {
		t.Fatalf("expected LastExpandedAt to advance even when unchanged, got %d (was %d)", reloaded.LastExpandedAt, firstExpandedAt)
	}
}

// A showcase channel already has its own refresh loop keeping it current
// (showcaseLoop); the event-driven recheck must not duplicate that work.
func TestRecheckChannelAvailabilitySkipsShowcaseChannels(t *testing.T) {
	showKey := core.RatingKey(701)
	target := core.EpisodeKey(int64(showKey), 1, 2)
	available := false
	s := testService(t, Options{
		Catalog: &showcaseTestCatalog{},
		Availability: availFunc(func(key core.RatingKey) (bool, bool) {
			if key == target {
				return true, available
			}
			return false, false
		}),
	})

	c := testChannel()
	c.ID = "showcase-chan"
	c.Items = nil
	c.Sources = []ProgramSource{{Kind: "show", ShowKey: showKey}}
	saved, err := s.SaveChannel(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	if saved.DroppedUnavailable != 1 {
		t.Fatalf("expected 1 dropped title before registering as showcase, got %d", saved.DroppedUnavailable)
	}
	if _, err := s.o.DB.ExecContext(context.Background(),
		`INSERT INTO livetv_showcase(id,channel_id,version,recipe_hash,state,refreshed_ms,attempted_ms) VALUES(?,?,?,?,?,?,?)`,
		"r1", "showcase-chan", 1, "h", showcaseLive, 0, 0); err != nil {
		t.Fatal(err)
	}

	available = true
	s.recheckChannelAvailability(context.Background(), "showcase-chan")

	reloaded, err := s.Channel(context.Background(), "showcase-chan")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Items) != 3 || reloaded.DroppedUnavailable != 1 {
		t.Fatalf("recheck must skip a channel already covered by the showcase refresh loop, got %d items dropped=%d", len(reloaded.Items), reloaded.DroppedUnavailable)
	}
}

// maybeRecheckAvailability must not fire before the floor has passed, even
// though the channel still has a drop recorded.
func TestMaybeRecheckAvailabilityRespectsFloor(t *testing.T) {
	showKey := core.RatingKey(703)
	target := core.EpisodeKey(int64(showKey), 1, 2)
	available := false
	s := testService(t, Options{
		Catalog: &showcaseTestCatalog{},
		Availability: availFunc(func(key core.RatingKey) (bool, bool) {
			if key == target {
				return true, available
			}
			return false, false
		}),
	})

	c := testChannel()
	c.ID = "too-soon"
	c.Items = nil
	c.Sources = []ProgramSource{{Kind: "show", ShowKey: showKey}}
	saved, err := s.SaveChannel(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}

	available = true
	s.maybeRecheckAvailability(saved) // well within the floor; must be a no-op
	time.Sleep(20 * time.Millisecond)

	reloaded, err := s.Channel(context.Background(), "too-soon")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DroppedUnavailable != 1 {
		t.Fatalf("recheck must not fire before the floor passes, got dropped=%d", reloaded.DroppedUnavailable)
	}
}

// waitFor polls until cond is true or a short deadline passes, for
// asserting on work maybeRecheckAvailability runs in a background goroutine.
func waitFor(t *testing.T, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}
