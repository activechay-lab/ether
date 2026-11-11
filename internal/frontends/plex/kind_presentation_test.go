package plex

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// kindExperience is a frontend.Experience that publishes two plugin
// libraries: a music library (artist -> album -> track) and a photo library,
// backed by in-memory items. It exists to prove the Plex presentation mapping
// (spec §6.4) without the plugin store.
type kindExperience struct {
	frontend.Experience
}

func newKindExperience() *kindExperience {
	return &kindExperience{Experience: frontend.NoopExperience()}
}

func (e *kindExperience) Views(context.Context, *frontend.Viewer) ([]frontend.View, error) {
	return []frontend.View{
		{ID: "7", LibraryID: 7, Name: "Field Recordings", Type: core.TypeShow, Virtual: true, KindKey: "recordings", PluginID: "fieldrec", Native: plexBaseArtist},
		{ID: "8", LibraryID: 8, Name: "Snapshots", Type: core.TypeShow, Virtual: true, KindKey: "snapshots", PluginID: "fieldrec", Native: plexBasePhoto},
	}, nil
}

func (e *kindExperience) LibraryItems(_ context.Context, _ *frontend.Viewer, _ frontend.Device, viewID string, _ frontend.ItemQuery) (core.Page, error) {
	switch viewID {
	case "7":
		return core.Page{Items: []core.Title{
			{Key: core.PluginKey(101), Type: core.TypeShow, Title: "Artist One", ChildCount: 1},
		}, Total: 1}, nil
	case "8":
		return core.Page{Items: []core.Title{
			{Key: core.PluginKey(1201), Type: core.TypeShow, Title: "Photo One"},
		}, Total: 1}, nil
	}
	return core.Page{}, nil
}

func (e *kindExperience) Item(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
	return frontend.ItemDetail{Title: core.Title{Key: req.Key, Type: core.TypeShow, Title: "Artist One"}}, nil
}

func (e *kindExperience) Children(context.Context, *frontend.Viewer, frontend.Device, core.RatingKey, frontend.ItemQuery) (core.Page, error) {
	return core.Page{Items: []core.Title{{Key: core.PluginKey(102), Type: core.TypeSeason, Title: "Album One"}}}, nil
}

// findDirectory returns the /library/sections directory for the given title.
func findDirectory(t *testing.T, body string, title string) map[string]any {
	t.Helper()
	var out struct {
		MediaContainer struct {
			Directory []map[string]any `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("decode sections: %v", err)
	}
	for _, d := range out.MediaContainer.Directory {
		if d["title"] == title {
			return d
		}
	}
	return nil
}

func serveSections(t *testing.T, srv *Server, user *frontend.User) string {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/library/sections", nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(withRequest(req.Context(), &Request{User: user, Format: model.JSON}))
	srv.handleSections(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("sections status = %d", rec.Code)
	}
	return rec.Body.String()
}

func TestKindSectionPresentation(t *testing.T) {
	srv := newTestServer(testServerDeps{Experience: newKindExperience()})
	body := serveSections(t, srv, &ownerUser)

	music := findDirectory(t, body, "Field Recordings")
	if music == nil {
		t.Fatalf("music library missing from sections: %s", body)
	}
	if music["type"] != plexBaseArtist {
		t.Errorf("music section type = %v, want %s", music["type"], plexBaseArtist)
	}
	photo := findDirectory(t, body, "Snapshots")
	if photo == nil {
		t.Fatalf("photo library missing from sections: %s", body)
	}
	if photo["type"] != plexBasePhoto {
		t.Errorf("photo section type = %v, want %s", photo["type"], plexBasePhoto)
	}
	if movies := findDirectory(t, body, "Movies"); movies == nil || movies["type"] != "movie" {
		t.Errorf("builtin Movies section changed: %v", movies)
	}
}

func TestKindSectionViewerRestricted(t *testing.T) {
	srv := newTestServer(testServerDeps{Experience: newKindExperience()})
	viewer := &frontend.User{ID: 42, AllowedLibraries: []core.LibraryID{7}}
	body := serveSections(t, srv, viewer)

	if findDirectory(t, body, "Field Recordings") == nil {
		t.Errorf("allowed music library missing: %s", body)
	}
	if findDirectory(t, body, "Snapshots") != nil {
		t.Errorf("restricted viewer saw the photo library: %s", body)
	}
}

func TestKindSectionListsArtists(t *testing.T) {
	srv := newTestServer(testServerDeps{Experience: newKindExperience()})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/library/sections/7/all", nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(withRequest(req.Context(), &Request{User: &ownerUser, Format: model.JSON}))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("section", "7")
	rctx.URLParams.Add("sub", "all")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	srv.handleSectionSub(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		MediaContainer struct {
			Metadata []map[string]any `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.MediaContainer.Metadata) != 1 {
		t.Fatalf("artist list = %s", rec.Body.String())
	}
	if got := out.MediaContainer.Metadata[0]["type"]; got != plexBaseArtist {
		t.Errorf("artist item type = %v, want %s", got, plexBaseArtist)
	}
	if got := out.MediaContainer.Metadata[0]["title"]; got != "Artist One" {
		t.Errorf("artist title = %v", got)
	}
}
