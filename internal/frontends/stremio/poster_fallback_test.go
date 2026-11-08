package stremio

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	_ "image/jpeg"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	artworkservice "github.com/braedonsaunders/ether/internal/artwork/service"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

type storedTitles map[core.RatingKey]core.Title

func (s storedTitles) StoredTitle(_ context.Context, key core.RatingKey) (*core.Title, error) {
	if t, ok := s[key]; ok {
		return &t, nil
	}
	return nil, core.ErrNotFound
}

// Owner rule: no title anywhere without a poster. With no secure portal and
// no loopback address a Stremio client cannot load an Ether image URL, yet
// every catalog item and meta still carries a poster it can load: TMDB's
// CDN for a real poster, Ether's generated poster as a small data: URI.
func TestStremioPostersWithoutReachableAddress(t *testing.T) {
	released := time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)
	real := core.Title{Key: core.MovieKey(42), Type: core.TypeMovie, Title: "Has Poster", PosterPath: "/real.jpg", Released: released}
	real.IDs.TMDB = 42
	bare := core.Title{Key: core.MovieKey(43), Type: core.TypeMovie, Title: "No Poster", Released: released}
	bare.IDs.TMDB = 43
	titles := storedTitles{real.Key: real, bare.Key: bare}
	art := artworkservice.Source{Catalog: titles, Generated: artwork.NewRenderer(nil, "", nil)}
	host := frontend.Host{
		Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 7}, nil }},
		Experience: frontendtest.Experience{ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
			return []frontend.View{{ID: "movies", Type: "movie"}}, nil
		}, LibraryItemsFunc: func(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (frontend.Page, error) {
			return frontend.Page{Items: []frontend.Title{real, bare}, Total: 2}, nil
		}, ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			return frontend.ItemDetail{Title: titles[req.Key]}, nil
		}},
		Sessions: frontendtest.Sessions{},
		// A LAN client and no secure portal: no Ether image URL is loadable.
		ClientIP:    func(*http.Request) (netip.Addr, bool) { return netip.MustParseAddr("192.168.1.7"), true },
		ImageGrants: frontendtest.ImageGrants{},
		Artwork:     art,
	}
	inst, err := (Frontend{}).Mount(context.Background(), host)
	if err != nil {
		t.Fatal(err)
	}
	get := func(path string) []map[string]any {
		rec := httptest.NewRecorder()
		inst.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var body struct {
			Metas []map[string]any
			Meta  map[string]any
		}
		if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body)
		}
		if body.Meta != nil {
			return []map[string]any{body.Meta}
		}
		return body.Metas
	}
	metas := append(get("/stremio/c/catalog/movie/ether-movies.json"), get("/stremio/c/meta/movie/tmdb:42.json")...)
	metas = append(metas, get("/stremio/c/meta/movie/tmdb:43.json")...)
	if len(metas) != 4 {
		t.Fatalf("metas = %d, want 4", len(metas))
	}
	for _, m := range metas {
		poster, _ := m["poster"].(string)
		switch m["id"] {
		case "tmdb:42":
			if poster != "https://image.tmdb.org/t/p/w342/real.jpg" {
				t.Errorf("real poster = %q", poster)
			}
		case "tmdb:43":
			data, ok := strings.CutPrefix(poster, "data:image/jpeg;base64,")
			raw, err := base64.StdEncoding.DecodeString(data)
			if !ok || err != nil || len(raw) > 50<<10 {
				t.Fatalf("generated poster = %.60q (%d bytes, %v)", poster, len(raw), err)
			}
			cfg, _, err := image.DecodeConfig(bytes.NewReader(raw))
			if err != nil || cfg.Width != 200 || cfg.Height != 300 {
				t.Errorf("generated poster %dx%d %v, want 200x300 JPEG", cfg.Width, cfg.Height, err)
			}
		}
	}
}
