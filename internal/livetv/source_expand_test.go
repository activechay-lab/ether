package livetv

import (
	"context"
	"testing"

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

// A dropped episode must not stay dropped forever: once it finishes caching,
// the periodic availability recheck should pick it back up without anyone
// touching the channel's source selection.
func TestRecheckDroppedChannelsPicksUpRecoveredTitle(t *testing.T) {
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

	// The episode finishes caching. Nobody touches the channel - the
	// periodic recheck should find and fix it on its own.
	available = true
	s.recheckDroppedChannels(context.Background())

	reloaded, err := s.Channel(context.Background(), "binge")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Items) != 4 || reloaded.DroppedUnavailable != 0 {
		t.Fatalf("expected the recovered episode back after a recheck, got %d items dropped=%d: %+v", len(reloaded.Items), reloaded.DroppedUnavailable, reloaded.Items)
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

// A showcase channel already has its own refresh loop keeping it current
// (showcaseLoop); the generic recheck must not duplicate that work.
func TestRecheckDroppedChannelsSkipsShowcaseChannels(t *testing.T) {
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
	s.recheckDroppedChannels(context.Background())

	reloaded, err := s.Channel(context.Background(), "showcase-chan")
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Items) != 3 || reloaded.DroppedUnavailable != 1 {
		t.Fatalf("recheck must skip a channel already covered by the showcase refresh loop, got %d items dropped=%d", len(reloaded.Items), reloaded.DroppedUnavailable)
	}
}
