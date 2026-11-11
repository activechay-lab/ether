package catalog

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// A rule limited to browsing keeps a title out of browse, rows, lists and
// recommendations (FilterVisibility without a search context), but explicit
// search and direct lookup still find it. A rule scoped everywhere hides it
// from both.
func TestVisibilityScopeDiscoveryOnlyKeepsSearchAndLookup(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	released := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	foreign := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "Parasite Night", Year: 2020, Released: released, Countries: []string{"KR"}, Popularity: 50}
	local := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Parasite Day", Year: 2020, Released: released, Countries: []string{"US"}, Popularity: 40}
	for _, tt := range []core.Title{foreign, local} {
		if err := c.UpsertTitle(ctx, tt, true); err != nil {
			t.Fatal(err)
		}
	}
	onlyUS := func(scope string) []core.LibraryExclusion {
		return []core.LibraryExclusion{{
			Action: "hide", MediaType: "mixed", Match: "all", Scope: scope,
			Rules: []core.CollectionRule{{Field: "country", Op: "notIn", Value: json.RawMessage(`["US"]`)}},
		}}
	}
	has := func(ts []core.Title, k core.RatingKey) bool {
		for _, x := range ts {
			if x.Key == k {
				return true
			}
		}
		return false
	}
	search := core.WithExplicitSearch(ctx)

	for _, tc := range []struct {
		scope      string
		searchable bool
	}{
		{core.VisibilityDiscovery, true},
		{core.VisibilityEverywhere, false},
		{"", false}, // saved rules without a scope keep hiding everywhere
	} {
		setTestPolicies(t, c, onlyUS(tc.scope), "")

		// Discovery: library browse, hub rows, New Releases and Trending
		// (browse SQL), recommendations (FilterVisibility), Visible.
		page, err := c.Browse(ctx, core.BrowseQuery{})
		if err != nil {
			t.Fatal(err)
		}
		if has(page.Items, foreign.Key) || !has(page.Items, local.Key) {
			t.Fatalf("scope %q: browse = %+v, want only the US title", tc.scope, page.Items)
		}
		if got := c.FilterVisibility(ctx, []core.Title{foreign, local}); has(got, foreign.Key) || !has(got, local.Key) {
			t.Fatalf("scope %q: recommendations kept %+v", tc.scope, got)
		}
		if c.Visible(&foreign) {
			t.Fatalf("scope %q: discovery Visible shows the foreign title", tc.scope)
		}
		// Even a search context cannot widen library browsing.
		if page, err = c.Browse(search, core.BrowseQuery{}); err != nil || has(page.Items, foreign.Key) {
			t.Fatalf("scope %q: browse under a search context = %+v, %v", tc.scope, page.Items, err)
		}

		// Search: the catalogue search, the shared FilterVisibility pass and
		// the people search check.
		got, err := c.Search(search, "Parasite", nil, 10)
		if err != nil {
			t.Fatal(err)
		}
		if has(got, foreign.Key) != tc.searchable || !has(got, local.Key) {
			t.Fatalf("scope %q: search = %+v, want foreign found=%v", tc.scope, got, tc.searchable)
		}
		if f := c.FilterVisibility(search, []core.Title{foreign}); has(f, foreign.Key) != tc.searchable {
			t.Fatalf("scope %q: search filter kept %+v", tc.scope, f)
		}
		if c.SearchVisible(&foreign) != tc.searchable {
			t.Fatalf("scope %q: SearchVisible = %v", tc.scope, !tc.searchable)
		}
		// Without the search mark, a search is discovery and hides it.
		if got, err = c.Search(ctx, "Parasite", nil, 10); err != nil || has(got, foreign.Key) {
			t.Fatalf("scope %q: unmarked search = %+v, %v", tc.scope, got, err)
		}

		// Direct lookup by key (detail page, playback) is never filtered.
		if title, err := c.Get(ctx, foreign.Key); err != nil || title == nil || title.Key != foreign.Key {
			t.Fatalf("scope %q: Get = %v, %v", tc.scope, title, err)
		}
	}
}

// The "everything else" default applies to the catalogue as it does to the
// preview, people pages and collections.
func TestVisibilityDefaultHideAppliesToBrowse(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	released := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for i, country := range []string{"US", "FR"} {
		if err := c.UpsertTitle(ctx, core.Title{Key: core.MovieKey(int64(i + 1)), Type: core.TypeMovie, Title: "Sample", Year: 2020, Released: released, Countries: []string{country}}, true); err != nil {
			t.Fatal(err)
		}
	}
	setTestPolicies(t, c, []core.LibraryExclusion{{
		Action: "show", MediaType: "mixed", Match: "all",
		Rules: []core.CollectionRule{{Field: "country", Op: "in", Value: json.RawMessage(`["US"]`)}},
	}}, "hide")
	page, err := c.Browse(ctx, core.BrowseQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Key != core.MovieKey(1) {
		t.Fatalf("browse = %+v, want only the US title", page.Items)
	}
}
