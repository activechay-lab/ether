package catalog

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
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
	// Writes while a copy is being rebuilt land in it before the swap.
	rankBuildHook = func() {
		if err := c.upsertMany(ctx, []core.Title{{Key: core.MovieKey(900), Type: core.TypeMovie, Title: "During", Released: rel, Popularity: 2000}}, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET language='de' WHERE key=?`, int64(core.MovieKey(4))); err != nil {
			t.Fatal(err)
		}
		if _, err := c.db.ExecContext(ctx, `DELETE FROM catalog_titles WHERE key=?`, int64(core.MovieKey(5))); err != nil {
			t.Fatal(err)
		}
	}
	defer func() { rankBuildHook = nil }()
	for i := range 2 {
		if _, err := c.RebuildRank(ctx); err != nil {
			t.Fatal(err)
		}
		rankBuildHook = nil
		check("rebuilt")
		if i == 0 {
			if got := order(); got[0] != int64(core.MovieKey(900)) {
				t.Fatalf("a title stored during the rebuild is missing from the new copy: %v", got)
			}
			continue
		}
		if got := order(); got[0] != int64(core.MovieKey(900)) || got[1] != int64(core.MovieKey(1)) {
			t.Fatalf("rebuild did not refresh popularity: %v", got)
		}
	}
	if !c.rankReady.Load() {
		t.Fatal("rank not marked ready")
	}
}

// A popularity page read off the rank is the page the title indexes
// give: same titles, same order, every page, both directions, with and
// without a genre filter, ties included.
func TestRankPagesMatchTitleOrder(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	rel := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var titles []core.Title
	for i := int64(1); i <= 300; i++ {
		g := []string{"Drama"}
		if i%3 == 0 {
			g = []string{"Comedy"}
		}
		titles = append(titles, core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, Title: fmt.Sprint("M", i), Released: rel,
			Popularity: float64((i * 7919) % 97), Genres: g, Language: "en"})
	}
	if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RebuildRank(ctx); err != nil {
		t.Fatal(err)
	}
	for _, desc := range []bool{true, false} {
		for _, genre := range []string{"", "Comedy"} {
			for offset := 0; offset < 300; offset += 40 {
				q := core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Sort: core.SortPopularity, Desc: desc, Offset: offset, Limit: 40, NoTotal: true}
				if genre != "" {
					q.Filter.Genres = []string{genre}
				}
				pageKeys := func(rank bool) []core.RatingKey {
					c.rankReady.Store(rank)
					c.writeGen.Add(1) // no cached page
					sctx, stats := store.WithQueryStats(ctx)
					p, err := c.Browse(sctx, q)
					if err != nil {
						t.Fatal(err)
					}
					used := false
					for _, st := range stats.Statements() {
						used = used || strings.Contains(st.SQL, "catalog_rank")
					}
					if used != rank {
						t.Fatalf("rank=%t but the page query used the rank: %t", rank, used)
					}
					keys := make([]core.RatingKey, len(p.Items))
					for i, it := range p.Items {
						keys[i] = it.Key
					}
					return keys
				}
				want, got := pageKeys(false), pageKeys(true)
				if fmt.Sprint(want) != fmt.Sprint(got) {
					t.Fatalf("desc=%t genre=%q offset=%d:\n titles %v\n rank   %v", desc, genre, offset, want, got)
				}
			}
		}
	}
}

// Replacing the popularity-bearing title indexes must leave every sorted
// page and the console cursor pages as they were, and every query that
// named an old index must name its replacement or none.
func TestTitleIndexSwapKeepsPages(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	rel := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	var titles []core.Title
	for i := int64(1); i <= 120; i++ {
		titles = append(titles, core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, Title: fmt.Sprint("M", i), Released: rel.AddDate(-int(i%30), 0, 0),
			Popularity: float64((i * 31) % 53), Runtime: time.Duration(60+i%40) * time.Minute, Language: []string{"en", "fr"}[i%2], Genres: []string{"Drama"}})
	}
	if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	queries := []core.BrowseQuery{
		{Filter: core.Filter{Type: core.TypeMovie}, Sort: core.SortPopularity, Desc: true, Limit: 25},
		{Filter: core.Filter{Type: core.TypeMovie}, Sort: core.SortYear, Desc: true, Limit: 25},
		{Filter: core.Filter{Type: core.TypeMovie}, Sort: core.SortDuration, Limit: 25},
		{Filter: core.Filter{Type: core.TypeMovie, Languages: []string{"fr"}}, Sort: core.SortPopularity, Desc: true, Limit: 25},
		{Filter: core.Filter{Type: core.TypeMovie, LocalOnly: true}, Sort: core.SortPopularity, Desc: true, Limit: 25, NoTotal: true},
	}
	pages := func() string {
		c.writeGen.Add(1)
		var out []string
		for _, q := range queries {
			p, err := c.Browse(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			for _, it := range p.Items {
				keys = append(keys, fmt.Sprint(it.Key))
			}
			out = append(out, strings.Join(keys, ","))
		}
		// The console cursor: two pages by popularity, the second after
		// the first's last key.
		cq := core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, LocalOnly: true}, Sort: core.SortPopularity, Desc: true, Limit: 30}
		first, err := c.consoleCursorPage(ctx, cq)
		if err != nil {
			t.Fatal(err)
		}
		cq.AfterKey = first.Items[len(first.Items)-1].Key
		second, err := c.consoleCursorPage(ctx, cq)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []core.Page{first, second} {
			var keys []string
			for _, it := range p.Items {
				keys = append(keys, fmt.Sprint(it.Key))
			}
			out = append(out, strings.Join(keys, ","))
		}
		if _, err := c.WarmDetails(ctx, 5); err != nil {
			t.Fatal(err)
		}
		return strings.Join(out, "\n")
	}
	before := pages()
	if _, err := c.RebuildRank(ctx); err != nil {
		t.Fatal(err)
	}
	ranked := pages()
	if err := c.swapTitleIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	swapped := pages()
	if before != ranked || before != swapped {
		t.Fatalf("pages changed:\nbefore\n%s\nranked\n%s\nswapped\n%s", before, ranked, swapped)
	}
}
