package collections

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

// Earlier versions created genre/network collections on install. They stay as
// the owner left them, and the matching bundle skips them instead of adding
// duplicates; the library templates still use the facet index.
func TestEarlierDefaultCollectionsAreKeptAndAdopted(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(db, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	legacy := map[string]string{"ether-default:movie.genre.action": "Action", "ether-default:show.studio.hbo": "HBO"}
	for tid, name := range legacy {
		sp := facetTpl(name, MediaMovie, "genre", "Action")
		sp.Name, sp.TemplateID = name, tid
		if _, err := s.CreateSpec(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM collections_meta WHERE k='library-defaults-templates-v1'`); err != nil {
		t.Fatal(err)
	}
	if s, err = New(db, cat, nil); err != nil {
		t.Fatal(err)
	}
	specs, err := s.ListSpecs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	adopted := map[string]string{}
	for _, sp := range specs {
		if !sp.BuiltIn {
			adopted[sp.Name] = sp.TemplateID
		}
	}
	if len(adopted) != 2 || adopted["Action"] != "genre-movie-action" || adopted["HBO"] != "network-hbo" {
		t.Fatalf("adopted: %v", adopted)
	}
	for bundle, skipped := range map[string]int{"movie-genres": 1, "tv-networks": 1} {
		p, err := PreviewTemplateBundle(Templates(), specs, bundle)
		if err != nil || p.WillSkip != skipped || p.WillCreate == 0 {
			t.Fatalf("%s: %+v %v", bundle, p, err)
		}
	}
	for _, d := range libraryTemplateDefs {
		if _, ok := localFacetSpec(d.spec); d.spec.Rules[0].Op == "is" && !ok {
			t.Fatalf("%s misses the facet index", d.id)
		}
	}
}
