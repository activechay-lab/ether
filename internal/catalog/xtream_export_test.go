package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestXtreamLargeCatalogueKeyset(t *testing.T) {
	// Seeding 100001 rows through the trigram search triggers alone outlasts
	// the package timeout under -race; the cursor invariants hold at any size,
	// and the full-size walk and its budget run in every non-race build.
	n := 100001
	if raceEnabled {
		n = 2501
	}
	c := largeCatalogue(t, n)
	ctx := context.Background()
	after := core.RatingKey(0)
	count, pages := 0, 0
	start := time.Now()
	for {
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Sort: core.SortKey, AfterKey: after, Limit: 250, NoTotal: true, ListRows: &core.ListRows{Facts: core.FactGenres}})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Items) > 250 {
			t.Fatal("unbounded page")
		}
		for _, item := range page.Items {
			if item.Key <= after {
				t.Fatal("cursor failed to advance")
			}
			after = item.Key
			count++
		}
		if len(page.Items) < 250 {
			break
		}
	}
	elapsed := time.Since(start)
	if count != n || pages != n/250+1 {
		t.Fatalf("count=%d pages=%d", count, pages)
	}
	if !raceEnabled && elapsed > 30*time.Second {
		t.Fatalf("keyset walk too slow: %v", elapsed)
	}
	t.Logf("%d actual SQL catalogue rows: %v, %d bounded pages", n, elapsed, pages)
}

// An export page (ListRows) carries a row's credits and the facts its
// policy reads, a title the caller judges on its facts comes back in full,
// and the partial titles reach only the same viewer's artwork reads.
func TestExportListRowsStayPartial(t *testing.T) {
	c := largeCatalogue(t, 3)
	key := core.MovieKey(1)
	for i := 0; i < 5; i++ {
		if _, err := c.db.Exec(`INSERT INTO catalog_credits(key,person_tmdb,name,kind,ord) VALUES(?,?,?,?,?)`, int64(key), 100+i, "Actor", "cast", i); err != nil {
			t.Fatal(err)
		}
	}
	for j, kind := range []string{"director", "writer"} {
		if _, err := c.db.Exec(`INSERT INTO catalog_credits(key,person_tmdb,name,kind,ord) VALUES(?,?,?,?,0)`, int64(key), 200+j, "Crew", kind); err != nil {
			t.Fatal(err)
		}
	}
	ctx := core.WithCatalogViewer(context.Background(), &core.User{ID: 7})
	page := func(lr *core.ListRows) core.Title {
		t.Helper()
		p, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Sort: core.SortKey, Limit: 250, NoTotal: true, ListRows: lr})
		if err != nil || len(p.Items) != 3 || p.Items[0].Key != key {
			t.Fatalf("page %+v %v", p.Items, err)
		}
		return p.Items[0]
	}
	if got := page(&core.ListRows{Facts: core.FactGenres}); len(got.Cast) != 3 || len(got.Directors) != 1 || len(got.Writers) != 0 || len(got.Keywords) != 0 {
		t.Fatalf("export row cast=%d directors=%d writers=%d keywords=%v", len(got.Cast), len(got.Directors), len(got.Writers), got.Keywords)
	}
	if got := page(&core.ListRows{Facts: core.FactGenres | core.FactKeywords}); len(got.Keywords) != 1 || len(got.Cast) != 3 {
		t.Fatalf("the policy reads keywords, the row lacks them: %v", got.Keywords)
	}
	arts, err := c.ArtworkTitles(ctx, []core.RatingKey{key})
	if got := arts[key]; err != nil || len(got.Cast) != 3 || len(got.Keywords) != 1 {
		t.Fatalf("the signer did not get the viewer's export row: %+v %v", got, err)
	}
	other := core.WithCatalogViewer(context.Background(), &core.User{ID: 8})
	if arts, err := c.ArtworkTitles(other, []core.RatingKey{key}); err != nil || len(arts[key].Cast) != 5 {
		t.Fatalf("another viewer got this viewer's export row: %+v %v", arts[key], err)
	}
	if got := page(&core.ListRows{Facts: core.FactGenres, Detail: func(core.Title) bool { return true }}); len(got.Cast) != 5 || len(got.Writers) != 1 || len(got.Keywords) != 1 {
		t.Fatalf("a title judged on its facts came back partial: cast=%d writers=%d", len(got.Cast), len(got.Writers))
	}
	full, err := c.CachedGetMany(context.Background(), []core.RatingKey{key})
	if got := full[key]; err != nil || len(got.Cast) != 5 || len(got.Writers) != 1 || len(got.Keywords) != 1 {
		t.Fatalf("a full read got an export's partial title: cast=%d writers=%d keywords=%v %v", len(got.Cast), len(got.Writers), got.Keywords, err)
	}
}
