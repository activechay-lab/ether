package catalog

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestReleaseMatchIndexedOnLargeCatalogue(t *testing.T) {
	c := newChunkTestCatalog(t)
	if store.DialectOf(c.db) != store.SQLite {
		t.Skip("SQLite query-plan assertion")
	}
	_, err := c.db.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<100000)
 INSERT INTO catalog_titles(key,type,tmdb_id,imdb,tvdb,title,year,released_ts) SELECT 1000000000000000+i,'movie',i,'tt'||i,i,'Film '||i,2020,1577836800 FROM n`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.db.Exec(`INSERT INTO catalog_release_names(name,key) SELECT 'film '||tmdb_id,key FROM catalog_titles`)
	if err != nil {
		t.Fatal(err)
	}
	queries := []struct {
		q, index string
		arg      any
	}{
		{releaseMatchCols + `JOIN catalog_release_names n ON n.key=t.key WHERE n.name=? LIMIT ?`, "SEARCH n USING PRIMARY KEY (name=?)", "film 4321"},
		{releaseMatchCols + `WHERE t.tmdb_id=? LIMIT ?`, "catalog_titles_tmdb", int64(4321)},
		{releaseMatchCols + `WHERE t.imdb=? LIMIT ?`, "catalog_titles_imdb", "tt4321"},
		{releaseMatchCols + `WHERE t.tvdb=? LIMIT ?`, "catalog_titles_tvdb", int64(4321)},
	}
	for _, q := range queries {
		rows, err := c.db.Query(`EXPLAIN QUERY PLAN `+q.q, q.arg, 257)
		if err != nil {
			t.Fatal(err)
		}
		var plan string
		for rows.Next() {
			var a, b, d int
			var detail string
			if err = rows.Scan(&a, &b, &d, &detail); err != nil {
				t.Fatal(err)
			}
			plan += detail + "\n"
		}
		rows.Close()
		if err = rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan, q.index) || strings.Contains(plan, "SCAN t") || strings.Contains(plan, "SCAN n") {
			t.Fatalf("unbounded plan for %s: %s", q.q, plan)
		}
	}
	r := core.Release{ID: "hit", Info: releaseparse.Parse("Film.4321.2020.1080p.WEB-DL"), TitleIDs: core.IDs{TMDB: 4321}}
	got, err := c.ReleaseMatches(context.Background(), []core.Release{r})
	if err != nil || len(got["hit"]) != 1 {
		t.Fatalf("match %v %v", got, err)
	}
}

func TestReleaseNameIndexTracksUpdatesAndOverflow(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	title := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "The Café", OriginalTitle: "Old Name", Year: 2020, Released: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := c.UpsertTitle(ctx, title, true); err != nil {
		t.Fatal(err)
	}
	title.Title = "New Name"
	title.OriginalTitle = ""
	if err := c.UpsertTitle(ctx, title, true); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want int
	}{{"cafe", 0}, {"new name", 1}, {"old name", 1}} {
		var n int
		if err := c.db.QueryRow(`SELECT count(*) FROM catalog_release_names WHERE name=?`, tc.name).Scan(&n); err != nil || n != tc.want {
			t.Fatalf("%s: %d %v", tc.name, n, err)
		}
	}
	for i := 2; i <= 258; i++ {
		if _, err := c.db.Exec(`INSERT INTO catalog_release_names(name,key) VALUES('new name',?)`, i); err != nil {
			t.Fatal(err)
		}
		if _, err := c.db.Exec(`INSERT INTO catalog_titles(key,type,title,year,released_ts) VALUES(?,'movie','New Name',2020,1577836800)`, i); err != nil {
			t.Fatal(err)
		}
	}
	r := core.Release{ID: "ambiguous", Info: releaseparse.Parse("New.Name.2020.1080p")}
	got, err := c.ReleaseMatches(ctx, []core.Release{r})
	if err != nil || len(got) != 0 {
		t.Fatal(fmt.Sprint(got), err)
	}
}

func TestReleaseNameMigrationBackfillsBothNames(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	_, err := c.db.Exec(`INSERT INTO catalog_titles(key,type,title,original_title) VALUES(123,'movie','The Café','Original Name'),(124,'episode','Episode','')`)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DROP TABLE catalog_release_names`); err != nil {
		t.Fatal(err)
	}
	if err = migrateReleaseNames(ctx, tx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		want int
	}{{"cafe", 1}, {"original name", 1}, {"episode", 0}} {
		var n int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM catalog_release_names WHERE name=?`, tc.name).Scan(&n); err != nil || n != tc.want {
			t.Fatalf("%s: %d %v", tc.name, n, err)
		}
	}
}
