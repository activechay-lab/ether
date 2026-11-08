package viewerpolicy_test

import (
	"context"
	"database/sql"
	"testing"

	_ "github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/viewerpolicy"
)

// The epoch must move for every catalogue write that can turn some viewer's
// allow into a deny under the rules in force, and only for those: the
// fence relies on the first half, cache keys and fence retries on the
// second. Runs on SQLite and (ETHER_TEST_POSTGRES) PostgreSQL, whose
// triggers are written separately.
func TestPolicyEpochMovesOnlyForRevokingWrites(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	with := func(in viewerpolicy.Inputs) {
		t.Helper()
		read := func(context.Context, *sql.Tx) (viewerpolicy.Inputs, error) { return in, nil }
		none := func(context.Context, *sql.Tx) (viewerpolicy.Inputs, error) { return viewerpolicy.Inputs{}, nil }
		if err := viewerpolicy.RebuildInputs(ctx, db, viewerpolicy.Rebuild{Source: viewerpolicy.SourceConfig, Read: read},
			viewerpolicy.Rebuild{Source: viewerpolicy.SourceSafety, Read: none}, viewerpolicy.Rebuild{Source: viewerpolicy.SourceRules, Read: none}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(name string, moves bool, queries ...string) {
		t.Helper()
		before, err := viewerpolicy.Read(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, q := range queries {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				tx.Rollback()
				t.Fatalf("%s: %s: %v", name, q, err)
			}
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		after, err := viewerpolicy.Read(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if moved := after != before; moved != moves {
			t.Errorf("%s: epoch %d -> %d, want moved=%v", name, before, after, moves)
		}
		// PostgreSQL advances once per transaction. SQLite advances per row;
		// a transaction still writes the epoch page once.
		if store.DialectOf(db) == store.Postgres && after > before+1 {
			t.Errorf("%s: one transaction advanced the epoch %d times", name, after-before)
		}
	}
	with(viewerpolicy.Inputs{})
	check("new title", false, `INSERT INTO catalog_titles(key,type,tmdb_id,title,content_rating,browsable) VALUES(101,'movie',101,'A','PG',1)`,
		`INSERT INTO catalog_titles(key,type,tmdb_id,title,content_rating,browsable) VALUES(102,'movie',102,'B','PG',1)`)
	check("detail fill, no rules", false,
		`INSERT INTO catalog_genres(key,genre) VALUES(101,'Horror'),(101,'Drama')`,
		`INSERT INTO catalog_keywords(key,keyword) VALUES(101,'ghost')`,
		`INSERT INTO catalog_studios(key,studio) VALUES(101,'S')`,
		`INSERT INTO catalog_countries(key,country) VALUES(101,'US')`,
		`INSERT INTO catalog_credits(key,person_tmdb,name,kind) VALUES(101,7,'P','cast'),(101,8,'Q','cast')`,
		`UPDATE catalog_titles SET popularity=9, vote_count=3, rating=7.5, year=2001, runtime_sec=60, language='en' WHERE key=101`)
	check("facet removal, no rules", false, `DELETE FROM catalog_genres WHERE key=101`)
	check("credit removal", true, `DELETE FROM catalog_credits WHERE key=101 AND person_tmdb=7`)
	check("credit details", false, `UPDATE catalog_credits SET name='Q2', ord=4 WHERE key=101`)
	check("rating, many rows", true, `UPDATE catalog_titles SET content_rating='R' WHERE key IN (101,102)`)
	check("share label", true, `UPDATE catalog_titles SET collection_name='Saga' WHERE key=101`)
	check("title removal", true, `DELETE FROM catalog_titles WHERE key=102`)
	check("no-op delete", false, `DELETE FROM catalog_titles WHERE key=999`, `DELETE FROM catalog_credits WHERE key=999`)

	blocked := viewerpolicy.Inputs{}
	blocked.Field("genre", "Horror")
	viewerpolicy.RulesInputs(blocked, []core.LibraryExclusion{{Rules: []core.CollectionRule{{Field: "popularity", Op: "gte", Value: []byte(`5`)}}}})
	with(blocked)
	check("first genre", true, `INSERT INTO catalog_genres(key,genre) VALUES(101,'Drama')`)
	check("unread genre", false, `INSERT INTO catalog_genres(key,genre) VALUES(101,'Comedy')`)
	check("blocked genre, folded", true, `INSERT INTO catalog_genres(key,genre) VALUES(101,' HORROR')`)
	check("unread removal", false, `DELETE FROM catalog_genres WHERE key=101 AND genre='Comedy'`)
	check("unfoldable genre", true, `INSERT INTO catalog_genres(key,genre) VALUES(101,'Animación')`)
	check("last genres removed", true, `DELETE FROM catalog_genres WHERE key=101`)
	check("multi-row first genres", true, `INSERT INTO catalog_genres(key,genre) VALUES(101,'Drama'),(101,'Comedy')`)
	check("keywords still unread", false, `INSERT INTO catalog_keywords(key,keyword) VALUES(101,'ghosts')`)
	check("ruled popularity", true, `UPDATE catalog_titles SET popularity=1 WHERE key=101`)
	check("unruled votes", false, `UPDATE catalog_titles SET vote_count=99 WHERE key=101`)
}
