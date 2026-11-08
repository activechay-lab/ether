package catalog

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// The rank copies must always hold exactly the stored movies and shows,
// with their browse columns: through inserts, changes, deletes and a
// rebuild, on both copies (the live one and the one the next rebuild
// replaces). A popularity change reaches the rank only at a rebuild.
func TestRankTracksCatalogue(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	rel := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var titles []core.Title
	for i := int64(1); i <= 40; i++ {
		titles = append(titles, core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, Title: fmt.Sprint("M", i), Released: rel, Popularity: float64(i), Language: "en"})
	}
	titles = append(titles, core.Title{Key: core.ShowKey(7), Type: core.TypeShow, Title: "S", Released: rel, Popularity: 3})
	if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	// A season is not ranked.
	if err := c.upsertMany(ctx, []core.Title{{Key: core.SeasonKey(7, 1), Type: core.TypeSeason, ShowKey: core.ShowKey(7), Season: 1, Title: "Season 1"}}, upsertOpts{}); err != nil {
		t.Fatal(err)
	}
	check := func(label string) {
		t.Helper()
		for _, side := range []string{"a", "b"} {
			var missing, extra, differ int
			q := `SELECT
 (SELECT COUNT(*) FROM catalog_titles t WHERE t.type IN ('movie','show') AND NOT EXISTS (SELECT 1 FROM catalog_rank_` + side + ` r WHERE r.key=t.key)),
 (SELECT COUNT(*) FROM catalog_rank_` + side + ` r WHERE NOT EXISTS (SELECT 1 FROM catalog_titles t WHERE t.key=r.key AND t.type IN ('movie','show'))),
 (SELECT COUNT(*) FROM catalog_rank_` + side + ` r JOIN catalog_titles t ON t.key=r.key WHERE r.type<>t.type OR r.browsable<>t.browsable OR r.adult<>t.adult OR r.released_ts<>t.released_ts OR r.year<>t.year OR r.language<>t.language OR r.status<>t.status)`
			if err := c.db.QueryRowContext(ctx, q).Scan(&missing, &extra, &differ); err != nil {
				t.Fatal(err)
			}
			if missing+extra+differ != 0 {
				t.Fatalf("%s: copy %s: %d missing, %d extra, %d differ", label, side, missing, extra, differ)
			}
		}
	}
	order := func() []int64 {
		t.Helper()
		rows, err := c.db.QueryContext(ctx, `SELECT key FROM catalog_rank WHERE type='movie' AND browsable=1 AND adult=0 ORDER BY rk, key LIMIT 3`)
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
	check("inserted")
	if got := order(); len(got) != 3 || got[0] != int64(core.MovieKey(40)) {
		t.Fatalf("new titles rank by popularity at once: %v", got)
	}
	// A popularity change waits for the rebuild; a browse change does not.
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET popularity=1000 WHERE key=?`, int64(core.MovieKey(1))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET year=1999, language='fr', browsable=0 WHERE key=?`, int64(core.MovieKey(2))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM catalog_titles WHERE key=?`, int64(core.MovieKey(3))); err != nil {
		t.Fatal(err)
	}
	check("changed")
	if got := order(); got[0] != int64(core.MovieKey(40)) {
		t.Fatalf("popularity alone moved the rank before a rebuild: %v", got)
	}
	for range 2 {
		if _, err := c.RebuildRank(ctx); err != nil {
			t.Fatal(err)
		}
		check("rebuilt")
		if got := order(); got[0] != int64(core.MovieKey(1)) {
			t.Fatalf("rebuild did not refresh popularity: %v", got)
		}
	}
	if !c.rankReady.Load() {
		t.Fatal("rank not marked ready")
	}
}
