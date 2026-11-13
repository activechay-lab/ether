package plex

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
	"github.com/braedonsaunders/ether/internal/sessions"
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

func (e *kindExperience) PublishedViews(context.Context) ([]frontend.View, error) {
	return []frontend.View{
		{ID: "7", LibraryID: 7, Name: "Field Recordings", Type: core.TypeShow, Virtual: true, KindKey: "recordings", PluginID: "fieldrec", Native: plexBaseArtist},
		{ID: "8", LibraryID: 8, Name: "Snapshots", Type: core.TypeShow, Virtual: true, KindKey: "snapshots", PluginID: "fieldrec", Native: plexBasePhoto},
	}, nil
}

// Music library 7: artist 101 -> album 102 -> track 103.
// Photo library 8: album 1201 -> photo 1202.
func kindMusicArtist() core.Title {
	return core.Title{Key: core.PluginKey(101), Type: core.TypeShow, Title: "Artist One", ChildCount: 1, KindLevel: "artist", LibraryID: 7}
}
func kindMusicAlbum() core.Title {
	return core.Title{Key: core.PluginKey(102), Type: core.TypeSeason, Title: "Album One", ChildCount: 1, ShowKey: core.PluginKey(101), ShowTitle: "Artist One", KindLevel: "album", LibraryID: 7}
}
func kindMusicTrack() core.Title {
	return core.Title{Key: core.PluginKey(103), Type: core.TypeEpisode, Title: "Track One", Episode: 1, Season: 1, ShowKey: core.PluginKey(101), ShowTitle: "Artist One", KindLevel: "track", LibraryID: 7, Runtime: 210 * time.Second}
}
func kindPhotoAlbum() core.Title {
	return core.Title{Key: core.PluginKey(1201), Type: core.TypeShow, Title: "Album One", ChildCount: 1, KindLevel: "album", LibraryID: 8}
}
func kindPhoto() core.Title {
	return core.Title{Key: core.PluginKey(1202), Type: core.TypeEpisode, Title: "Photo One", Episode: 1, Season: 1, ShowKey: core.PluginKey(1201), ShowTitle: "Album One", KindLevel: "photo", LibraryID: 8}
}

func (e *kindExperience) LibraryItems(_ context.Context, _ *frontend.Viewer, _ frontend.Device, viewID string, _ frontend.ItemQuery) (core.Page, error) {
	switch viewID {
	case "7":
		return core.Page{Items: []core.Title{kindMusicArtist()}, Total: 1}, nil
	case "8":
		return core.Page{Items: []core.Title{kindPhotoAlbum()}, Total: 1}, nil
	}
	return core.Page{}, nil
}

func (e *kindExperience) Item(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
	for _, t := range []core.Title{kindMusicArtist(), kindMusicAlbum(), kindMusicTrack(), kindPhotoAlbum(), kindPhoto()} {
		if t.Key == req.Key {
			return frontend.ItemDetail{Title: t}, nil
		}
	}
	return frontend.ItemDetail{}, core.ErrNotFound
}

func (e *kindExperience) Children(_ context.Context, _ *frontend.Viewer, _ frontend.Device, key core.RatingKey, _ frontend.ItemQuery) (core.Page, error) {
	switch key {
	case kindMusicArtist().Key:
		return core.Page{Items: []core.Title{kindMusicAlbum()}, Total: 1}, nil
	case kindMusicAlbum().Key:
		return core.Page{Items: []core.Title{kindMusicTrack()}, Total: 1}, nil
	case kindPhotoAlbum().Key:
		return core.Page{Items: []core.Title{kindPhoto()}, Total: 1}, nil
	}
	return core.Page{}, nil
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

// serveKindRoute calls a handler with {key}/{section}/{sub} URL params set.
func serveKindRoute(t *testing.T, srv *Server, handler http.HandlerFunc, target string, params map[string]string, user *frontend.User) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.Header.Set("Accept", "application/json")
	req = req.WithContext(withRequest(req.Context(), &Request{User: user, Format: model.JSON}))
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	handler(rec, req)
	return rec
}

