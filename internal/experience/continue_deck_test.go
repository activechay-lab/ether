package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestContinueDeckHydratesAndScopesState(t *testing.T) {
	a, b := core.ShowKey(1), core.ShowKey(2)
	first := core.Title{Key: core.EpisodeKey(1, 1, 1), Type: core.TypeEpisode, Season: 1, Episode: 1}
	next := core.Title{Key: core.EpisodeKey(1, 1, 2), Type: core.TypeEpisode, Season: 1, Episode: 2}
	foreign := core.EpisodeKey(2, 1, 1)
	loads := 0
	d := ContinueDeck{
		Load: func(context.Context, []core.RatingKey) (map[core.RatingKey][]core.Title, map[core.RatingKey]core.WatchStatus) {
			return map[core.RatingKey][]core.Title{a: {first, next}, b: {{Key: foreign}}}, map[core.RatingKey]core.WatchStatus{first.Key: {}, foreign: {UpdatedAt: time.Now()}}
		},
		Order: func(_ context.Context, show core.RatingKey, eps []core.Title) ([]core.Title, error) {
			if show == b {
				return nil, core.ErrNotFound
			}
			return eps, nil
		},
		Hydrate: func(_ context.Context, keys []core.RatingKey) []core.Title {
			loads++
			var out []core.Title
			for _, key := range keys {
				ep := first
				if key == next.Key {
					ep = next
				}
				ep.Title = "hydrated"
				out = append(out, ep)
			}
			return out
		},
		Unavailable: func(_ context.Context, title *core.Title) bool {
			if title.Title != "hydrated" {
				t.Fatal("availability received lightweight metadata")
			}
			return title.Key == first.Key
		},
		Fallback: func(_ context.Context, show core.RatingKey, _ []core.Title, prepared bool) (*core.Title, map[core.RatingKey]core.WatchStatus) {
			if show != b || prepared {
				t.Fatal("unexpected fallback")
			}
			return nil, nil
		},
	}
	d.Prepare(context.Background(), []core.RatingKey{a, b})
	got, states := d.Select(context.Background(), a)
	if got == nil || got.Key != next.Key || got.Title != "hydrated" || loads != 2 {
		t.Fatalf("selection=%+v loads=%d", got, loads)
	}
	if _, ok := states[foreign]; ok {
		t.Fatal("batch neighbor state leaked into recency")
	}
	if got, _ := d.Select(context.Background(), b); got != nil {
		t.Fatal("failed order did not fall back")
	}
}

type hiddenContinueState struct {
	continueState
	fail      bool
	requested []core.RatingKey
}

func (s *hiddenContinueState) ContinueHidden(ctx context.Context, uid int64, keys []core.RatingKey) (map[core.RatingKey]bool, error) {
	s.requested = keys
	if s.fail {
		return nil, context.Canceled
	}
	return map[core.RatingKey]bool{core.ShowKey(1): true}, nil
}

func TestContinuePersistedRemovalPrecedesPersonalFilter(t *testing.T) {
	ep := core.Title{Key: core.EpisodeKey(1, 1, 2), Type: core.TypeEpisode}
	movie := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie}
	state := &hiddenContinueState{continueState: continueState{statuses: []core.WatchStatus{{Key: ep.Key}, {Key: movie.Key}}}}
	c := Continue{State: state, Now: time.Now, Unrestricted: func(*core.User) bool { return true }, GetTitles: func(context.Context, []core.RatingKey) []core.Title { return []core.Title{ep, movie} }, SectionAllowed: func(*core.User, core.MediaType) bool { return true }, Filter: func(_ context.Context, _ *core.User, items []core.Title) []core.Title {
		if state.fail {
			t.Fatal("failed hidden read reached personal filter")
		}
		for _, item := range items {
			if item.Key == ep.Key {
				t.Fatal("removed episode reached personal filter")
			}
		}
		return items
	}}
	c.Filter = ContinueFilter(c.State, c.Filter)
	got, _ := c.Row(context.Background(), &core.User{ID: 7}, "", 10)
	if len(got) != 1 || got[0].Key != movie.Key {
		t.Fatalf("items=%+v", got)
	}
	state.fail = true
	if got, _ := c.Row(context.Background(), &core.User{ID: 7}, "", 10); len(got) != 0 {
		t.Fatal("failed hidden read exposed items")
	}
}
