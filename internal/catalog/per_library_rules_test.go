package catalog

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPerLibraryPoliciesScopedPreviewAndLegacyExemptions(t *testing.T) {
	db := store.OpenTest(t)
	ctx := t.Context()

	// Legacy mixed rule migrated onto Movies (mediaType="movie") and TV Shows (mediaType="show").
	legacyMixed := core.LibraryExclusion{
		Name: "No French", MediaType: "mixed", Match: "all",
		Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"fr"`)}},
	}
	pol := testPolicies([]core.LibraryExclusion{legacyMixed}, "")
	c, err := New(db, nil, config.Catalog{}, nil, WithLibraryPolicies(pol))
	if err != nil {
		t.Fatal(err)
	}

	rel := time.Now().Add(-48 * time.Hour)
	for _, title := range []core.Title{
		{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "French Comedy", Language: "fr", Genres: []string{"Comedy"}, Released: rel},
		{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "US Comedy", Language: "en", Genres: []string{"Comedy"}, Released: rel},
		{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "JP Comedy", Language: "ja", Genres: []string{"Comedy"}, Released: rel},
		{Key: core.ShowKey(10), Type: core.TypeShow, Title: "French Show", Language: "fr", Genres: []string{"Drama"}, Released: rel},
	} {
		if err := c.upsert(ctx, title, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
	}

	// Legacy viewer exemption recorded against the pre-migration "mixed" rule identity
	// still exempts both canonical Movies and TV Shows rules.
	v := &core.User{ID: 7}
	if err := c.PutViewerRules(ctx, v.ID, ViewerRules{
		Mode:       "exempt",
		Exemptions: []string{RuleIdentity(legacyMixed)},
	}); err != nil {
		t.Fatal(err)
	}
	vCtx := core.WithCatalogViewer(ctx, v)
	mp, err := c.Browse(vCtx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil || mp.Total != 3 {
		t.Fatalf("exempt viewer movies total=%d err=%v, want 3", mp.Total, err)
	}
	sp, err := c.Browse(vCtx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeShow}, Limit: 10})
	if err != nil || sp.Total != 1 {
		t.Fatalf("exempt viewer shows total=%d err=%v, want 1", sp.Total, err)
	}

	// Scoped preview on a saved-filter library (Comedy movies) applies canonical Movies
	// rules (hiding French Comedy -> 2 candidates) and evaluates extra filter-library rules.
	extraRule := []core.LibraryExclusion{{
		Name: "No JP in Standup", MediaType: "movie", Match: "all",
		Rules: []core.CollectionRule{{Field: "language", Op: "is", Value: json.RawMessage(`"ja"`)}},
	}}
	comedyGroup := &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{{Operator: "and", Filter: &core.Filter{Genres: []string{"Comedy"}}}}}
	filterScope := core.Filter{
		Type:                    core.TypeMovie,
		LocalOnly:               true,
		ApplyCanonicalInPreview: true,
		Group:                   comedyGroup,
	}
	rawPrev, err := c.PreviewVisibilityScoped(ctx, extraRule, "", &filterScope)
	if err != nil {
		t.Fatal(err)
	}
	prev := rawPrev.(VisibilityPreview)
	if prev.Total != 2 || prev.Hidden != 1 || prev.Visible != 1 {
		t.Fatalf("scoped filter preview = %+v, want Total=2 Hidden=1 Visible=1", prev)
	}

	// Normal browsing of that filter library with ExtraRules matches Visible=1.
	normalPage, err := c.Browse(ctx, core.BrowseQuery{
		Filter: core.Filter{
			Type:       core.TypeMovie,
			LocalOnly:  true,
			Group:      comedyGroup,
			ExtraRules: extraRule,
		},
		Limit: 10,
	})
	if err != nil || normalPage.Total != 1 || normalPage.Items[0].Key != core.MovieKey(2) {
		t.Fatalf("filter library browse with ExtraRules = %+v, %v", normalPage, err)
	}
}
