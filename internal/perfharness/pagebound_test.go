package perfharness

import (
	"context"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestDetailCapsFactlessKindsAndInheritedRows(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	exec := func(q string) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	// Direct inserts only: no scale generator, metadata service or App.
	exec(`INSERT INTO catalog_titles (key,type,show_key,season,episode,child_count) VALUES
 (1,'movie',0,0,0,0), (10,'show',0,0,0,3),
 (11,'season',10,1,0,8), (12,'season',10,2,0,0),
 (13,'season',10,3,0,0), (14,'season',10,0,0,4),
 (101,'episode',10,1,1,0), (102,'episode',10,2,2,0),
 (20,'show',0,0,0,2), (21,'season',20,1,0,6), (22,'season',20,2,0,6)`)
	// Numbering reads positive seasons even if later, unaired, adult or
	// childless. Specials and the other show's seasons do not count.
	exec(`UPDATE catalog_titles SET released_ts=4102444800, adult=1 WHERE key=13`)
	check := func(want map[string]int) map[string]int {
		t.Helper()
		caps, err := DetailCaps(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(caps, want) {
			t.Fatalf("caps=%v, want %v", caps, want)
		}
		t.Logf("caps=%v", caps)
		return caps
	}
	// The old fact-driven query returned an empty map for this library.
	check(map[string]int{"movie": 10, "show": 16, "season": 11, "episode": 14})
	exec(`INSERT INTO catalog_keywords (key,keyword) VALUES (10,'one'),(10,'two')`)
	exec(`INSERT INTO catalog_alt_titles (key,title) VALUES (10,'alias')`)
	exec(`INSERT INTO catalog_credits (key,person_tmdb) VALUES (10,1),(10,2)`)
	exec(`INSERT INTO catalog_genres (key,genre) VALUES (1,'Drama'),(102,'Drama')`)
	// Season: baseline10 + parent identity1 + keywords2 + alias1 =14.
	// Episode: those14 + own genre1 + all three positive seasons =18.
	// Show keeps own facts5 + existing aired-count allowance6 + baseline10.
	caps := check(map[string]int{"movie": 11, "show": 21, "season": 14, "episode": 18})
	for _, update := range []string{
		`UPDATE catalog_titles SET season=1 WHERE key=102`,
		`UPDATE catalog_titles SET season=2,episode=0 WHERE key=102`,
	} {
		exec(update)
		check(map[string]int{"movie": 11, "show": 21, "season": 14, "episode": 15})
	}
	exec(`UPDATE catalog_titles SET episode=2 WHERE key=102`)

	// Preserve the gate's ability to reject unrelated work. This real SQL
	// walk returns every pair of the fixture's titles three times while the
	// response renders just one season and one episode.
	measured, stats := store.WithQueryStats(ctx)
	rows, err := db.QueryContext(measured, `SELECT a.key FROM catalog_titles a CROSS JOIN catalog_titles b
 CROSS JOIN (SELECT 1 AS n UNION ALL SELECT 2 UNION ALL SELECT 3) copies`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var key int64
		if err := rows.Scan(&key); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	result := Result{Client: "plex", Step: "mixed", Phase: "cold", Tag: "walk", RenderedItems: map[string]int{"season": 1, "episode": 1}}
	row := &Row{Client: result.Client, Step: result.Step, Class: classBrowse}
	ApplyPageBound([]*Row{row}, []Result{result}, map[string]TraceRecord{"walk": {Rows: stats.Rows()}}, caps)
	if row.PageBound != 232 || row.PageItems != 2 {
		t.Fatalf("mixed page bound=%d items=%d, want232/2", row.PageBound, row.PageItems)
	}
	if why, over := row.PageOver(); !over {
		t.Fatalf("unrelated SQL walk (%d rows) was accepted", stats.Rows())
	} else {
		t.Log(why)
	}
	row.PageRows = row.PageBound
	if why, over := row.PageOver(); over {
		t.Fatal(why)
	}
	row.PageRows++
	if _, over := row.PageOver(); !over {
		t.Fatal("one row over the derived bound must fail")
	}
}

func TestDetailCapsFactlessOrphanBaseline(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_titles (key,type,show_key,season,episode) VALUES
 (1,'movie',0,0,0),(2,'show',0,0,0),(3,'season',999,1,0),(4,'episode',999,2,1)`); err != nil {
		t.Fatal(err)
	}
	caps, err := DetailCaps(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if want := (map[string]int{"movie": 10, "show": 10, "season": 10, "episode": 11}); !reflect.DeepEqual(caps, want) {
		// The parent is absent (no identity row), but numbering still reads
		// the orphan season sharing show_key999, exactly as hydrateShows does.
		t.Fatalf("caps=%v, want%v", caps, want)
	}
}
