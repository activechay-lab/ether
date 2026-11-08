package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Titles a bulk transaction inserts are folded into the title search index
// in one statement; every other write keeps the per-row trigger. Both must
// leave exactly the index the trigger alone would: FTS5's integrity-check
// compares the external-content index with catalog_titles itself, and each
// title is found by a substring the moment its transaction commits.
func TestFTSFoldMatchesTrigger(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if store.DialectOf(db) == store.Postgres {
		t.Skip("PostgreSQL searches a pg_trgm index on catalog_titles")
	}
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.ftsFold {
		t.Fatal("the FTS insert trigger does not honour the bulk marker")
	}
	mk := func(i int) core.Title {
		return core.Title{Key: core.MovieKey(int64(7000 + i)), Type: core.TypeMovie, IDs: core.IDs{TMDB: int64(7000 + i)},
			Title: fmt.Sprintf("Zebrafold %04d", i), OriginalTitle: fmt.Sprintf("Origfold %04d", i), Released: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)}
	}
	var bulk []core.Title
	for i := 0; i < 40; i++ {
		bulk = append(bulk, mk(i))
	}
	bulk = append(bulk, mk(3)) // a repeat in the batch is an update of the new row
	if err := c.upsertMany(ctx, bulk, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	for i := 40; i < 45; i++ { // one at a time: the trigger path
		if err := c.UpsertTitle(ctx, mk(i), true); err != nil {
			t.Fatal(err)
		}
	}
	renamed := mk(5)
	renamed.Title = "Zebrafold renamed"
	if err := c.upsertMany(ctx, []core.Title{renamed, mk(45), mk(46)}, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_titles_fts(catalog_titles_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("title search index differs from catalog_titles: %v", err)
	}
	var marker int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_fts_bulk`).Scan(&marker); err != nil || marker != 0 {
		t.Fatalf("bulk marker left behind: %d %v", marker, err)
	}
	found := func(q string) []int64 {
		rows, err := db.QueryContext(ctx, `SELECT rowid FROM catalog_titles_fts WHERE catalog_titles_fts MATCH ? ORDER BY rowid`, `"`+q+`"`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var k int64
			rows.Scan(&k)
			out = append(out, k)
		}
		return out
	}
	for i := 0; i < 47; i++ {
		want := int64(mk(i).Key)
		q := fmt.Sprintf("Origfold %04d", i)
		if got := found(q); len(got) != 1 || got[0] != want {
			t.Fatalf("%q found %v, want [%d]", q, got, want)
		}
	}
	if got := found("Zebrafold 0005"); len(got) != 0 {
		t.Fatalf("the old name of a renamed title is still indexed: %v", got)
	}
	if got := found("Zebrafold renamed"); len(got) != 1 {
		t.Fatalf("a renamed title is not indexed under its new name: %v", got)
	}
}
