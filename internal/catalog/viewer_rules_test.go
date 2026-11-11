package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// One invariant across SQL pagination, compiled discovery/search policy, restart,
// global edits and viewer cache revisions: a viewer override never changes peers.
func TestViewerRulesIsolationAndScopes(t *testing.T) {
	db := store.OpenTest(t)
	rule := core.LibraryExclusion{Name: "French", MediaType: "movie", Match: "all", Scope: core.VisibilityDiscovery, Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"fr"`)}}}
	cfg := config.Catalog{Exclusions: []core.LibraryExclusion{rule}}
	c, err := New(db, nil, cfg, nil, WithLibraryPolicies(testPolicies(cfg.Exclusions, cfg.VisibilityDefault)))
	if err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "French movie", Language: "fr", Released: time.Now().Add(-24 * time.Hour)}
	if err := c.upsert(t.Context(), title, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	a, b := &core.User{ID: 1}, &core.User{ID: 2}
	check := func(wantA, wantB int) {
		t.Helper()
		for i, u := range []*core.User{a, b} {
			want := []int{wantA, wantB}[i]
			ctx := core.WithCatalogViewer(t.Context(), u)
			page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 1})
			if err != nil || page.Total != want || len(page.Items) != want {
				t.Fatalf("viewer %d: %+v %v want %d", u.ID, page, err, want)
			}
			if got := len(c.FilterVisibility(ctx, []core.Title{title})); got != want {
				t.Fatalf("viewer %d discovery=%d want %d", u.ID, got, want)
			}
			if !c.VisibleForViewer(u, &title) {
				t.Fatal("browsing-only rule blocked lookup")
			}
		}
	}
	check(0, 0)
	candidates, err := c.List(core.WithCatalogCandidates(t.Context()), "popular", core.TypeMovie, 10)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("shared candidates lost globally hidden titles: %v %v", candidates, err)
	}
	ordinary, err := c.List(t.Context(), "popular", core.TypeMovie, 10)
	if err != nil || len(ordinary) != 0 {
		t.Fatalf("materialization polluted global list cache: %v %v", ordinary, err)
	}
	if err := c.PutViewerRules(t.Context(), a.ID, ViewerRules{Mode: "exempt", Exemptions: []string{RuleIdentity(rule)}}); err != nil {
		t.Fatal(err)
	}
	check(1, 0)
	if c.ViewerRulesRevision(a) != 1 || c.ViewerRulesRevision(b) != 0 {
		t.Fatal("peer revision changed")
	}
	// Reordering unrelated global rules cannot move an exemption to another rule.
	cfg.Exclusions = append([]core.LibraryExclusion{{Name: "English", MediaType: "movie", Match: "all", Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"en"`)}}}}, rule)
	setTestPolicies(t, c, cfg.Exclusions, cfg.VisibilityDefault)
	check(1, 0)
	if err := c.PutViewerRules(t.Context(), a.ID, ViewerRules{Mode: "custom", VisibilityDefault: "show"}); err != nil {
		t.Fatal(err)
	}
	c, err = New(db, nil, cfg, nil, WithLibraryPolicies(testPolicies(cfg.Exclusions, cfg.VisibilityDefault)))
	if err != nil {
		t.Fatal(err)
	}
	check(1, 0)
	if err := c.PutViewerRules(t.Context(), a.ID, ViewerRules{Mode: "inherit"}); err != nil {
		t.Fatal(err)
	}
	check(0, 0)
	if c.ViewerRulesRevision(a) != 3 || c.ViewerRulesRevision(b) != 0 {
		t.Fatal("revision isolation lost after reset")
	}
}