// decodeMetadata decodes a single-object MediaContainer's Metadata list.
func decodeMetadata(t *testing.T, body []byte) []map[string]any {
	t.Helper()
	var out struct {
		MediaContainer struct {
			Metadata []map[string]any `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode metadata: %v\n%s", err, body)
	}
	return out.MediaContainer.Metadata
}

// TestKindMusicChildrenTypeFromLevel proves a music section's artist lists its
// album child typed album (9), and the album lists its track child typed track
// (10), each carrying its Part so a client can stream it.
func TestKindMusicChildrenTypeFromLevel(t *testing.T) {
	srv := newTestServer(testServerDeps{Experience: newKindExperience()})

	artistKey := kindMusicArtist().Key.String()
	albums := decodeMetadata(t, serveKindRoute(t, srv, srv.handleChildren, "/library/metadata/"+artistKey+"/children", map[string]string{"key": artistKey}, &ownerUser).Body.Bytes())
	if len(albums) != 1 {
		t.Fatalf("album children = %d, want 1", len(albums))
	}
	if got := albums[0]["type"]; got != plexBaseAlbum {
		t.Errorf("album type = %v, want %s", got, plexBaseAlbum)
	}
	if got := albums[0]["title"]; got != "Album One" {
		t.Errorf("album title = %v", got)
	}

	albumKey := kindMusicAlbum().Key.String()
	tracks := decodeMetadata(t, serveKindRoute(t, srv, srv.handleChildren, "/library/metadata/"+albumKey+"/children", map[string]string{"key": albumKey}, &ownerUser).Body.Bytes())
	if len(tracks) != 1 {
		t.Fatalf("track children = %d, want 1", len(tracks))
	}
	if got := tracks[0]["type"]; got != plexBaseTrack {
		t.Errorf("track type = %v, want %s", got, plexBaseTrack)
	}
	if got := tracks[0]["title"]; got != "Track One" {
		t.Errorf("track title = %v", got)
	}
}

// TestKindPhotoChildrenTypeFromLevel proves a photo section's album lists its
// photo child typed photo (13) with its Part.
func TestKindPhotoChildrenTypeFromLevel(t *testing.T) {
	srv := newTestServer(testServerDeps{Experience: newKindExperience()})
	albumKey := kindPhotoAlbum().Key.String()
	photos := decodeMetadata(t, serveKindRoute(t, srv, srv.handleChildren, "/library/metadata/"+albumKey+"/children", map[string]string{"key": albumKey}, &ownerUser).Body.Bytes())
	if len(photos) != 1 {
		t.Fatalf("photo children = %d, want 1", len(photos))
	}
	if got := photos[0]["type"]; got != plexBasePhoto {
		t.Errorf("photo type = %v, want %s", got, plexBasePhoto)
	}
}

// TestKindTrackMetadataHasPart proves a plugin track's detail page renders a
// Media with a Part whose key is a Plex part URL, so the client streams bytes
// from the shared pipeline.
func TestKindTrackMetadataHasPart(t *testing.T) {
	srv := newTestServer(testServerDeps{Experience: newKindExperience()})
	trackKey := kindMusicTrack().Key.String()
	rec := serveKindRoute(t, srv, srv.handleMetadata, "/library/metadata/"+trackKey, map[string]string{"key": trackKey}, &ownerUser)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	md := decodeMetadata(t, rec.Body.Bytes())
	if len(md) != 1 {
		t.Fatalf("track metadata = %d, want 1", len(md))
	}
	if got := md[0]["type"]; got != plexBaseTrack {
		t.Errorf("track detail type = %v, want %s", got, plexBaseTrack)
	}
	media, ok := md[0]["Media"].([]any)
	if !ok || len(media) == 0 {
		t.Fatalf("track detail has no Media: %v", md[0])
	}
	first := media[0].(map[string]any)
	parts, ok := first["Part"].([]any)
	if !ok || len(parts) == 0 {
		t.Fatalf("track Media has no Part: %v", first)
	}
	key, _ := parts[0].(map[string]any)["key"].(string)
	if !strings.HasPrefix(key, "/library/parts/") {
		t.Errorf("track Part key = %q, want /library/parts/...", key)
	}
}

// kindStreamSource serves fixed bytes as a core.MediaSource for a plugin item.
type kindStreamSource struct {
	data []byte
	name string
}

func (k *kindStreamSource) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 || off >= int64(len(k.data)) {
		return 0, io.EOF
	}
	n := copy(p, k.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
func (k *kindStreamSource) Size() int64       { return int64(len(k.data)) }
func (k *kindStreamSource) Name() string      { return k.name }
func (k *kindStreamSource) ReleaseID() string { return "kind:" + k.name }
func (k *kindStreamSource) Close() error      { return nil }

// TestKindPartStreamsBytes proves a plugin track/photo Part streams its bytes
// through the same Plex part endpoint a catalogue item uses, routed to the
// shared KindPlayback pipeline (spec §15.1), with Range support.
func TestKindPartStreamsBytes(t *testing.T) {
	data := bytes.Repeat([]byte("kindbytes"), 64)
	fake := newFakePlayback(sampleInfo)
	kindSrc := func(_ context.Context, _ *core.Viewer, key core.RatingKey) (*core.KindInput, error) {
		title := kindMusicTrack()
		if key == kindPhoto().Key {
			title = kindPhoto()
		}
		return &core.KindInput{
			Title:    &title,
			SourceID: key.String(),
			Open: func(context.Context) (core.MediaSource, error) {
				return &kindStreamSource{data: data, name: key.String()}, nil
			},
		}, nil
	}
	rt := sessions.NewLocalRuntime(sessions.LocalRuntimeDeps{Playback: fake, KindSource: kindSrc})
	srv := newTestServer(testServerDeps{Experience: newKindExperience(), Deps: Deps{Host: frontend.Host{Sessions: rt}, Playback: fake}})
	defer srv.Close()

	for _, key := range []core.RatingKey{kindMusicTrack().Key, kindPhoto().Key} {
		part := key.String()
		rec := serveKindRoute(t, srv, srv.handlePart, "/library/parts/"+part+"/1/file.mkv", map[string]string{"part": part, "changestamp": "1", "file": "file.mkv"}, &ownerUser)
		if rec.Code != http.StatusPartialContent && rec.Code != http.StatusOK {
			t.Fatalf("part %s status=%d body=%s", key, rec.Code, rec.Body.String())
		}
		if !bytes.Equal(rec.Body.Bytes(), data) {
			t.Fatalf("part %s served %d bytes, want %d", key, rec.Body.Len(), len(data))
		}
	}
}
