package app

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

type demandCatalogueKind struct {
	calls int
	fail  bool
}

func (*demandCatalogueKind) ID() string {
	return extend.PluginID("catalogue-test", plugins.StremioCatalogKey(plugins.StremioCat{Type: "movie", ID: "test"}))
}
func (*demandCatalogueKind) Describe() collections.KindDescribe                                   { return collections.KindDescribe{} }
func (*demandCatalogueKind) Validate(*collections.ValidationError, string, core.CollectionSource) {}
func (k *demandCatalogueKind) Gather(g *collections.Gather) {
	k.calls++
	if k.fail {
		g.Fail(errors.New("upstream unavailable"))
		return
	}
	g.ResolveExternal("Test", []collections.ExternalRef{{TMDB: 101, Type: "movie"}, {TMDB: 101, Type: "movie"}}, 0)
}

func TestCatalogueMaterializationDemandCacheAndFailure(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: core.MovieKey(101), Type: core.TypeMovie, IDs: core.IDs{TMDB: 101}, Title: "Test", Year: 2000, Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err = cat.UpsertDetailedTitle(ctx, title); err != nil {
		t.Fatal(err)
	}
	cols, err := collections.New(db, cat, nil)
	if err != nil {
		t.Fatal(err)
	}
	kind := &demandCatalogueKind{}
	if err = cols.RegisterSourceKind(kind); err != nil {
		t.Fatal(err)
	}
	mgr, err := plugins.New(plugins.Deps{DB: db})
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"id":"catalogue-test","name":"Test","version":"1.0.0","resources":["catalog"],"types":["movie"],"catalogs":[{"type":"movie","id":"test"}]}`
	if _, err = db.Exec(`INSERT INTO plugins_installed(id,name,version,manifest,installed_at,updated_at) VALUES('catalogue-test','Test','1.0.0',?,0,0)`, manifest); err != nil {
		t.Fatal(err)
	}
	a := &App{db: db, catalog: cat, collections: cols, pluginsRT: &pluginRuntime{mgr: mgr}}
	a.wireCatalogueSources()
	if kind.calls != 0 {
		t.Fatal("eager source work")
	}
	id := "stremio:catalogue-test:" + plugins.StremioCatalogKey(plugins.StremioCat{Type: "movie", ID: "test"})
	query := core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, LocalOnly: true, CatalogueSources: []string{id}}, Limit: 10}
	for i := 0; i < 2; i++ {
		page, err := cat.Browse(ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || page.Total != 1 {
			t.Fatalf("materialized page: %+v", page)
		}
	}
	if kind.calls != 1 {
		t.Fatalf("cached source calls=%d", kind.calls)
	}
	kind.fail = true
	if _, err = db.Exec(`UPDATE libraries_catalogue_state SET refreshed=0 WHERE source_id=?`, id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = cat.Browse(ctx, query); err == nil {
			t.Fatal("upstream failure hidden")
		}
	}
	if kind.calls != 2 {
		t.Fatalf("failure retry was not throttled: %d", kind.calls)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM libraries_catalogue_members WHERE source_id=?`, id).Scan(&count); err != nil || count != 1 {
		t.Fatalf("old membership lost: %d %v", count, err)
	}
}
