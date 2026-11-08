package catalog

import (
	"context"
	"math/rand/v2"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A page that seeks past the previous page's last title is the page an
// offset reads, ties of the sort value included, for every seekable sort.
func TestSeekPageMatchesOffsetPage(t *testing.T) {
	c := largeCatalogue(t, 0)
	rng := rand.New(rand.NewPCG(3, 4))
	released := time.Now().Add(-48 * time.Hour).Unix()
	for i := 1; i <= 700; i++ {
		// Few distinct values per column, so page ends fall inside ties.
		title := []string{"alpha", "bravo", "charlie", "delta"}[rng.IntN(4)]
		if _, err := c.db.Exec(`INSERT INTO catalog_titles(key,type,tmdb_id,title,sort_title,year,released_ts,popularity,rating,runtime_sec,browsable,adult) VALUES(?,?,?,?,?,?,?,?,?,?,1,0)`,
			int64(core.MovieKey(int64(i))), "movie", i, title, title, 1990+rng.IntN(5), released-int64(rng.IntN(6))*86400, float64(rng.IntN(7)), float64(rng.IntN(5)), 3600*rng.IntN(3)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	keys := func(p core.Page) []core.RatingKey {
		out := make([]core.RatingKey, len(p.Items))
		for i, it := range p.Items {
			out[i] = it.Key
		}
		return out
	}
	for _, sort := range []core.SortOrder{core.SortTitle, core.SortPopularity, core.SortRating, core.SortYear, core.SortReleased, core.SortDuration, core.SortAdded} {
		for _, desc := range []bool{false, true} {
			q := core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Sort: sort, Desc: desc, Limit: 50}
			var after core.RatingKey
			for offset := 0; offset < 760; offset += 50 {
				q.Offset, q.AfterKey = offset, 0
				want, err := c.Browse(ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				if offset > 0 {
					q.Offset, q.AfterKey = 0, after
					got, err := c.Browse(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if !slices.Equal(keys(got), keys(want)) || got.Total != want.Total {
						t.Fatalf("%s desc=%v offset %d: seek %v (total %d), offset %v (total %d)", sort, desc, offset, keys(got), got.Total, keys(want), want.Total)
					}
				}
				if len(want.Items) > 0 {
					after = want.Items[len(want.Items)-1].Key
				}
			}
		}
	}
}
