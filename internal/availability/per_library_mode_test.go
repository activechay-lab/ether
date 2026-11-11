package availability

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestIndependentMovieAndShowModes(t *testing.T) {
	x := setup(t)
	now := x.now
	movie := core.Title{Key: core.MovieKey(501), Type: core.TypeMovie, Title: "New Movie", Released: now.Add(-12 * time.Hour)}
	show := core.Title{Key: core.ShowKey(601), Type: core.TypeShow, Title: "New Show", Released: now.Add(-12 * time.Hour), LastAirDate: now.Add(-12 * time.Hour)}
	ep := core.Title{Key: core.EpisodeKey(601, 1, 1), Type: core.TypeEpisode, Title: "Pilot", Released: now.Add(-12 * time.Hour)}
	x.cat.titles[movie.Key] = movie
	x.cat.titles[show.Key] = show
	x.cat.titles[ep.Key] = ep

	// Movies on digital release (no evidence yet -> hidden), TV Shows immediate (shown).
	x.c.Availability.MovieMode = config.ModeDigital
	x.c.Availability.ShowMode = config.ModeImmediate
	x.c.Availability.ApplyMode()
	got := x.s.FilterDiscovery(t.Context(), []core.Title{movie, show, ep})
	if len(got) != 2 || got[0].Key != show.Key || got[1].Key != ep.Key {
		t.Fatalf("digital movie + immediate show got %+v", got)
	}

	// Movies immediate (shown), TV Shows playable (no evidence yet -> hidden).
	x.c.Availability.MovieMode = config.ModeImmediate
	x.c.Availability.ShowMode = config.ModePlayable
	x.c.Availability.ApplyMode()
	got = x.s.FilterDiscovery(t.Context(), []core.Title{movie, show, ep})
	if len(got) != 1 || got[0].Key != movie.Key {
		t.Fatalf("immediate movie + playable show got %+v", got)
	}
}
