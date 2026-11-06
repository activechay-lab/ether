package app

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
)

func TestQualityExplainHandlerUsesTitleMedia(t *testing.T) {
	ctx := context.Background()
	a := setupTestApp(t, true)
	if err := a.users.SyncFriends(ctx, []plextv.Friend{{PlexID: 78, Username: "sam", AllLibs: true}}); err != nil {
		t.Fatal(err)
	}
	users, err := a.users.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var viewer int64
	for _, u := range users {
		if u.PlexID == 78 {
			viewer = u.ID
		}
	}
	if viewer == 0 {
		t.Fatal("viewer missing")
	}
	if err := a.cfg.Update(func(c *config.Config) error {
		c.Quality = config.Quality{
			Profiles: []core.QualityProfile{
				{Name: "Movies", MediaType: core.MediaMovie, MaxHeight: 2160, AllowHDR: true, AllowDolbyVision: true},
				{Name: "Shows", MediaType: core.MediaTV, MaxHeight: 1080},
			},
			DefaultMovie: "Movies",
			DefaultTV:    "Shows",
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	movie := core.Title{Key: core.MovieKey(603), Type: core.TypeMovie, IDs: core.IDs{TMDB: 603}, Title: "The Matrix", Released: time.Date(1999, 3, 31, 0, 0, 0, 0, time.UTC)}
	episode := core.Title{
		Key: core.EpisodeKey(1399, 1, 1), Type: core.TypeEpisode,
		ShowKey: core.ShowKey(1399), ShowIDs: core.IDs{TMDB: 1399},
		ShowTitle: "Severance", Title: "Good News About Hell", Season: 1, Episode: 1,
		Released: time.Date(2022, 2, 18, 0, 0, 0, 0, time.UTC),
	}
	if err := a.catalog.UpsertTitle(ctx, movie, true); err != nil {
		t.Fatal(err)
	}
	if err := a.catalog.UpsertTitle(ctx, episode, false); err != nil {
		t.Fatal(err)
	}
	viewerID := strconv.FormatInt(viewer, 10)

	code, got := postQualityExplain(t, a, `{"userId":`+viewerID+`,"clientId":"plex-apple-tv","titleKey":"`+movie.Key.String()+`"}`)
	if code != 200 || got.Profile != "Movies" {
		t.Fatalf("movie title: %d %+v", code, got)
	}
	code, got = postQualityExplain(t, a, `{"userId":`+viewerID+`,"clientId":"plex-apple-tv","titleKey":"`+episode.Key.String()+`"}`)
	if code != 200 || got.Profile != "Shows" {
		t.Fatalf("episode title: %d %+v", code, got)
	}
	code, got = postQualityExplain(t, a, `{"userId":`+viewerID+`,"clientId":"plex-apple-tv","mediaType":"movie"}`)
	if code != 200 || got.Profile != "Movies" {
		t.Fatalf("explicit movie: %d %+v", code, got)
	}
	code, got = postQualityExplain(t, a, `{"userId":`+viewerID+`,"clientId":"plex-apple-tv","mediaType":"tv"}`)
	if code != 200 || got.Profile != "Shows" {
		t.Fatalf("explicit tv: %d %+v", code, got)
	}
	if code, _ := postQualityExplain(t, a, `{"userId":`+viewerID+`,"clientId":"plex-apple-tv","mediaType":"shows"}`); code != 400 {
		t.Fatalf("invalid media type: %d", code)
	}
}
