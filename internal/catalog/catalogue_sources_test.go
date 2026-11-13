package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	_ "github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

func TestCatalogueSourceUnionAndNarrowing(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 3; i++ {
		if err = c.UpsertTitle(ctx, core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, Title: "Movie", Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), IDs: core.IDs{TMDB: i}, Year: int(2000 + i)}, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		source string
		id     int64
	}{{"a", 1}, {"a", 2}, {"b", 2}, {"b", 3}} {
		if _, err = db.ExecContext(ctx, `INSERT INTO libraries_catalogue_members(source_id,key) VALUES(?,?)`, row.source, int64(core.MovieKey(row.id))); err != nil {
			t.Fatal(err)
		}
	}
	// A compatible union deduplicates shared members before pagination, and
	// a saved rule narrows that union rather than replacing its membership.
	f := core.Filter{Type: core.TypeMovie, LocalOnly: true, CatalogueSources: []string{"a", "b"}, YearTo: 2002}
	page, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("union: total=%d items=%d", page.Total, len(page.Items))
	}
	f.CatalogueSources = []string{"a"}
	f.YearFrom = 2002
	page, err = c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Key != core.MovieKey(2) {
		t.Fatalf("narrowed: %+v", page)
	}
}
