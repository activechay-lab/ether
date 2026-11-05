package plex

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

func TestFirstBrowseWarmKeyPrefersContinueWatching(t *testing.T) {
	movie := core.MovieKey(550)
	other := core.MovieKey(603)
	rows := []model.Hub{
		{HubIdentifier: "movie.recommended", Metadata: []model.Metadata{{RatingKey: other.String(), Type: "movie"}}},
		{HubIdentifier: "home.continue", Context: "hub.home.continue", Metadata: []model.Metadata{{RatingKey: movie.String(), Type: "movie"}}},
	}
	got, ok := firstBrowseWarmKey(rows)
	if !ok || got != movie {
		t.Fatalf("got %v ok=%v, want continue-watching %v", got, ok, movie)
	}
}

// Only the viewer's own progress is warmed: a Home of recommendations and
// trending rows warms nothing, and shows in a progress row are skipped for
// their episodes.
func TestFirstBrowseWarmKeyWarmsOnlyOwnProgress(t *testing.T) {
	show := core.ShowKey(1396)
	ep := core.EpisodeKey(1396, 1, 1)
	recommended := []model.Hub{
		{HubIdentifier: "tv.recommended", Metadata: []model.Metadata{{RatingKey: ep.String(), Type: "episode"}}},
		{HubIdentifier: "home.trending", Metadata: []model.Metadata{{RatingKey: core.MovieKey(603).String(), Type: "movie"}}},
	}
	if got, ok := firstBrowseWarmKey(recommended); ok {
		t.Fatalf("warmed recommendation %v", got)
	}
	rows := append(recommended, model.Hub{
		HubIdentifier: "tv.inprogress.2", Context: "hub.tv.inprogress",
		Metadata: []model.Metadata{
			{RatingKey: show.String(), Type: "show"},
			{RatingKey: ep.String(), Type: "episode"},
		},
	})
	got, ok := firstBrowseWarmKey(rows)
	if !ok || got != ep {
		t.Fatalf("got %v ok=%v, want episode %v", got, ok, ep)
	}
}
