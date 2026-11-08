package catalog

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestHydrateAbsoluteEpisodes(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	aired := time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)
	show := core.Title{Key: core.ShowKey(100), Type: core.TypeShow, Title: "Anime", Released: aired}
	gapped := core.Title{Key: core.ShowKey(200), Type: core.TypeShow, Title: "Gapped", Released: aired}
	season := func(sh core.Title, n, count int) core.Title {
		return core.Title{Key: core.SeasonKey(sh.Key.TMDB(), n), Type: core.TypeSeason, ShowKey: sh.Key, Season: n, ChildCount: count, Title: "Season", Released: aired}
	}
	titles := []core.Title{show, gapped,
		// Specials (season 0) never count towards absolute numbers.
		season(show, 0, 30), season(show, 1, 61), season(show, 2, 77), season(show, 3, 39),
		// Season 2 of the gapped show has no known size.
		season(gapped, 1, 12), season(gapped, 2, 0), season(gapped, 3, 12),
	}
	if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	ep := func(sh core.Title, s, e int) core.Title {
		return core.Title{Key: core.EpisodeKey(sh.Key.TMDB(), s, e), Type: core.TypeEpisode, ShowKey: sh.Key, Season: s, Episode: e}
	}
	items := []core.Title{ep(show, 1, 7), ep(show, 3, 5), ep(show, 0, 2), ep(gapped, 3, 1), ep(gapped, 1, 3), ep(show, 3, 5)}
	if err := c.hydrateSlice(ctx, items); err != nil {
		t.Fatal(err)
	}
	for i, want := range []int{7, 61 + 77 + 5, 0, 0, 3, 61 + 77 + 5} {
		if got := items[i].AbsoluteEpisode; got != want {
			t.Errorf("item %d S%dE%d: absolute %d, want %d", i, items[i].Season, items[i].Episode, got, want)
		}
	}
}

func TestHydrateSeasonParentsDoesNotReadSiblingSeasons(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	show := core.Title{Key: core.ShowKey(100), Type: core.TypeShow, Title: "Parent", ContentRating: "PG", Keywords: []string{"inherited"}, AlternativeTitles: []string{"Alias"}, Released: time.Date(2010, 1, 1, 0, 0, 0, 0, time.UTC)}
	titles := []core.Title{show}
	for n := 1; n <= 3; n++ {
		titles = append(titles, core.Title{Key: core.SeasonKey(100, n), Type: core.TypeSeason, ShowKey: show.Key, Season: n, ChildCount: 10, Released: show.Released})
	}
	if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	items := []core.Title{titles[1], titles[2]}
	ctx, stats := store.WithQueryStats(ctx)
	if err := c.hydrateShows(ctx, items); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		if item.ShowTitle != "Parent" || item.ContentRating != "PG" || !slices.Equal(item.Keywords, []string{"inherited"}) || !slices.Equal(item.ShowAlternativeTitles, []string{"Alias"}) {
			t.Fatalf("lost inherited policy or display facts: %+v", item)
		}
	}
	// One distinct parent's identity, keyword and alias. Its siblings are
	// needed for absolute episode numbers, never for a season's metadata.
	if stats.Queries() != 1 || stats.Rows() != 3 {
		t.Fatalf("season parent hydration read %d statements/%d rows, want 1/3", stats.Queries(), stats.Rows())
	}
	t.Logf("season-only parent statement: %d rows", stats.Rows())

	// A mixed page must count only the episode's parent seasons, while
	// retaining the season item's inherited policy and presentation facts.
	other := core.ShowKey(200)
	if err := c.upsertMany(context.Background(), []core.Title{
		{Key: other, Type: core.TypeShow, Title: "Numbered", ContentRating: "TV-14", Released: show.Released},
		{Key: core.SeasonKey(200, 1), Type: core.TypeSeason, ShowKey: other, Season: 1, ChildCount: 7, Released: show.Released},
		{Key: core.SeasonKey(200, 2), Type: core.TypeSeason, ShowKey: other, Season: 2, ChildCount: 9, Released: show.Released},
	}, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	mixed := []core.Title{titles[1], {Key: core.EpisodeKey(200, 2, 3), Type: core.TypeEpisode, ShowKey: other, Season: 2, Episode: 3}}
	mixedCtx, mixedStats := store.WithQueryStats(context.Background())
	if err := c.hydrateShows(mixedCtx, mixed); err != nil {
		t.Fatal(err)
	}
	if mixed[0].ContentRating != "PG" || mixed[0].ShowTitle != "Parent" || !slices.Equal(mixed[0].Keywords, []string{"inherited"}) || !slices.Equal(mixed[0].ShowAlternativeTitles, []string{"Alias"}) || mixed[1].AbsoluteEpisode != 10 || mixed[1].ContentRating != "TV-14" || mixed[1].ShowTitle != "Numbered" {
		t.Fatalf("mixed page lost policy, presentation or numbering: %+v", mixed)
	}
	// Two identities, one keyword, one alias, two numbered-show seasons.
	if mixedStats.Queries() != 1 || mixedStats.Rows() != 6 {
		t.Fatalf("mixed parent hydration read %d statements/%d rows, want 1/6", mixedStats.Queries(), mixedStats.Rows())
	}
	t.Logf("mixed season/episode parent statement: %d rows", mixedStats.Rows())
}
