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

// Credits a bulk transaction inserts count into catalog_person_names, and
// new people into its search index, exactly as the per-row triggers do: a
// count per credit, the last non-empty credited name, a new person starting
// with an empty name when no credit names them.
func TestPersonNamesFoldMatchesTrigger(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	if store.DialectOf(db) == store.Postgres {
		t.Skip("PostgreSQL keeps person names with its row trigger")
	}
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c.personFold {
		t.Fatal("the person-name triggers do not honour the bulk marker")
	}
	// Person 1 is known before through the trigger path (a credit written
	// outside the catalogue's bulk writes).
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_credits(key,person_tmdb,name,role,kind,ord) VALUES(1,1,'Ann Old','r','cast',0)`); err != nil {
		t.Fatal(err)
	}
	title := func(i int, cast ...core.Person) core.Title {
		return core.Title{Key: core.MovieKey(int64(8000 + i)), Type: core.TypeMovie, IDs: core.IDs{TMDB: int64(8000 + i)}, Title: fmt.Sprint("Credited ", i),
			Released: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC), Cast: cast}
	}
	p := func(id int64, name, role string) core.Person { return core.Person{TMDB: id, Name: name, Role: role} }
	batch := []core.Title{
		title(1, p(1, "Ann New", "a"), p(2, "", "b"), p(3, "Cy", "c")),
		title(2, p(2, "Bo", "d"), p(3, "Cy", "e"), p(4, "", "f")),
		title(3, p(1, "", "g"), p(5, "Di", "h")),
	}
	if err := c.upsertMany(ctx, batch, upsertOpts{browsable: true, detail: true}); err != nil {
		t.Fatal(err)
	}
	want := map[int64]struct {
		name    string
		credits int
	}{1: {"Ann New", 3}, 2: {"Bo", 2}, 3: {"Cy", 2}, 4: {"", 1}, 5: {"Di", 1}}
	rows, err := db.QueryContext(ctx, `SELECT person_tmdb, name, credits FROM catalog_person_names ORDER BY person_tmdb`)
	if err != nil {
		t.Fatal(err)
	}
	got := 0
	for rows.Next() {
		var id int64
		var name string
		var n int
		rows.Scan(&id, &name, &n)
		got++
		if w, ok := want[id]; !ok || w.name != name || w.credits != n {
			t.Errorf("person %d: %q %d, want %+v", id, name, n, want[id])
		}
	}
	rows.Close()
	if got != len(want) {
		t.Fatalf("%d people, want %d", got, len(want))
	}
	var counted int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_credits c JOIN catalog_person_names p ON p.person_tmdb=c.person_tmdb`).Scan(&counted); err != nil || counted != 9 {
		t.Fatalf("credits joined to people: %d %v", counted, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_person_names_fts(catalog_person_names_fts) VALUES('integrity-check')`); err != nil {
		t.Fatalf("person search index differs from catalog_person_names: %v", err)
	}
	for _, q := range []string{"Bo", "Di", "Ann"} {
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_person_names_fts WHERE catalog_person_names_fts MATCH ?`, q+"*").Scan(&n); err != nil || n != 1 {
			t.Errorf("%s*: %d matches %v", q, n, err)
		}
	}
}
