package livetv

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestGuideSkipsTitlesKnownToBeUnplayable(t *testing.T) {
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
	if err != nil || len(got) != 1 || got[0].Item.Key != second || got[0].Item.Title != "Second" {
		t.Fatalf("guide kept an unplayable title: %+v %v", got, err)
	}
	unknown := testService(t, Options{Now: func() time.Time { return now }})
	plain, err := unknown.programmes(context.Background(), c, now, now.Add(time.Second))
	if err != nil || len(plain) != 1 || plain[0].Item.Key != first {
		t.Fatalf("unknown availability changed the guide: %+v %v", plain, err)
	}
}

// A tune after the lazy guide snapshot was evicted must still schedule
// playout from the gated channel the guide was served from, never the raw
// channel: the ungated first title would shift the second programme's span
// so the viewer joins a different programme than the guide lists.
func TestPlannedChannelAfterSnapshotEvictionMatchesGuide(t *testing.T) {
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
		{Key: first, Title: "Unplayable", DurationMS: 12000},
		{Key: second, Title: "Second", DurationMS: 20000},
	}
	if _, err := s.SaveChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	guide, err := s.programmes(context.Background(), c, now, now.Add(time.Second))
	if err != nil || len(guide) != 1 || guide[0].Item.Key != second {
		t.Fatalf("guide: %+v %v", guide, err)
	}
	s.virtualGuide.mu.Lock()
	s.virtualGuide.entries = nil
	s.virtualGuide.mu.Unlock()
	planned := s.plannedChannel(context.Background(), c)
	playout := s.broadcastSchedule(context.Background(), planned, now, now.Add(time.Second), true)
	if len(playout) != 1 || playout[0].Item.Key != guide[0].Item.Key || !playout[0].Start.Equal(guide[0].Start) || !playout[0].End.Equal(guide[0].End) {
		t.Fatalf("playout after eviction drifted from guide: playout=%+v guide=%+v", playout, guide)
	}
}

type availFunc func(core.RatingKey) (bool, bool)

func (f availFunc) Available(_ context.Context, key core.RatingKey) (bool, bool) { return f(key) }
