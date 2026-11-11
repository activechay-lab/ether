package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

// pluginRefs is a KindStreams that also answers ItemRef <-> RatingKey, like
// the host KindResolver does for a stored plugin library.
type pluginRefs struct {
	byRef map[core.ItemRef]core.RatingKey
	byKey map[core.RatingKey]core.ItemRef
}

func (p *pluginRefs) OpenKind(context.Context, *core.Viewer, core.RatingKey, []string) (*frontend.KindMedia, error) {
	return nil, errors.New("unused")
}
func (p *pluginRefs) KindLeaves(context.Context, *core.Viewer, core.RatingKey) ([]frontend.KindLeaf, error) {
	return nil, errors.New("unused")
}
func (p *pluginRefs) KeyForRef(_ context.Context, _ *core.Viewer, ref core.ItemRef) (core.RatingKey, error) {
	key, ok := p.byRef[ref]
	if !ok {
		return 0, core.ErrNotFound
	}
	return key, nil
}
func (p *pluginRefs) RefsForKeys(_ context.Context, _ *core.Viewer, keys []core.RatingKey) (map[core.RatingKey]core.ItemRef, error) {
	out := map[core.RatingKey]core.ItemRef{}
	for _, key := range keys {
		if ref, ok := p.byKey[key]; ok {
			out[key] = ref
		}
	}
	return out, nil
}

func pluginLibraryHost() (frontend.Host, *pluginRefs) {
	rootKey := core.PluginKey(1)
	leafKey := core.PluginKey(2)
	rootRef := core.NewItemRef("field", "album", "a1")
	leafRef := core.NewItemRef("field", "album", "t1")
	refs := &pluginRefs{
		byRef: map[core.ItemRef]core.RatingKey{rootRef: rootKey, leafRef: leafKey},
		byKey: map[core.RatingKey]core.ItemRef{rootKey: rootRef, leafKey: leafRef},
	}
	identity := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token != "grant" {
			return nil, errors.New("invalid")
		}
		return &frontend.Viewer{ID: 7}, nil
	}}
	experience := frontendtest.Experience{
		ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			return []frontend.View{
				{ID: "1", LibraryID: 1, Name: "Movies", Type: "movie", Native: "movie"},
				{ID: "3", LibraryID: 3, Name: "Records", Type: "show", KindKey: "album", PluginID: "field", Native: "series"},
				{ID: "4", LibraryID: 4, Name: "Books", Type: "movie", KindKey: "book", PluginID: "read", Native: "books"},
			}, nil
		},
		LibraryItemsFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ string, _ frontend.ItemQuery) (frontend.Page, error) {
			return frontend.Page{Items: []frontend.Title{{Key: rootKey, Type: "show", Title: "Album One"}}, Total: 1}, nil
		},
		ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
			return frontend.ItemDetail{Title: frontend.Title{Key: rootKey, Type: "show", Title: "Album One"}}, nil
		},
		ChildrenFunc: func(context.Context, *frontend.Viewer, frontend.Device, core.RatingKey, frontend.ItemQuery) (frontend.Page, error) {
			return frontend.Page{Items: []frontend.Title{{Key: leafKey, Type: "episode", Title: "Track One", Season: 1, Episode: 1}}, Total: 1}, nil
		},
	}
	host := frontend.Host{Identity: identity, Experience: experience, Sessions: frontendtest.Sessions{}, KindStreams: refs}
	return host, refs
}

func TestManifestListsCatalogPerPublishedPluginLibrary(t *testing.T) {
	host, _ := pluginLibraryHost()
	inst, err := (Frontend{}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stremio/grant/manifest.json", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("manifest: %d %s", w.Code, w.Body.String())
	}
	var body struct {
		IDPrefixes []string
		Catalogs   []struct{ Type, ID, Name string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !contains(body.IDPrefixes, "pk:") {
		t.Fatalf("idPrefixes: %v", body.IDPrefixes)
	}
	byID := map[string]struct{ Type, Name string }{}
	for _, c := range body.Catalogs {
		byID[c.ID] = struct{ Type, Name string }{c.Type, c.Name}
	}
	if _, ok := byID["ether-movies"]; !ok {
		t.Fatalf("builtin alias missing: %v", byID)
	}
	rec, ok := byID["ether-lib-3"]
	if !ok || rec.Type != "series" || rec.Name != "Records" {
		t.Fatalf("plugin library catalog: %v", byID)
	}
	if _, ok := byID["ether-lib-4"]; ok {
		t.Fatalf("books library must not declare a Stremio catalog: %v", byID)
	}
}

func TestCatalogServesPluginLibraryWithPKIDs(t *testing.T) {
	host, _ := pluginLibraryHost()
	inst, err := (Frontend{}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	inst.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stremio/grant/catalog/series/ether-lib-3.json", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"id":"pk:field:album:a1"`) {
		t.Fatalf("catalog: %d %s", w.Code, w.Body.String())
	}
}

func TestMetaAndStreamAnswerPKIDs(t *testing.T) {
	host, _ := pluginLibraryHost()
	inst, err := (Frontend{}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	mw := httptest.NewRecorder()
	inst.Handler().ServeHTTP(mw, httptest.NewRequest(http.MethodGet, "/stremio/grant/meta/series/pk:field:album:a1.json", nil))
	if mw.Code != http.StatusOK {
		t.Fatalf("meta: %d %s", mw.Code, mw.Body.String())
	}
	var meta struct {
		Meta struct {
			ID     string `json:"id"`
			Videos []struct {
				ID string `json:"id"`
			} `json:"videos"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(mw.Body.Bytes(), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Meta.ID != "pk:field:album:a1" || len(meta.Meta.Videos) != 1 || meta.Meta.Videos[0].ID != "pk:field:album:t1" {
		t.Fatalf("meta body: %s", mw.Body.String())
	}

	// A stream request for a pk: id reaches lazy issuance with the plugin key.
	lazy := &pluginLazy{leafKey: core.PluginKey(2)}
	host.LazyStreams = lazy
	host.Capabilities = frontendtest.Capabilities{}
	host.ClientIP = func(*http.Request) (netip.Addr, bool) { return netip.MustParseAddr("192.168.1.7"), true }
	host.SecurePortalURL = func(context.Context, *http.Request) (string, error) { return "https://ether.example/ether/me", nil }
	inst2, err := (Frontend{}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	sw := httptest.NewRecorder()
	inst2.Handler().ServeHTTP(sw, httptest.NewRequest(http.MethodGet, "/stremio/grant/stream/series/pk:field:album:t1.json", nil))
	if sw.Code != http.StatusOK || !lazy.issued {
		t.Fatalf("stream: %d %s", sw.Code, sw.Body.String())
	}
}

type pluginLazy struct {
	issued  bool
	leafKey core.RatingKey
}

func (f *pluginLazy) Issue(_ context.Context, credential string, request frontend.PlanRequest) (string, error) {
	if credential != "grant" || request.Protocol != "stremio" || request.Item != f.leafKey {
		return "", errors.New("wrong scope")
	}
	f.issued = true
	return "/sessions/lazy/scoped/file", nil
}
func (*pluginLazy) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusPartialContent)
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
