package adapters_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/stremio"
)

// installCatalogAddon installs a plain Stremio add-on with a movie catalog
// (genre options, no paging) and a series catalog that pages with skip.
func installCatalogAddon(t *testing.T, h *listTestHarness) plugins.Record {
	t.Helper()
	raw := json.RawMessage(`{
		"id": "org.example.catalogs", "name": "Example catalogs", "version": "1.0.0",
		"resources": ["catalog"], "types": ["movie", "series"],
		"catalogs": [
			{"type": "movie", "id": "top", "name": "Popular Movies", "extra": [{"name": "genre", "options": ["Action", "Sci-Fi"]}]},
			{"type": "series", "id": "top", "name": "Popular Series", "extra": [{"name": "skip"}]}
		]
	}`)
	ctx := context.Background()
	preview, err := h.mgr.PreviewInstall(ctx, plugins.Source{Kind: "url", URL: "https://addon.example/secret-token/manifest.json", Manifest: raw})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := h.mgr.Install(ctx, preview, plugins.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func addonCatalogKind(rec plugins.Record, typ, id string) string {
	return extend.PluginID(rec.ID, plugins.StremioCatalogKey(stremio.Catalog{Type: typ, ID: id}))
}

func TestAddonCatalogMatchesByIDAndCountsUnmatched(t *testing.T) {
	h := newListTestHarness(t)
	ctx := context.Background()
	rec := installCatalogAddon(t, h)
	movies := addonCatalogKind(rec, "movie", "top")
	if _, ok := h.cols.KindRegistry().Lookup(movies); !ok {
		t.Fatalf("add-on catalog %s is not a collection source", movies)
	}
	if _, ok := h.pers.RowKindRegistry().Lookup(movies); !ok {
		t.Fatalf("add-on catalog %s is not a Home row kind", movies)
	}

	calls := 0
	h.rt.routes["/catalog/movie/top/genre=Action.json"] = func(plugins.Request) (plugins.Response, error) {
		calls++
		// tt0133093 and tmdb:102 are in the catalogue; kitsu:999 has no id
		// Ether can match (never matched by its title); tt0999999 is not in
		// the catalogue.
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"metas":[
			{"id":"tt0133093","type":"movie","name":"The Matrix"},
			{"id":"tmdb:102","type":"movie","name":"The Matrix Reloaded"},
			{"id":"kitsu:999","type":"movie","name":"The Matrix Revolutions"},
			{"id":"tt0999999","type":"movie","name":"Unknown Film"}
		]}`)}, nil
	}
	sp := core.CollectionSpec{
		Name: "Action picks", MediaType: "movie", Sort: "source", Refresh: "daily", Enabled: true,
		Sources: []core.CollectionSource{{Kind: movies, Params: map[string]string{"genre": "Action"}}},
		Targets: core.CollectionTargets{HomeRow: true, CollectionsTab: true, Users: core.AllUsers()},
	}
	prev, err := h.cols.Preview(ctx, sp, 20)
	if err != nil {
		t.Fatal(err)
	}
	const warning = "2 of 4 items in Popular Movies · movie could not be matched to the catalogue"
	if prev.Total != 2 || !strings.Contains(strings.Join(prev.Warnings, "\n"), warning) {
		t.Fatalf("preview = %d items, warnings %v", prev.Total, prev.Warnings)
	}
	created, err := h.cols.CreateSpec(ctx, sp)
	if err != nil {
		t.Fatal(err)
	}
	if created, err = h.cols.Rebuild(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	if created.ItemCount != 2 || !strings.Contains(strings.Join(created.Warnings, "\n"), warning) {
		t.Fatalf("build = %d items, warnings %v", created.ItemCount, created.Warnings)
	}
	// The catalog does not declare skip: one page per read. Creating a
	// remote-source collection starts an initial build, so the explicit
	// rebuild below can add a third read when that build wins the race.
	if calls < 2 || calls > 3 {
		t.Fatalf("catalog reads = %d, want 2 or 3 one-page reads", calls)
	}

	// The source's own limit bounds the read.
	limited := sp
	limited.Sources = []core.CollectionSource{{Kind: movies, Params: map[string]string{"genre": "Action"}, MaxItems: 1}}
	if prev, err = h.cols.Preview(ctx, limited, 20); err != nil || prev.Total != 1 {
		t.Fatalf("limited preview = %+v, %v", prev, err)
	}
}

func TestAddonCatalogValidation(t *testing.T) {
	h := newListTestHarness(t)
	rec := installCatalogAddon(t, h)
	movies := addonCatalogKind(rec, "movie", "top")
	for name, tc := range map[string]struct {
		src     core.CollectionSource
		refresh string
		field   string
	}{
		"undeclared option": {src: core.CollectionSource{Kind: movies, Params: map[string]string{"genre": "Horror"}}, field: "genre"},
		"undeclared extra":  {src: core.CollectionSource{Kind: movies, Params: map[string]string{"search": "x"}}, field: "search"},
		"too many items":    {src: core.CollectionSource{Kind: movies, MaxItems: 2001}, field: "maxItems"},
		"live refresh":      {src: core.CollectionSource{Kind: movies}, refresh: "live", field: "refresh"},
	} {
		sp := core.CollectionSpec{Name: name, MediaType: "movie", Refresh: tc.refresh, Sources: []core.CollectionSource{tc.src}}
		if _, err := h.cols.Preview(context.Background(), sp, 20); err == nil || !strings.Contains(err.Error(), tc.field) {
			t.Errorf("%s: err = %v, want a %s error", name, err, tc.field)
		}
	}
}

func TestAddonCatalogPagesWithSkipUntilTheEnd(t *testing.T) {
	h := newListTestHarness(t)
	ctx := context.Background()
	rec := installCatalogAddon(t, h)
	series := addonCatalogKind(rec, "series", "top")
	h.rt.routes["/catalog/series/top.json"] = func(plugins.Request) (plugins.Response, error) {
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"metas":[{"id":"tt0903747","type":"series"}]}`)}, nil
	}
	h.rt.routes["/catalog/series/top/skip=1.json"] = func(plugins.Request) (plugins.Response, error) {
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"metas":[{"id":"tt7777777","type":"series"},{"id":"kitsu:5","type":"series"}]}`)}, nil
	}
	// skip=3 is not routed: the fake answers 404, the protocol's end of a
	// paged catalog, which is not a failure.
	created, err := h.cols.CreateSpec(ctx, core.CollectionSpec{Name: "Series", MediaType: "show", Refresh: "daily", Enabled: true,
		Sources: []core.CollectionSource{{Kind: series}}, Targets: core.CollectionTargets{CollectionsTab: true, Users: core.AllUsers()}})
	if err != nil {
		t.Fatal(err)
	}
	if created, err = h.cols.Rebuild(ctx, created.ID); err != nil {
		t.Fatal(err)
	}
	paths := h.rt.paths()
	if created.ItemCount != 1 || !strings.Contains(strings.Join(created.Warnings, "\n"), "2 of 3 items") ||
		!strings.Contains(strings.Join(paths, " "), "/catalog/series/top/skip=3.json") {
		t.Fatalf("build = %d items, warnings %v, paths %v", created.ItemCount, created.Warnings, paths)
	}
	health, err := h.mgr.HealthOf(ctx, rec.ID)
	if err != nil || len(health.Failures) != 0 {
		t.Fatalf("end of catalog recorded as a failure: %+v %v", health, err)
	}
}
