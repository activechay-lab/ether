package catalog_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Playable evidence must survive metadata refresh and must not delay an
// already earlier metadata date. The season must open with its premiere.
func TestPlayableEpisodeEarliestDate(t *testing.T) {
	ctx := context.Background()
	now := day(13).Add(12 * time.Hour)
	c, err := catalog.New(store.OpenTest(t), nil, config.Catalog{}, slog.New(slog.DiscardHandler), catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	show := core.ShowKey(259909)
	season := core.Title{Key: core.SeasonKey(show.TMDB(), 2), Type: core.TypeSeason, ShowKey: show, Season: 2, Released: day(15), ChildCount: 3}
	ep := core.Title{Key: core.EpisodeKey(show.TMDB(), 2, 3), Type: core.TypeEpisode, ShowKey: show, Season: 2, Episode: 3, Released: day(15)}
	for _, row := range []core.Title{season, ep} {
		if err := c.UpsertTitle(ctx, row, true); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.RecordPlayableEpisode(ctx, ep.Key, now); err != nil {
		t.Fatal(err)
	}
	for _, row := range []core.Title{season, ep} {
		if err := c.UpsertTitle(ctx, row, true); err != nil {
			t.Fatal(err)
		}
		got, err := c.CachedTitle(ctx, row.Key)
		if err != nil || !got.IsReleased(now) || !got.Released.Equal(now) {
			t.Fatalf("refresh lost evidence: %+v %v", got, err)
		}
	}
	ep.Released = day(12)
	if err := c.UpsertTitle(ctx, ep, true); err != nil {
		t.Fatal(err)
	}
	if err := c.RecordPlayableEpisode(ctx, ep.Key, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := c.CachedTitle(ctx, ep.Key)
	if err != nil || !got.Released.Equal(day(12)) {
		t.Fatalf("earlier metadata lost: %+v %v", got, err)
	}
}
