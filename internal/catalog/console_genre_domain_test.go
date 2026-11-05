package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

func TestConsoleGenreDomainTracksInsertUpdateDelete(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	for _, v := range []struct {
		id    int64
		genre string
	}{{1, "Comedy"}, {1, "Thriller"}, {2, "Comedy"}, {3, "Thriller"}} {
		if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_genres(key,genre) VALUES(?,?)`, int64(core.MovieKey(v.id)), v.genre); err != nil {
			t.Fatal(err)
		}
	}
	f := core.Filter{Type: core.TypeMovie, Genres: []string{"Comedy"}, Group: &core.FilterGroup{Filter: &core.Filter{Genres: []string{"Thriller"}}}}
	empty, err := c.consoleDomainEmpty(ctx, f)
	if err != nil || empty {
		t.Fatalf("shared genre title lost: %v %v", empty, err)
	}
	var count int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_genre_pairs WHERE a='Comedy' AND b='Thriller'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("pair count %d: %v", count, err)
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM catalog_genres WHERE key=? AND genre='Thriller'`, int64(core.MovieKey(1))); err != nil {
		t.Fatal(err)
	}
	empty, err = c.consoleDomainEmpty(ctx, f)
	if err != nil || !empty {
		t.Fatalf("disjoint genres not pruned: %v %v", empty, err)
	}
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_genres SET genre='Comedy' WHERE key=? AND genre='Thriller'`, int64(core.MovieKey(3))); err != nil {
		t.Fatal(err)
	}
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_genre_pairs WHERE a='Comedy' AND b='Comedy'`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("updated diagonal %d: %v", count, err)
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM catalog_genres`); err != nil {
		t.Fatal(err)
	}
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_genre_pairs`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted pairs retained %d: %v", count, err)
	}
}

func TestConsoleDomainNeverPrunesAnORAlternative(t *testing.T) {
	c := largeCatalogue(t, 1)
	ctx := context.Background()
	if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_genres(key,genre) VALUES(?,'Comedy')`, int64(core.MovieKey(1))); err != nil {
		t.Fatal(err)
	}
	f := core.Filter{Group: &core.FilterGroup{Operator: "or", Children: []core.FilterGroup{{Filter: &core.Filter{Genres: []string{"Missing"}}}, {Filter: &core.Filter{Genres: []string{"Comedy"}}}}}}
	empty, err := c.consoleDomainEmpty(ctx, f)
	if err != nil || empty {
		t.Fatalf("OR alternative pruned: %v %v", empty, err)
	}
	for _, f := range []core.Filter{{Genres: []string{"Missing"}}, {Keywords: []string{"missing"}}, {Person: 999999}, {Credits: []core.CreditFilter{{Names: []string{"Nobody Here"}, Kind: "director"}}}} {
		empty, err := c.consoleDomainEmpty(ctx, f)
		if err != nil || !empty {
			t.Fatalf("unknown fact not pruned %+v: %v %v", f, empty, err)
		}
	}
}

// Two metadata writers can otherwise each observe only its own uncommitted
// genre and leave no cross-pair witness after both commit.
func TestConsoleGenreDomainConcurrentWriters(t *testing.T) {
	c := largeCatalogue(t, 1)
	if store.DialectOf(c.db) != store.Postgres {
		t.Skip("PostgreSQL multi-writer snapshot regression")
	}
	c.db.SetMaxOpenConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Rollback()
	second, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Rollback()
	key := int64(core.MovieKey(1))
	if _, err := first.ExecContext(ctx, `INSERT INTO catalog_genres(key,genre) VALUES(?,'Comedy')`, key); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := second.ExecContext(ctx, `INSERT INTO catalog_genres(key,genre) VALUES(?,'Thriller')`, key)
		if err == nil {
			err = second.Commit()
		}
		done <- err
	}()
	var secondErr error
	finished := false
	select {
	case secondErr = <-done:
		finished = true
	case <-time.After(50 * time.Millisecond):
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	if !finished {
		select {
		case secondErr = <-done:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if secondErr != nil {
		t.Fatal(secondErr)
	}
	var count int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_genre_pairs WHERE a='Comedy' AND b='Thriller'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("committed genres lack witness: %d %v", count, err)
	}
}
