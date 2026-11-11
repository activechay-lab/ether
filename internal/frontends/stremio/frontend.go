// Package stremio presents Ether's viewer-specific Stremio addon.
package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/route"
	"github.com/braedonsaunders/ether/internal/frontend/kit/support"
)

// writeJSON encodes before writing the status, so an unencodable value is a
// 500, never an empty 200.
func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	status := http.StatusOK
	if err != nil {
		slog.Error("encode response", "component", "stremio", "err", err)
		b, status = []byte(`{"error":{"code":"encode_failed","message":"the server could not encode this response"}}`), http.StatusInternalServerError
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

// Frontend is mounted on the main listener under /stremio.
type Frontend struct{}

func (Frontend) Describe() frontend.Descriptor {
	return frontend.Descriptor{ID: "stremio", Name: "Stremio", Version: "1.0.0", Clients: []string{"Stremio"}, SettingsSchema: frontend.AccountSchema("addon")}
}

func (Frontend) Mount(_ context.Context, host frontend.Host) (frontend.Instance, error) {
	if host.Identity == nil || host.Experience == nil || host.Sessions == nil {
		return nil, errors.New("stremio requires identity, experience and sessions")
	}
	i := &instance{host: host}
	if err := i.registerRoutes(); err != nil {
		return nil, err
	}
	return i, nil
}

// RegisteredRoutes enumerates the same routes mounted by the frontend.
func RegisteredRoutes() ([]route.Route, error) {
	i := &instance{}
	if err := i.registerRoutes(); err != nil {
		return nil, err
	}
	return i.router.Routes(), nil
}

func (i *instance) registerRoutes() error {
	i.router = &route.Router{}
	if err := i.router.Handle(http.MethodGet, "/{credential}/manifest.json", http.HandlerFunc(i.manifest)); err != nil {
		return err
	}
	for _, pattern := range []string{"/{credential}/catalog/{type}/{id}", "/{credential}/catalog/{type}/{id}/{extra}"} {
		if err := i.router.Handle(http.MethodGet, pattern, http.HandlerFunc(i.catalog)); err != nil {
			return err
		}
	}
	if err := i.router.Handle(http.MethodGet, "/{credential}/meta/{type}/{id}", http.HandlerFunc(i.meta)); err != nil {
		return err
	}
	if err := i.router.Handle(http.MethodGet, "/image/{secret}", http.HandlerFunc(i.image)); err != nil {
		return err
	}
	if err := i.router.Handle(http.MethodGet, "/{credential}/stream/{type}/{id}", http.HandlerFunc(i.stream)); err != nil {
		return err
	}
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		if err := i.router.Handle(method, "/sessions/lazy/{rest...}", http.HandlerFunc(i.serveLazy)); err != nil {
			return err
		}
	}
	return nil
}

type instance struct {
	host   frontend.Host
	router *route.Router
	pages  catalogPages
}

// catalogPages keeps one encoded catalog body per viewer and page freshness.
// A catalogue, policy, or grant change drops it. The body is the response
// already authorized for that viewer.
type catalogPages struct {
	mu      sync.Mutex
	entries map[string]catalogPage
}

type catalogPage struct {
	fresh frontend.PageFreshness
	body  []byte
}

func (i *instance) Handler() http.Handler {
	next := support.Wrap(i.host.Support, "stremio", func(r *http.Request) (*frontend.Viewer, frontend.Device) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/"), "/")
		if len(parts) < 2 || parts[0] == "image" || parts[0] == "sessions" {
			return nil, frontend.Device{}
		}
		v, err := i.host.Identity.Resolve(r.Context(), parts[0])
		if err != nil || v == nil {
			return nil, frontend.Device{}
		}
		return v, deviceFor(parts[0])
	}, func(path string) string {
		parts := strings.Split(path, "/")
		if len(parts) > 1 {
			parts[1] = "*"
		}
		return strings.Join(parts, "/")
	}, i.router)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Range")
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/stremio/") {
			r = r.Clone(r.Context())
			u := *r.URL
			u.Path = strings.TrimPrefix(u.Path, "/stremio")
			r.URL = &u
		}
		next.ServeHTTP(w, r)
	})
}
func (*instance) Listeners() []frontend.ListenerSpec {
	return []frontend.ListenerSpec{{Shared: true, PathPrefix: "/stremio"}}
}
func (*instance) Discovery() []frontend.Announcer { return nil }
func (*instance) Status(context.Context) frontend.Status {
	return frontend.Status{TargetVersion: "1.0", Conformance: "partial"}
}
func (*instance) Close(context.Context) error { return nil }
func (i *instance) Routes() []route.Route     { return i.router.Routes() }

func (i *instance) observeClient(r *http.Request, credential string, viewer *frontend.Viewer) {
	if i.host.ClientSeen != nil {
		i.host.ClientSeen(deviceFor(credential).ID)
	}
	support.Seen(r, i.host.Support, "stremio", viewer, deviceFor(credential))
}

func (i *instance) manifest(w http.ResponseWriter, r *http.Request) {
	credential := r.PathValue("credential")
	viewer, err := i.host.Identity.Resolve(r.Context(), credential)
	if err != nil || viewer == nil {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	i.observeClient(r, credential, viewer)
	catalogs := []any{
		map[string]any{"type": "movie", "id": "ether-movies", "name": "Ether Movies", "extra": []any{map[string]any{"name": "search"}, map[string]any{"name": "skip"}, map[string]any{"name": "genre"}}},
		map[string]any{"type": "series", "id": "ether-series", "name": "Ether Series", "extra": []any{map[string]any{"name": "search"}, map[string]any{"name": "skip"}, map[string]any{"name": "genre"}}},
	}
	for _, typ := range []string{"movie", "series"} {
		for _, row := range []struct{ id, name string }{{"continue", "Continue Watching"}, {"recent", "Recently Released"}, {"recommendations", "Recommendations"}} {
			catalogs = append(catalogs, map[string]any{"type": typ, "id": "ether-" + row.id, "name": row.name, "extra": []any{map[string]any{"name": "skip"}}})
		}
	}
	// One catalog per published plugin library whose presentation is a valid
	// Stremio type (spec §6.4). The library's catalog id is ether-lib-<id>;
	// its metas address plugin items as pk: ItemRefs.
	catalogs = append(catalogs, i.libraryCatalogs(r, viewer)...)
	writeJSON(w, map[string]any{
		"id": "org.ether.viewer", "version": "1.0.0", "name": "Ether",
		"types": []string{"movie", "series", "channel", "tv"}, "resources": []string{"catalog", "meta", "stream"},
		"idPrefixes": []string{"tmdb:", "tt", "pk:"},
		"catalogs":   catalogs,
	})
}

// libraryCatalogs lists catalogs for plugin libraries the viewer may open and
// whose Stremio presentation type is one Stremio accepts. Built-in movies and
// TV shows keep their existing ether-movies/ether-series aliases and are not
// repeated here.
func (i *instance) libraryCatalogs(r *http.Request, viewer *frontend.Viewer) []any {
	if i.host.Experience == nil {
		return nil
	}
	views, err := i.host.Experience.Views(r.Context(), viewer)
	if err != nil {
		return nil
	}
	out := make([]any, 0, len(views))
	for _, view := range views {
		if view.LibraryID <= 2 {
			continue
		}
		typ := stremioLibraryType(view)
		if typ == "" {
			continue
		}
		out = append(out, map[string]any{
			"type": typ, "id": libraryCatalogID(view.ID), "name": view.Name,
			"extra": []any{map[string]any{"name": "skip"}},
		})
	}
	return out
}
