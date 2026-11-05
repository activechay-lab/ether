package personalize

import (
	"context"
	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestPersonalPicksRespectViewerSafety(t *testing.T) {
	titles := []core.Title{
		{Key: core.MovieKey(901), Type: core.TypeMovie, Title: "Safe", ContentRating: "G", Genres: []string{"Comedy"}},
		{Key: core.MovieKey(902), Type: core.TypeMovie, Title: "Above cap", ContentRating: "R"},
		{Key: core.MovieKey(903), Type: core.TypeMovie, Title: "Blocked genre", ContentRating: "G", Genres: []string{"Horror"}},
		{Key: core.MovieKey(904), Type: core.TypeMovie, Title: "Not interested", ContentRating: "G"},
	}
	h := newHarness(t, func(o *Options) {
		o.PersonalWatchlist = func(context.Context, *core.User) ([]core.Title, error) { return titles, nil }
		o.NotInterested = func(context.Context, *core.User) ([]NotInterestedSignal, error) {
			return []NotInterestedSignal{{Key: titles[3].Key}}, nil
		}
		o.Allowed = func(_ *core.User, t *core.Title) bool {
			for _, g := range t.Genres {
				if g == "Horror" {
					return false
				}
			}
			return true
		}
	})
	u := &core.User{ID: 42, MaxContentRating: "PG"}
	picks, err := h.e.Picks(context.Background(), u, map[string]float64{"watchlist": 100}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(picks) != 1 || picks[0].Title.Key != titles[0].Key {
		t.Fatalf("unsafe picks: %#v", picks)
	}
	for _, p := range picks {
		if !contentrating.Allowed(p.Title.ContentRating, u.MaxContentRating) {
			t.Fatal("rating leak")
		}
		for _, g := range p.Title.Genres {
			if g == "Horror" {
				t.Fatal("genre leak")
			}
		}
	}
}
