package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// A studio filter read through the vocabulary returns exactly the titles a
// studio of which matches the value, for every operator, through studio
// writes, case differences, accents, short values and LIKE metacharacters.
func TestStudioFiltersMatchTheirNames(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	c, err := New(db, nil, config.Catalog{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	names := []string{"Universal Pictures", "universal", "Focus Features", "Studio Ghibli", "Ghibli 100%", "Éclair Films", "A_B Co", "BBC", ""}
	studios := map[core.RatingKey][]string{}
	for i := int64(1); i <= 60; i++ {
		key := core.MovieKey(i)
		exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,detail) VALUES(?,?,?,?,?,1,1)`, int64(key), "movie", fmt.Sprint("t", i), fmt.Sprint("t", i), time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC).Unix())
		for j, n := range names {
			if (int(i)+j)%4 == 0 {
				exec(`INSERT INTO catalog_studios(key,studio) VALUES(?,?)`, int64(key), n)
				studios[key] = append(studios[key], n)
			}
		}
	}
	// Writes the vocabulary follows: a rename, a removal, a new name.
	exec(`UPDATE catalog_studios SET studio='Focus Pictures' WHERE studio='Focus Features' AND key=?`, int64(core.MovieKey(1)))
	exec(`DELETE FROM catalog_studios WHERE studio='BBC'`)
	exec(`INSERT INTO catalog_studios(key,studio) VALUES(?,?)`, int64(core.MovieKey(2)), "Pixar")
	studios = map[core.RatingKey][]string{}
	rows, err := db.QueryContext(ctx, `SELECT key, studio FROM catalog_studios`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k int64
		var n string
		if err := rows.Scan(&k, &n); err != nil {
			t.Fatal(err)
		}
		studios[core.RatingKey(k)] = append(studios[core.RatingKey(k)], n)
	}
	rows.Close()
	lower := func(s string) string { // SQLite's LOWER folds ASCII only
		return strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' {
				return r + 32
			}
			return r
		}, s)
	}
	if store.DialectOf(db) == store.Postgres {
		lower = strings.ToLower
	}
	// The filter lower-cases its value in Go and compares it with LOWER of
	// the name: equality as is, LIKE folding ASCII case on SQLite.
	like := func(s string) string { return lower(s) }
	match := map[string]func(name, v string) bool{
		"=":  func(n, v string) bool { return strings.Contains(like(lower(n)), like(v)) },
		"==": func(n, v string) bool { return lower(n) == v },
		"<=": func(n, v string) bool { return strings.HasPrefix(like(lower(n)), like(v)) },
		">=": func(n, v string) bool { return strings.HasSuffix(like(lower(n)), like(v)) },
	}
	for _, v := range []string{"universal", "Universal Pictures", "ghibli", "100%", "a_b", "éclair", "Éclair", "bb", "pic", "focus pictures", "pixar", "zz"} {
		for op, ok := range match {
			page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, StudioFilters: []core.StringFilter{{Op: op, Value: v}}}, Sort: core.SortTitle, Limit: 500})
			if err != nil {
				t.Fatal(err)
			}
			var got, want []int64
			for _, it := range page.Items {
				got = append(got, int64(it.Key))
			}
			for k, ns := range studios {
				if slices.ContainsFunc(ns, func(n string) bool { return ok(n, strings.ToLower(v)) }) {
					want = append(want, int64(k))
				}
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) || page.Total != len(want) {
				t.Errorf("studio%s%q: got %v (total %d), want %v", op, v, got, page.Total, want)
			}
		}
	}
}
