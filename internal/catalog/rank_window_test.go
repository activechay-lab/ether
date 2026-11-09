package catalog

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// rankView is what a taste ranking reads of a title (personalize's
// Taste.Fit and quality): its row, genres, keywords, every director and
// the first cast, writers and crew in credit order.
func rankView(t core.Title) core.Title {
	cut := func(people []core.Person, n int) []core.Person { return people[:min(len(people), n)] }
	t.Cast, t.Writers, t.Crew = cut(t.Cast, core.RankCast), cut(t.Writers, core.RankWriters), cut(t.Crew, core.RankCrew)
	t.Studios, t.Countries, t.AlternativeTitles = nil, nil, nil
	t.RemoteTrailerName, t.RemoteTrailerURL = "", ""
	return t
}

// A browse read as a ranking window (core.WithRankingCandidates) returns
// the full browse's titles in its order, each with everything a ranking
// reads, credits tied on order included, and fewer credits than in full;
// RankingDetails then reads its partial titles back to the full browse's.
// A title the caller judges in full comes back in full, from the page
// cache as from the database.
func TestRankingWindowMatchesFullBrowse(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	const person = 7
	var titles []core.Title
	for i := int64(1); i <= 24; i++ {
		typ, key := core.TypeMovie, core.MovieKey(i)
		if i%5 == 0 {
			typ, key = core.TypeShow, core.ShowKey(i)
		}
		title := core.Title{Key: key, Type: typ, IDs: core.IDs{TMDB: i}, Title: fmt.Sprintf("Work %d", i), Released: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), Year: 2001,
			Language: "en", Popularity: float64(100 - i), Rating: 7, VoteCount: 900, CollectionID: i % 3, CollectionName: "Saga",
			Genres: []string{"Drama", "Thriller"}, Keywords: []string{"heist", fmt.Sprintf("k%d", i)}, Studios: []string{"Studio"}, Countries: []string{"US"},
			AlternativeTitles: []string{fmt.Sprintf("Werk %d", i)}, RemoteTrailerName: "Trailer", RemoteTrailerURL: "https://example.invalid/t"}
		title.Cast = append(title.Cast, core.Person{TMDB: person, Name: "The Lead", Role: "Hero"})
		for n := int64(1); n <= 9; n++ {
			title.Cast = append(title.Cast, core.Person{TMDB: 100 + n, Name: fmt.Sprintf("Actor %d", n), Role: fmt.Sprintf("Role %d", n)})
			title.Crew = append(title.Crew, core.Person{TMDB: 200 + n, Name: fmt.Sprintf("Crew %d", n), Role: []string{"Composer", "Editor", "Cinematographer"}[n%3]})
		}
		for n := int64(1); n <= 5; n++ {
			title.Writers = append(title.Writers, core.Person{TMDB: 300 + n, Name: fmt.Sprintf("Writer %d", n), Role: "Writer"})
		}
		title.Directors = []core.Person{{TMDB: 400 + i, Name: "Director", Role: "Director"}, {TMDB: 499, Name: "Co-Director", Role: "Director"}}
		titles = append(titles, title)
	}
	if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true, detail: true}); err != nil {
		t.Fatal(err)
	}
	// A person's filmography sync writes its own orders: ties and gaps.
	for _, stmt := range []string{
		`UPDATE catalog_credits SET ord=ord/2 WHERE kind='cast' AND key%2=0`,
		`UPDATE catalog_credits SET ord=0 WHERE kind='crew' AND key%3=0`,
		`UPDATE catalog_credits SET ord=ord*3 WHERE kind='writer'`,
		`UPDATE catalog_credits SET ord=4 WHERE kind='cast' AND ord BETWEEN 3 AND 6 AND key%2=1`,
	} {
		if _, err := c.db.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	reset := func() {
		c.cacheMu.Lock()
		c.titleCache, c.browseCache = nil, map[string]cachedBrowse{}
		c.cacheMu.Unlock()
	}
	for _, q := range []core.BrowseQuery{
		{Filter: core.Filter{Person: person}, Sort: core.SortPopularity, Desc: true, Limit: 50},
		{Filter: core.Filter{Person: person, Type: core.TypeMovie}, Sort: core.SortPopularity, Desc: true, Limit: 50},
	} {
		reset()
		full, err := c.Browse(ctx, q)
		if err != nil || len(full.Items) < 10 {
			t.Fatalf("full browse %+v: %d titles %v", q.Filter, len(full.Items), err)
		}
		detailKey := full.Items[1].Key
		for _, cached := range []bool{false, true} {
			if !cached {
				reset()
			}
			cand := &core.SearchCandidates{Facts: core.FactGenres | core.FactKeywords, Detail: func(t core.Title) bool { return t.Key == detailKey }}
			window := core.WithRankingCandidates(ctx, cand)
			got, err := c.Browse(window, q)
			if err != nil || len(got.Items) != len(full.Items) || got.Total != full.Total {
				t.Fatalf("window browse cached=%t: %d of %d titles %v", cached, len(got.Items), len(full.Items), err)
			}
			for i, title := range got.Items {
				want := full.Items[i]
				if title.Key != want.Key {
					t.Fatalf("cached=%t title %d is %v, full browse has %v", cached, i, title.Key, want.Key)
				}
				if title.Key == detailKey {
					if cand.Partial(title.Key) || !reflect.DeepEqual(title, want) {
						t.Fatalf("cached=%t: judged title not read in full: %+v", cached, title)
					}
					continue
				}
				if !cand.Partial(title.Key) || len(title.Cast) >= len(want.Cast) || len(title.AlternativeTitles) != 0 {
					t.Fatalf("cached=%t: %v not read as a window title: partial=%t cast %d of %d", cached, title.Key, cand.Partial(title.Key), len(title.Cast), len(want.Cast))
				}
				if !reflect.DeepEqual(rankView(title), rankView(want)) {
					t.Fatalf("cached=%t: %v ranks on\n%+v\nnot\n%+v", cached, title.Key, rankView(title), rankView(want))
				}
			}
			details, err := c.RankingDetails(window, got.Items)
			if err != nil || !reflect.DeepEqual(details, full.Items) {
				t.Fatalf("cached=%t: details differ from the full browse: %v", cached, err)
			}
		}
		// The full browse still reads full titles: the window's page
		// cache entry is its own.
		again, err := c.Browse(ctx, q)
		if err != nil || !reflect.DeepEqual(again.Items, full.Items) {
			t.Fatalf("full browse after the window differs: %v", err)
		}
	}
}
