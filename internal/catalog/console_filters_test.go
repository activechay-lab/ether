package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestConsoleNumericAndComposerFilters(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET vote_count=tmdb_id*100,popularity=tmdb_id*10,status='Returning Series',child_count=tmdb_id,leaf_count=tmdb_id*10`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_credits(key,person_tmdb,name,role,kind) VALUES(?,42,'Composer Name','Composer','crew')`, int64(core.MovieKey(2))); err != nil {
		t.Fatal(err)
	}
	for _, f := range []core.Filter{
		{MinVotes: 150, MaxVotes: 250}, {MinPopularity: 15, MaxPopularity: 25},
		{MinSeasons: 2, MaxSeasons: 2}, {MinEpisodes: 20, MaxEpisodes: 20},
		{Credits: []core.CreditFilter{{People: []int64{42}, Kind: "composer"}}},
	} {
		f.Type = core.TypeMovie
		f.LocalOnly = true
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 || page.Items[0].Key != core.MovieKey(2) {
			t.Fatalf("filter %+v: page %+v", f, page)
		}
	}
}

func TestConsoleFacetsColdReadIsDeferred(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	vals, pending, err := c.ConsoleFacets(ctx, core.TypeMovie, "year")
	if err != nil || !pending || len(vals) != 0 {
		t.Fatalf("cold: %v %v %v", vals, pending, err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		vals, pending, err = c.ConsoleFacets(ctx, core.TypeMovie, "year")
		if err != nil {
			t.Fatal(err)
		}
		if !pending {
			if len(vals) != 1 || vals[0].Count != 3 {
				t.Fatalf("counts: %v", vals)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("deferred facet did not populate")
}

func TestConsoleWatchlistAndRatedFilters(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	if _, err := c.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS watchlist_items(user_id INTEGER,item_key INTEGER,PRIMARY KEY(user_id,item_key)); INSERT INTO watchlist_items(user_id,item_key) VALUES(7,?)`, int64(core.MovieKey(2))); err != nil {
		t.Fatal(err)
	}
	want := true
	f := core.Filter{Type: core.TypeMovie, Group: &core.FilterGroup{OnWatchlist: &want, UserID: 7}}
	p, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
	if err != nil || p.Total != 1 || p.Items[0].Key != core.MovieKey(2) {
		t.Fatalf("watchlist %+v %v", p, err)
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO state_watch(user_id,rating_key,user_rating,rating_at) VALUES(7,?,8,1000000000)`, int64(core.MovieKey(2))); err != nil {
		t.Fatal(err)
	}
	f.Group = &core.FilterGroup{StateField: "lastRatedAt", StateOperator: ">", StateValue: 0, UserID: 7}
	p, err = c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
	if err != nil || p.Total != 1 || p.Items[0].Key != core.MovieKey(2) {
		t.Fatalf("rated %+v %v", p, err)
	}
	if !groupHasState(f.Group) {
		t.Fatal("watchlist must not use stale page cache")
	}
}

func TestConsoleNarrowPagingMatchesBrowse(t *testing.T) {
	c := largeCatalogue(t, 400)
	ctx := context.Background()
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET year=1980+(tmdb_id%40),rating=(tmdb_id%100)/10.0,runtime_sec=3600+(tmdb_id%100)*60`); err != nil {
		t.Fatal(err)
	}
	for _, order := range []core.BrowseQuery{
		{Sort: core.SortPopularity, Desc: true}, {Sort: core.SortYear, Desc: true, SecondarySorts: []core.SortSpec{{Sort: core.SortTitle}}}, {Sort: core.SortTitle},
	} {
		for _, typ := range []core.MediaType{core.TypeMovie, ""} {
			q := order
			q.Filter = core.Filter{Type: typ, YearFrom: 1990, MinRating: 5, MaxRuntime: 2 * time.Hour}
			q.NoTotal = true
			q.Limit = 17
			q.Offset = 13
			regular, err := c.Browse(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			q.Filter.LocalOnly = true
			console, err := c.Browse(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			if len(console.Items) != len(regular.Items) {
				t.Fatalf("size %d vs %d", len(console.Items), len(regular.Items))
			}
			for i := range console.Items {
				if console.Items[i].Key != regular.Items[i].Key {
					t.Fatalf("sort %s type %s item %d differs", q.Sort, typ, i)
				}
			}
		}
	}
}

func TestConsoleYearEmptyGuardRespectsBooleanAlternatives(t *testing.T) {
	c := largeCatalogue(t, 4)
	ctx := context.Background()
	for _, tc := range []struct {
		f     core.Filter
		empty bool
	}{
		{core.Filter{Type: core.TypeMovie, YearFrom: 2199}, true},
		{core.Filter{Group: &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{{Filter: &core.Filter{YearFrom: 2199}}}}}, true},
		{core.Filter{Group: &core.FilterGroup{Operator: "or", Children: []core.FilterGroup{{Filter: &core.Filter{YearFrom: 2199}}, {Filter: &core.Filter{YearFrom: 1990}}}}}, false},
	} {
		empty, err := c.consoleYearEmpty(ctx, tc.f)
		if err != nil || empty != tc.empty {
			t.Fatalf("%+v empty=%v err=%v", tc.f, empty, err)
		}
	}
}

func TestConsoleConjunctionNormalizationPreservesMembership(t *testing.T) {
	c := largeCatalogue(t, 400)
	ctx := context.Background()
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET year=1980+(tmdb_id%40),rating=(tmdb_id%100)/10.0,runtime_sec=3600+(tmdb_id%100)*60`); err != nil {
		t.Fatal(err)
	}
	original := core.Filter{Group: &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{
		{Filter: &core.Filter{YearFrom: 1990, YearTo: 1999}}, {Filter: &core.Filter{MinRating: 5}}, {Filter: &core.Filter{MaxRuntime: 2 * time.Hour}},
	}}}
	flat := normalizeConsoleFilter(original)
	if flat.Group != nil || flat.YearFrom != 1990 || flat.MinRating != 5 {
		t.Fatalf("not flattened: %+v", flat)
	}
	a, err := c.Browse(ctx, core.BrowseQuery{Filter: original, Sort: core.SortTitle, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Browse(ctx, core.BrowseQuery{Filter: flat, Sort: core.SortTitle, Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	if a.Total != b.Total || len(a.Items) != len(b.Items) {
		t.Fatal("normalization changed membership")
	}
	for i := range a.Items {
		if a.Items[i].Key != b.Items[i].Key {
			t.Fatal("normalization changed order")
		}
	}
	// Two genre chips mean BOTH genres; flattening their slices would mean OR.
	conflict := core.Filter{Group: &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{{Filter: &core.Filter{Genres: []string{"Comedy"}}}, {Filter: &core.Filter{Genres: []string{"Drama"}}}}}}
	if normalizeConsoleFilter(conflict).Group == nil {
		t.Fatal("conflicting fields were flattened")
	}
	original.Group.Operator = "or"
	if normalizeConsoleFilter(original).Group == nil {
		t.Fatal("OR was flattened")
	}
}

func TestConsoleNormalizationKeepsViewerStateResidual(t *testing.T) {
	unwatched := true
	original := core.Filter{Group: &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{{Filter: &core.Filter{YearFrom: 1990}}, {Unwatched: &unwatched, UserID: 7}, {Filter: &core.Filter{MinRating: 7}}}}}
	flat := normalizeConsoleFilter(original)
	if flat.YearFrom != 1990 || flat.MinRating != 7 || flat.Group == nil || flat.Group.Unwatched == nil || flat.Group.UserID != 7 {
		t.Fatalf("lost state or metadata: %+v", flat)
	}
	if original.Group.Children[0].Filter.YearFrom != 1990 || original.YearFrom != 0 || len(original.Group.Children) != 3 {
		t.Fatal("mutated caller AST")
	}
}
