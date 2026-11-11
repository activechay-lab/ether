package livetv

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// One schedule: a title known to be unplayable keeps its slot in the guide
// (it is replaced on air for that slot only), so later programmes never move.
func TestGuideKeepsUnplayableTitlesInTheirSlots(t *testing.T) {
	now := time.Unix(7, 0).UTC()
	first := core.MovieKey(1)
	second := core.MovieKey(2)
	s := testService(t, Options{
		Now: func() time.Time { return now },
		Availability: availFunc(func(key core.RatingKey) (bool, bool) {
			if key == first {
				return true, false
			}
			return false, false
		}),
	})
	c := testChannel()
	c.Items = []Item{
		{Key: first, Title: "Cats in the Museum 2", DurationMS: 12000},
		{Key: second, Title: "Second", DurationMS: 20000},
	}
	if _, err := s.SaveChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	got, err := s.programmes(context.Background(), c, now, now.Add(time.Second))
	if err != nil || len(got) != 1 || got[0].Item.Key != first {
		t.Fatalf("availability reshaped the guide: %+v %v", got, err)
	}
	unknown := testService(t, Options{Now: func() time.Time { return now }})
	plain, err := unknown.programmes(context.Background(), c, now, now.Add(time.Second))
	if err != nil || len(plain) != 1 || plain[0].Item.Key != first {
		t.Fatalf("unknown availability changed the guide: %+v %v", plain, err)
	}
}

type availFunc func(core.RatingKey) (bool, bool)

func (f availFunc) Available(_ context.Context, key core.RatingKey) (bool, bool) { return f(key) }

// Prewarming a future slot must never offer an already-aired (or soon to be
// aired-out) programme as a fallback candidate: a failed Prepare on the real
// target would otherwise replay stale content instead of moving forward.
func TestVirtualCandidatesNeverFallBackBeforeTheTargetSlot(t *testing.T) {
	now := time.Unix(20, 0).UTC() // mid-way through "Second"'s live airing
	s := testService(t, Options{Now: func() time.Time { return now }})
	c := testChannel() // First: [0,12) Second: [12,32) repeating every 32s
	if _, err := s.SaveChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}

	// The slot being planned/prewarmed is the *next* cycle's "First",
	// starting at 32s - after "now", simulating a lookahead prepare.
	future, ok, err := s.programmeAt(context.Background(), c, time.Unix(32, 0).UTC())
	if err != nil || !ok || future.Item.Title != "First" {
		t.Fatalf("expected next cycle's First at t=32: %+v %v %v", future, ok, err)
	}

	for _, cand := range s.virtualCandidates(c, future) {
		if cand.Start.Before(future.Start) {
			t.Fatalf("candidate %q starts at %v, before the planned slot %v: this replays stale content on fallback", cand.Item.Title, cand.Start, future.Start)
		}
	}
}
