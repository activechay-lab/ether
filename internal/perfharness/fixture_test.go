package perfharness

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	_ "github.com/braedonsaunders/ether/internal/state" // migrations
	"github.com/braedonsaunders/ether/internal/store"
)

func TestGenerateBuildsTheScale(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := SmallScale.Divide(50)
	s.Viewers, s.HistoryPerViewer = 2, 30
	if err := Generate(ctx, db, s, time.Now()); err != nil {
		t.Fatal(err)
	}
	var titles, history, fts, triggers int
	for q, dst := range map[string]*int{
		`SELECT COUNT(*) FROM catalog_titles`:                                                   &titles,
		`SELECT COUNT(*) FROM state_history`:                                                    &history,
		`SELECT COUNT(*) FROM catalog_titles_fts WHERE title LIKE '%Episode 1%'`:                &fts,
		`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND tbl_name='catalog_titles'`: &triggers,
	} {
		if err := db.QueryRowContext(ctx, q).Scan(dst); err != nil {
			t.Fatal(q, err)
		}
	}
	if titles != s.Rows() || history != 3*30 || fts == 0 || triggers == 0 {
		t.Fatalf("titles %d (want %d) history %d fts %d triggers %d", titles, s.Rows(), history, fts, triggers)
	}
}

func TestMeasureSortTitleFold540k(t *testing.T) {
	if os.Getenv("ETHER_PERF_SORT_FOLD") == "" {
		t.Skip("set ETHER_PERF_SORT_FOLD=1 to measure 540k sort_title fold startup cost")
	}
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	genStart := time.Now()
	if err := Generate(ctx, db, LargeScale, time.Now()); err != nil {
		t.Fatal(err)
	}
	t.Logf("generated %d rows in %s", LargeScale.Rows(), time.Since(genStart).Round(time.Millisecond))

	// 1. Measure cold startup scan over all 540,000 rows (270 keyset pages of 2,000 rows).
	coldStart := time.Now()
	if _, err := catalog.New(db, nil, config.Catalog{}, nil); err != nil {
		t.Fatal(err)
	}
	coldScanDur := time.Since(coldStart)
	t.Logf("1. cold 540k scan (0 unfolded rows, 270 keyset pages of 2,000): %s", coldScanDur.Round(time.Millisecond))

	// 2. Measure warm startup (meta marker already set).
	warmStart := time.Now()
	if _, err := catalog.New(db, nil, config.Catalog{}, nil); err != nil {
		t.Fatal(err)
	}
	warmDur := time.Since(warmStart)
	t.Logf("2. warm 540k startup (sort_title_folded=1 marker present): %s", warmDur.Round(time.Microsecond))

	// 3. Seed unfolded accented titles across the 540k table (5,000 spread across
	// movie keys + a full 2,000-row contiguous page) with triggers suspended during
	// fixture mutation, then clear the meta marker to measure keyset-paged migration with writes.
	restore, err := suspendTriggers(ctx, db, "catalog_titles")
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= LargeScale.Movies; i += 50 {
		key := int64(core.MovieKey(int64(i)))
		title := fmt.Sprintf("Léon %d", i)
		rawSort := fmt.Sprintf("léon %d", i)
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET title=?, sort_title=? WHERE key=?`, title, rawSort, key); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 2000; i++ {
		key := int64(core.MovieKey(int64(i)))
		title := fmt.Sprintf("Été %d", i)
		rawSort := fmt.Sprintf("été %d", i)
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET title=?, sort_title=? WHERE key=?`, title, rawSort, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_meta WHERE k='sort_title_folded'`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}

	writeStart := time.Now()
	if _, err := catalog.New(db, nil, config.Catalog{}, nil); err != nil {
		t.Fatal(err)
	}
	writeDur := time.Since(writeStart)
	t.Logf("3. cold 540k scan + folding 6,960 accented rows across keyset batches: %s", writeDur.Round(time.Millisecond))
}

// TestGenerateMatchesLiveTriggers proves the fixture's rebuild of what its
// suspended triggers maintain (show dates and episode counts, genre pairs,
// person names, search indexes, library tallies) writes exactly what the
// triggers write when every row goes in with them on, as production does.
// A new trigger-kept table the rebuild misses fails it.
func TestGenerateMatchesLiveTriggers(t *testing.T) {
	ctx := context.Background()
	s := SmallScale.Divide(50)
	s.Viewers, s.HistoryPerViewer, s.InProgressPerViewer = 2, 30, 5
	now := time.Now()
	open := func(live bool) *sql.DB {
		db, err := store.Open(ctx, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
		if err := generate(ctx, db, s, now, live); err != nil {
			t.Fatal(err)
		}
		return db
	}
	rebuilt, live := open(false), open(true)
	tables := map[string]string{}
	rows, err := rebuilt.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite%' AND name NOT LIKE '%fts%'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = `SELECT * FROM "` + name + `"`
	}
	rows.Close()
	// The generation counts writes, and the cursor holds the migration's
	// clock.
	for _, skip := range []string{"viewer_policy_epoch", "catalog_show_added_cursor"} {
		delete(tables, skip)
	}
	// Live tallies sit in their delta tables until the catalogue folds
	// them; compare the sums a read takes.
	for name, dims := range map[string]string{"catalog_browse_tally": "type,genre,rating_rank", "catalog_stats_tally": "kind,browsable,home,year_pending", "catalog_facet_tally": "type,facet,value,rating_rank"} {
		delete(tables, name+"_delta")
		tables[name] = `SELECT ` + dims + `,SUM(n) FROM (SELECT ` + dims + `,n FROM ` + name + ` UNION ALL SELECT ` + dims + `,d FROM ` + name + `_delta) GROUP BY ` + dims + ` HAVING SUM(n)<>0`
	}
	// A series' rating that no episode carries any more keeps a zero row.
	tables["catalog_show_episode_ratings"] = `SELECT * FROM catalog_show_episode_ratings WHERE known<>0`
	// The search indexes hold the same terms.
	for name, q := range map[string]string{
		"catalog_titles_fts":       `SELECT rowid FROM catalog_titles_fts WHERE title LIKE '%pisode%'`,
		"catalog_alt_titles_fts":   `SELECT rowid FROM catalog_alt_titles_fts WHERE catalog_alt_titles_fts MATCH 'ren'`,
		"catalog_person_names_fts": `SELECT rowid FROM catalog_person_names_fts WHERE catalog_person_names_fts MATCH 'ka*'`,
		"catalog_studio_names_fts": `SELECT n.studio FROM catalog_studio_names_fts f JOIN catalog_studio_names n ON n.rowid=f.rowid WHERE f.studio LIKE '%ren%'`,
	} {
		tables[name] = q
		if _, err := rebuilt.ExecContext(ctx, `INSERT INTO `+name+`(`+name+`) VALUES('integrity-check')`); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	dump := func(db *sql.DB, q string) []string {
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(q, err)
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		var out []string
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprint(vals...))
		}
		slices.Sort(out)
		return out
	}
	nonEmpty := 0
	for name, q := range tables {
		a, b := dump(rebuilt, q), dump(live, q)
		if len(a) > 0 {
			nonEmpty++
		}
		if !slices.Equal(a, b) {
			t.Errorf("%s: rebuilt %d rows, live triggers %d rows, or contents differ", name, len(a), len(b))
		}
	}
	for _, name := range []string{"catalog_genre_pairs", "catalog_person_names", "catalog_credits", "catalog_crowd", "catalog_collections", "state_history_events"} {
		if len(dump(rebuilt, tables[name])) == 0 {
			t.Errorf("%s is empty", name)
		}
	}
	t.Logf("compared %d tables, %d with rows", len(tables), nonEmpty)
}

// TestGenerateIgnoresWhenItRuns: a scale is one library whenever it is
// generated. Only its dates follow the generation time; its titles' shapes
// and credits must not, or the gate's page bounds (DetailCaps) and every
// row it compares move from run to run.
func TestGenerateIgnoresWhenItRuns(t *testing.T) {
	ctx := context.Background()
	shape := func(now time.Time) string {
		db, err := store.Open(ctx, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		s := SmallScale.Divide(50)
		s.Viewers, s.HistoryPerViewer = 2, 30
		if err := Generate(ctx, db, s, now); err != nil {
			t.Fatal(err)
		}
		var out string
		if err := db.QueryRowContext(ctx, `SELECT COALESCE(group_concat(r, ';'), '') FROM (
 SELECT key || ':' || title || ':' || vote_count || ':' || popularity AS r FROM catalog_titles
 UNION ALL SELECT key || '/' || kind || '/' || ord || '/' || person_tmdb FROM catalog_credits
 ORDER BY 1)`).Scan(&out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	a := shape(time.Date(2026, 11, 8, 23, 10, 0, 0, time.UTC))
	b := shape(time.Date(2026, 12, 19, 4, 32, 17, 0, time.UTC))
	if a == "" || a != b {
		t.Fatalf("the same scale generated at two times built different libraries (%d vs %d bytes of shape)", len(a), len(b))
	}
}
