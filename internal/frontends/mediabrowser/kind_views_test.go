package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// pluginViewsExercise returns a Movies builtin (library 1) plus four plugin
// libraries whose MediaBrowser presentation declares the non-AV collection
// types Jellyfin and Emby actually model. View.Native carries the declared
// CollectionType, exactly as the experience layer sets it from the kind's
// Presentations (spec §6.2/§6.4).
func pluginViewsExercise() frontendtest.Experience {
	return frontendtest.Experience{
		ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			return []frontend.View{
				{ID: "1", LibraryID: 1, Name: "Movies", Type: "movie", Native: "movies"},
				{ID: "3", LibraryID: 3, Name: "Records", Type: "movie", KindKey: "album", PluginID: "field", Native: "music"},
				{ID: "4", LibraryID: 4, Name: "Audiobooks", Type: "show", KindKey: "book", PluginID: "field", Native: "audiobooks"},
				{ID: "5", LibraryID: 5, Name: "Photos", Type: "movie", KindKey: "photo", PluginID: "field", Native: "photos"},
				{ID: "6", LibraryID: 6, Name: "Home Videos", Type: "show", KindKey: "clip", PluginID: "field", Native: "homevideos"},
			}, nil
		},
		LibraryItemsFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, id string, _ frontend.ItemQuery) (frontend.Page, error) {
			return frontend.Page{Items: []frontend.Title{
				{Key: core.PluginKey(1), Type: "movie", Title: "Item " + id},
			}, Total: 1}, nil
		},
		ItemFunc: func(context.Context, frontend.ItemRequest) (frontend.ItemDetail, error) {
			return frontend.ItemDetail{Title: frontend.Title{Key: core.PluginKey(1), Type: "movie", Title: "Item"}}, nil
		},
		ChildrenFunc: func(context.Context, *frontend.Viewer, frontend.Device, core.RatingKey, frontend.ItemQuery) (frontend.Page, error) {
			return frontend.Page{}, nil
		},
	}
}

func TestMediaBrowserUserViewsUseDeclaredCollectionTypes(t *testing.T) {
	inst := mountJellyfinViewer(t, pluginViewsExercise(), frontend.Viewer{ID: 1, IsOwner: true})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/UserViews", nil)
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("UserViews: %d %s", w.Code, w.Body.String())
	}
	var page struct{ Items []map[string]any }
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"3": "music", "4": "audiobooks", "5": "photos", "6": "homevideos"}
	if len(page.Items) != 5 {
		t.Fatalf("views: %d %s", len(page.Items), w.Body.String())
	}
	for _, item := range page.Items {
		viewID, ok := item["Id"].(string)
		if !ok {
			t.Fatal(item)
		}
		n, err := ids.ParseGUID(ids.View, viewID)
		if err != nil {
			t.Fatal(err)
		}
		if item["Type"] != "CollectionFolder" {
			t.Fatalf("type: %v", item["Type"])
		}
		if n == 1 {
			if item["CollectionType"] != "movies" {
				t.Fatalf("builtin movies: %v", item["CollectionType"])
			}
			continue
		}
		if item["CollectionType"] != want[ids.Decimal(n)] {
			t.Fatalf("view %d: %v want %v", n, item["CollectionType"], want[ids.Decimal(n)])
		}
	}
}

func TestMediaBrowserVirtualFoldersUseDeclaredCollectionTypes(t *testing.T) {
	inst := mountJellyfinViewer(t, pluginViewsExercise(), frontend.Viewer{ID: 1, IsOwner: true})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/Library/VirtualFolders", nil)
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("folders: %d %s", w.Code, w.Body.String())
	}
	var folders []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &folders); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range folders {
		n, err := ids.ParseGUID(ids.View, f["ItemId"].(string))
		if err != nil {
			t.Fatal(err)
		}
		got[ids.Decimal(n)] = f["CollectionType"].(string)
	}
	want := map[string]string{"1": "movies", "3": "music", "4": "audiobooks", "5": "photos", "6": "homevideos"}
	for id, ct := range want {
		if got[id] != ct {
			t.Fatalf("folder %s: %q want %q (%v)", id, got[id], ct, got)
		}
	}
}

// A viewer restricted to one plugin library sees exactly that view, and can
// browse it through /Items?ParentId=<view guid>.
func TestMediaBrowserRestrictedViewerSeesOnePluginLibrary(t *testing.T) {
	exp := pluginViewsExercise()
	inner := exp.ViewsFunc
	exp.ViewsFunc = func(ctx context.Context, v *frontend.Viewer) ([]frontend.View, error) {
		all, err := inner(ctx, v)
		if err != nil {
			return nil, err
		}
		var out []frontend.View
		for _, view := range all {
			if view.LibraryID == 4 {
				out = append(out, view)
			}
		}
		return out, nil
	}
	inst := mountJellyfinViewer(t, exp, frontend.Viewer{ID: 9, AllowedLibraries: []core.LibraryID{4}})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/UserViews", nil)
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	var page struct{ Items []map[string]any }
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil {
		t.Fatalf("views: %d %s", w.Code, w.Body.String())
	}
	if len(page.Items) != 1 || page.Items[0]["Name"] != "Audiobooks" || page.Items[0]["CollectionType"] != "audiobooks" {
		t.Fatalf("restricted views: %v", page.Items)
	}
	viewGUID, _ := ids.GUID(ids.View, 4)
	bw := httptest.NewRecorder()
	br := httptest.NewRequest(http.MethodGet, "/Items?ParentId="+viewGUID, nil)
	br.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(bw, br)
	var items struct {
		Items            []map[string]any
		TotalRecordCount int
	}
	if bw.Code != http.StatusOK || json.Unmarshal(bw.Body.Bytes(), &items) != nil || len(items.Items) != 1 {
		t.Fatalf("browse: %d %s", bw.Code, bw.Body.String())
	}
	if items.Items[0]["Name"] != "Item 4" {
		t.Fatalf("browse item: %v", items.Items[0])
	}
}

// Live TV keeps the reserved view id (1<<61) and must not collide with a
// plugin library whose numeric id happens to be small.
func TestMediaBrowserLiveTVViewIDStaysReserved(t *testing.T) {
	if liveTVViewNumber != 1<<61 {
		t.Fatalf("liveTVViewNumber = %d", liveTVViewNumber)
	}
}
