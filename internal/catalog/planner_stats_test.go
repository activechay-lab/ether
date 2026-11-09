package catalog

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

// catalog_titles must keep complete planner statistics as it grows, through
// both places that refresh them: Open and the daily Maintain. A sampled
// ANALYZE (PRAGMA optimize's default) records a few hundred or thousand rows
// per type, and the planner then walks every episode for per-show reads.
func TestCatalogTitlesKeepCompleteStatisticsAsTheyGrow(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	open := func() *sql.DB {
		t.Helper()
		db, err := store.Open(ctx, dir)
		if err != nil {
			t.Fatal(err)
		}
		return db
	}
	grow := func(db *sql.DB, to int) {
		t.Helper()
		if _, err := db.ExecContext(ctx, `WITH RECURSIVE n(i) AS (SELECT COALESCE(MAX(key),0)+1 FROM catalog_titles UNION ALL SELECT i+1 FROM n WHERE i<?)
			INSERT INTO catalog_titles(key, type, tmdb_id, show_key, season, episode, sort_title, year)
			SELECT i, CASE i%4 WHEN 0 THEN 'movie' WHEN 1 THEN 'show' WHEN 2 THEN 'season' ELSE 'episode' END,
				i, i/40, i%7, i%13, 'title ' || i, 1950 + i%70 FROM n`, to); err != nil {
			t.Fatal(err)
		}
	}
	// Every index led by type, the column a sample gets wrong: its first
	// statistic is the row count, its second the rows per type.
	check := func(db *sql.DB, when string, want int) {
		t.Helper()
		rows, err := db.QueryContext(ctx, `SELECT il.name, COALESCE(s.stat, '') FROM pragma_index_list('catalog_titles') il
			LEFT JOIN sqlite_stat1 s ON s.tbl='catalog_titles' AND s.idx=il.name
			WHERE il.partial=0 AND (SELECT name FROM pragma_index_info(il.name) WHERE seqno=0)='type'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		seen := 0
		for rows.Next() {
			var idx, stat string
			if err := rows.Scan(&idx, &stat); err != nil {
				t.Fatal(err)
			}
			f := strings.Fields(stat)
			if len(f) < 2 || f[0] != strconv.Itoa(want) || f[1] != strconv.Itoa(want/4) {
				t.Errorf("%s: %s statistics %q, want %d rows and %d per type", when, idx, stat, want, want/4)
			}
			seen++
		}
		if err := rows.Err(); err != nil || seen == 0 {
			t.Fatalf("%s: %d type-leading indexes (%v)", when, seen, err)
		}
	}

	db := open()
	grow(db, 3000)
	db.Close()
	db = open() // the new rows have no statistics: Open analyses them
	defer db.Close()
	check(db, "after Open", 3000)

	grow(db, 40000) // over ten times larger: stale
	if err := store.Maintain(ctx, db); err != nil {
		t.Fatal(err)
	}
	check(db, "after Maintain", 40000)
}
