package safety

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestCatalogueOverridesNeverWeakenSafety(t *testing.T) {
	db := store.OpenTest(t)
	c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithLibraryPolicies(catalog.LibraryPolicies{MovieDefault: "hide", ShowDefault: "hide"}))
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}
	s.SetCatalogPolicy(c.VisibleForViewer, c.DiscoveryVisibleForViewer)
	u := &core.User{ID: 1, MaxContentRating: "PG"}
	title := &core.Title{Type: core.TypeMovie, ContentRating: "PG", Released: time.Now().Add(-24 * time.Hour)}
	if s.Allowed(u, title) {
		t.Fatal("library default hide bypassed")
	}
	if err := c.PutViewerRules(t.Context(), u.ID, catalog.ViewerRules{Mode: "custom", VisibilityDefault: "show"}); err != nil {
		t.Fatal(err)
	}
	if !s.Allowed(u, title) {
		t.Fatal("viewer override not selected")
	}
	title.ContentRating = "R"
	if s.Allowed(u, title) || s.DiscoveryAllowed(u, title) {
		t.Fatal("catalogue override weakened rating cap")
	}
	title.ContentRating = "PG"
	title.Adult = true
	if s.Allowed(u, title) {
		t.Fatal("catalogue override revealed adult title")
	}
}
