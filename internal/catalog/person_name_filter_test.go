package catalog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestExactCreditNameFilter(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c, err := New(db, nil, config.Catalog{}, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	for i, title := range []core.Title{
		{Title: "One", Cast: []core.Person{{TMDB: 11, Name: "Zoë North"}}, Directors: []core.Person{{TMDB: 22, Name: "Pat West"}}},
		{Title: "Two", Cast: []core.Person{{TMDB: 22, Name: "Pat West"}}},
		{Title: "Three", Cast: []core.Person{{TMDB: 33, Name: "Zoë North"}, {TMDB: 22, Name: "Pat West"}}},
	} {
		title.Key = core.MovieKey(int64(i + 1))
		title.IDs.TMDB = int64(i + 1)
		title.Type = core.TypeMovie
		title.Released = now.AddDate(-1, 0, 0)
		if err := c.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		filters []core.CreditFilter
		want    int
	}{
		{"ASCII folding", []core.CreditFilter{{Names: []string{"ZOë NORTH"}, Kind: "cast"}}, 2},
		{"nonASCII exact", []core.CreditFilter{{Names: []string{"ZOË NORTH"}, Kind: "cast"}}, 0},
		{"unknown", []core.CreditFilter{{Names: []string{"Missing"}, Kind: "cast"}}, 0},
		{"unknown negative", []core.CreditFilter{{Names: []string{"Missing"}, Kind: "cast", Exclude: true}}, 3},
		{"kind", []core.CreditFilter{{Names: []string{"pat west"}, Kind: "director"}}, 1},
		{"alternatives", []core.CreditFilter{{Names: []string{"Missing", "pat west"}, Kind: "cast"}}, 2},
		{"mixed alternatives", []core.CreditFilter{{Names: []string{"pat west"}, People: []int64{11}, Kind: "cast"}}, 3},
		{"repeated conjunction", []core.CreditFilter{{Names: []string{"zoë north"}, Kind: "cast"}, {Names: []string{"pat west"}, Kind: "cast"}}, 1},
		{"negative", []core.CreditFilter{{Names: []string{"zoë north"}, Kind: "cast", Exclude: true}}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, Credits: tc.filters}, Limit: 20})
			if err != nil {
				t.Fatal(err)
			}
			if p.Total != tc.want {
				t.Fatalf("total=%d want=%d", p.Total, tc.want)
			}
		})
	}
	// The behaviour assertions above apply to both engines; the plan syntax below is SQLite-only.
	if store.DialectOf(db) != store.SQLite {
		return
	}
	rows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT key FROM catalog_credits WHERE person_tmdb IN (SELECT person_tmdb FROM catalog_person_names WHERE LOWER(name)=?) AND kind='cast'`, "zoë north")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if !strings.Contains(plan, "catalog_person_names_exact") || !strings.Contains(plan, "catalog_credits_person_kind") {
		t.Fatalf("name lookup lacks exact index: %s", plan)
	}
}
