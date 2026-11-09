package catalog

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Year-merged and genre-walk pages are the pages the one sorted query
// returned: (sort_title, key) order, ties by key, every offset.
func TestBrowseYearAndGenrePagesKeepTitleOrder(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	genres := []string{"Drama", "Comedy", "Western"}
	var all []core.Title
	for i := range 90 {
		year := 1985 + i%17
		ti := core.Title{
			Key: core.MovieKey(int64(9000 + i)), Type: core.TypeMovie, IDs: core.IDs{TMDB: int64(9000 + i)},
			// Every fourth title shares a sort title, so pages split ties.
			Title: fmt.Sprintf("T%02d", (i*37)%60), SortTitle: fmt.Sprintf("t%02d", (i*37)%60),
			Year: year, Released: time.Date(year, 3, 1, 0, 0, 0, 0, time.UTC),
			Genres: []string{genres[i%3]}, Popularity: float64(i),
		}
		if i%4 == 0 {
			ti.SortTitle, ti.Title = "same", "Same"
		}
		if err := c.UpsertTitle(ctx, ti, true); err != nil {
			t.Fatal(err)
		}
		all = append(all, ti)
	}
	cases := []struct {
		name string
		f    core.Filter
		keep func(core.Title) bool
	}{
		{"one year", core.Filter{YearFrom: 1990, YearTo: 1990}, func(x core.Title) bool { return x.Year == 1990 }},
		{"decade", core.Filter{YearFrom: 1990, YearTo: 1999}, func(x core.Title) bool { return x.Year >= 1990 && x.Year <= 1999 }},
		{"decade less a year", core.Filter{YearFrom: 1990, YearTo: 1999, ExcludeYears: []int{1993}}, func(x core.Title) bool { return x.Year >= 1990 && x.Year <= 1999 && x.Year != 1993 }},
		{"years", core.Filter{Years: []int{1986, 1999, 2001}}, func(x core.Title) bool { return x.Year == 1986 || x.Year == 1999 || x.Year == 2001 }},
		{"genre", core.Filter{Genres: []string{"Drama"}}, func(x core.Title) bool { return x.Genres[0] == "Drama" }},
		{"genres", core.Filter{Genres: []string{"Drama", "Western"}}, func(x core.Title) bool { return x.Genres[0] != "Comedy" }},
		{"genre in a decade", core.Filter{Genres: []string{"Western"}, YearFrom: 1990, YearTo: 1999}, func(x core.Title) bool {
			return x.Genres[0] == "Western" && x.Year >= 1990 && x.Year <= 1999
		}},
	}
	for _, tc := range cases {
		for _, desc := range []bool{false, true} {
			var want []core.RatingKey
			for _, x := range all {
				if tc.keep(x) {
					want = append(want, x.Key)
				}
			}
			sort := map[core.RatingKey]string{}
			for _, x := range all {
				sort[x.Key] = x.SortTitle
			}
			slices.SortFunc(want, func(a, b core.RatingKey) int {
				if c := cmp.Compare(sort[a], sort[b]); c != 0 {
					if desc {
						return -c
					}
					return c
				}
				return cmp.Compare(a, b)
			})
			f := tc.f
			f.Type = core.TypeMovie
			var got []core.RatingKey
			for offset := 0; offset <= len(want); offset += 4 {
				page, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Sort: core.SortTitle, Desc: desc, Offset: offset, Limit: 4})
				if err != nil {
					t.Fatal(err)
				}
				if page.Total != len(want) {
					t.Fatalf("%s desc=%v: total %d, want %d", tc.name, desc, page.Total, len(want))
				}
				for _, it := range page.Items {
					got = append(got, it.Key)
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s desc=%v:\n got %v\nwant %v", tc.name, desc, got, want)
			}
			// Paging on from each page's last title, as Jellyfin's
			// remembered page ends do.
			var seek []core.RatingKey
			for after := core.RatingKey(0); ; {
				page, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Sort: core.SortTitle, Desc: desc, AfterKey: after, Limit: 4, NoTotal: true})
				if err != nil {
					t.Fatal(err)
				}
				if len(page.Items) == 0 {
					break
				}
				for _, it := range page.Items {
					seek = append(seek, it.Key)
				}
				after = page.Items[len(page.Items)-1].Key
			}
			if !slices.Equal(seek, want) {
				t.Errorf("%s desc=%v seek:\n got %v\nwant %v", tc.name, desc, seek, want)
			}
		}
	}
}

// A viewer's filter carries empty share lists; it is still a walk. A
// person is not.
func TestWalkableFilter(t *testing.T) {
	viewer := core.Filter{Type: core.TypeMovie, Genres: []string{"Drama"}, CatalogViewerID: 7, ExcludeKeys: []core.RatingKey{}, IncludeCollectionNames: []string{}, ExcludeCollectionNames: []string{}, ExcludeContentRatings: []string{}}
	if !walkableFilter(viewer) {
		t.Fatal("a viewer's genre filter is not a walk")
	}
	viewer.Person = 31
	if walkableFilter(viewer) {
		t.Fatal("a person filter is a walk")
	}
}
