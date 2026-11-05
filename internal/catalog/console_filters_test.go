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
