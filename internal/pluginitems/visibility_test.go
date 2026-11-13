package pluginitems

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/plugins"
)

// Ordered policy, indexed facet and preview must all describe the same set.
func TestPluginVisibilityAndEnvelopeFacets(t *testing.T) {
	ctx := context.Background()
	s, _ := openTestService(t, Options{})
	decl := sampleBookDecl()
	decl.Fields = append(decl.Fields, plugins.KindField{Key: "studio", Type: "string", Label: "Studio", Indexed: true})
	if _, err := s.UpsertKind(ctx, "community.books", 10, decl, false); err != nil {
		t.Fatal(err)
	}
	items := []core.KindItem{
		{ID: "a", Level: "series", Title: "Alpha", Year: 1990, Rating: 8.5, Released: 100, Fields: map[string]json.RawMessage{"tags": json.RawMessage(`["fantasy"]`), "studio": json.RawMessage(`"Acme"`)}},
		{ID: "b", Level: "series", Title: "Beta", Year: 2020, Rating: 9.5, Released: 200, Fields: map[string]json.RawMessage{"tags": json.RawMessage(`["scifi"]`)}},
	}
	if _, err := s.UpsertItems(ctx, "community.books", "book", items); err != nil {
		t.Fatal(err)
	}
	specs := []core.LibraryExclusion{
		{Name: "Keep fantasy", Action: "show", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["fantasy"]`)}, {Field: "studio", Op: "is", Value: json.RawMessage(`"Acme"`)}}},
		{Name: "Hide high ratings", Action: "hide", Scope: "discovery", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "rating", Op: "gte", Value: json.RawMessage(`8`)}}},
	}
	policy, err := libraryfilter.CompilePolicy(specs, "hide")
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Level: "series", Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != "a" {
		t.Fatalf("ordered policy: %+v", page)
	}
	hidden, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Level: "series", Policy: policy, Hidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Total != 1 || hidden.Items[0].ID != "b" {
		t.Fatalf("hidden: %+v", hidden)
	}
	for _, field := range []string{"year", "released", "rating"} {
		facets, err := s.Facets(ctx, 10, "series", field, 10)
		if err != nil || len(facets) != 2 {
			t.Fatalf("%s facets: %+v %v", field, facets, err)
		}
		filtered, err := s.Browse(ctx, BrowseQuery{LibraryID: 10, Level: "series", Facets: map[string][]string{field: {facets[0].Value}}})
		if err != nil || filtered.Total != 1 {
			t.Fatalf("%s filter: %+v %v", field, filtered, err)
		}
	}
	preview, err := s.PreviewVisibility(ctx, 10, specs, "hide")
	if err != nil {
		t.Fatal(err)
	}
	p := preview.(map[string]any)
	if p["hidden"] != 1 || p["visible"] != 1 {
		t.Fatalf("preview: %+v", p)
	}
	// Dropping discovery-only rules still preserves the hide fallback.
	search, err := s.Search(ctx, SearchQuery{LibraryID: 10, Query: "Beta", Policy: policy.ForSearch()})
	if err != nil || search.Total != 0 {
		t.Fatalf("search: %+v %v", search, err)
	}
	searchPolicy, err := libraryfilter.CompilePolicy(specs, "show")
	if err != nil {
		t.Fatal(err)
	}
	search, err = s.Search(ctx, SearchQuery{LibraryID: 10, Query: "Beta", Policy: searchPolicy.ForSearch()})
	if err != nil || search.Total != 1 {
		t.Fatalf("discovery search: %+v %v", search, err)
	}
	lib := core.Library{ID: 10, Rules: specs, VisibilityDefault: "show"}
	hiddenItem, err := s.ItemHidden(core.WithExplicitSearch(ctx), lib, core.NewItemRef("community.books", "book", "b"))
	if err != nil || hiddenItem {
		t.Fatalf("discovery detail: %v %v", hiddenItem, err)
	}

}
