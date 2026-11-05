package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestExplainQualityScopesByTitleAndMedia(t *testing.T) {
	q := config.Quality{
		Profiles: []core.QualityProfile{
			{Name: "Movies", MediaType: core.MediaMovie, MaxHeight: 2160, AllowHDR: true, AllowDolbyVision: true},
			{Name: "Shows", MediaType: core.MediaTV, MaxHeight: 1080},
		},
		DefaultMovie: "Movies",
		DefaultTV:    "Shows",
	}
	movie := &core.Title{Type: core.TypeMovie, Key: core.MovieKey(603)}
	episode := &core.Title{Type: core.TypeEpisode, Key: core.EpisodeKey(1399, 1, 1)}

	tests := []struct {
		name  string
		title *core.Title
		media string
		want  string
	}{
		{name: "movie title", title: movie, want: "Movies"},
		{name: "episode title", title: episode, want: "Shows"},
		{name: "explicit movie", media: core.MediaMovie, want: "Movies"},
		{name: "explicit tv", media: core.MediaTV, want: "Shows"},
		{name: "title wins over explicit media", title: movie, media: core.MediaTV, want: "Movies"},
		{name: "no hint falls to first compatible", want: "Movies"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := QualityQuestion{Quality: q, Caps: appleTV(false), Title: tc.title, MediaType: tc.media}
			a := ExplainQuality(in)
			assertMatchesPlayback(t, in, a)
			if a.MediaType != questionMedia(in) {
				t.Fatalf("mediaType = %q, want %q", a.MediaType, questionMedia(in))
			}
			if a.Profile != tc.want {
				t.Fatalf("profile = %q, want %q: %+v", a.Profile, tc.want, a)
			}
		})
	}
}
