package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type searchMergeTMDB struct{ TMDB }

func (*searchMergeTMDB) SearchMovies(context.Context, string, int) (*tmdb.Paged[tmdb.Movie], error) {
	return &tmdb.Paged[tmdb.Movie]{TotalPages: 1, Results: []tmdb.Movie{{ID: 999, Title: "Crater", ReleaseDate: "2000-01-01", Popularity: 100}}}, nil
}

// Local and remote candidates must be ranked together before their retained
// titles are fully hydrated, whether the local index is ready or still warming.
// The remote title is the exact name and the local ones only hold the word,
// so the remote one ranks first despite the outside margin.
func TestSearchMergesAndHydratesRankedCandidates(t *testing.T) {
	for _, summary := range []bool{false, true} {
		for _, indexed := range []bool{false, true} {
			t.Run(fmt.Sprintf("indexed=%t/summary=%t", indexed, summary), func(t *testing.T) {
				c := newChunkTestCatalog(t)
				ctx := context.Background()
				if summary {
					ctx = core.WithSearchSummaryFacts(ctx)
				}
				now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				c.SetNow(func() time.Time { return now })
				c.SetTMDB(&searchMergeTMDB{})
				var titles []core.Title
				for i := 1; i <= 15; i++ {
					titles = append(titles, core.Title{Key: core.MovieKey(int64(i)), Type: core.TypeMovie, Title: fmt.Sprintf("Feature %d Crater", i), Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Popularity: float64(i), Genres: []string{"Drama"}})
				}
				if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true}); err != nil {
					t.Fatal(err)
				}
				if indexed {
					if c.searchIndex(ctx, true) == nil {
						t.Fatal("index did not build")
					}
				} else {
					c.searchIx.next = now.Add(time.Hour)
				}
				got, err := c.Search(ctx, "Crater", []core.MediaType{core.TypeMovie}, 10)
				if err != nil {
					t.Fatal(err)
				}
				if len(got) != 10 || got[0].Key != core.MovieKey(999) {
					t.Fatalf("ranked results: %v", got)
				}
				for _, title := range got[1:] {
					if len(title.Genres) != 1 || title.Genres[0] != "Drama" {
						t.Fatalf("local result lost facts: %+v", title)
					}
				}
			})
		}
	}
}
