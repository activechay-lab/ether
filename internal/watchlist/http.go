package watchlist

import (
	"context"
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strconv"
	"time"
)

// Router must be mounted behind Ether's owner authentication middleware.
func (s *Service) Router() http.Handler {
	r := chi.NewRouter()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	r.Get("/settings", func(w http.ResponseWriter, r *http.Request) {
		v, e := s.Settings(r.Context())
		if e != nil {
			http.Error(w, "Settings unavailable", 500)
			return
		}
		reply(w, v)
	})
	r.Put("/settings", func(w http.ResponseWriter, r *http.Request) {
		var v Settings
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&v) != nil {
			http.Error(w, "Invalid settings", 400)
			return
		}
		if e := s.SaveSettings(r.Context(), v); e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		out, e := s.Settings(r.Context())
		if e != nil {
			http.Error(w, "Settings unavailable", 500)
			return
		}
		reply(w, out)
	})
	r.Post("/rss/sync", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
		defer cancel()
		_ = s.SyncRSS(ctx)
		out, e := s.Settings(r.Context())
		if e != nil {
			http.Error(w, "Settings unavailable", 500)
			return
		}
		reply(w, out)
	})
	r.Route("/users/{id}", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				id, e := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
				if e != nil || id <= 0 {
					http.Error(w, "Invalid viewer", 400)
					return
				}
				u, e := s.user(r.Context(), id)
				if e != nil {
					http.Error(w, "Viewer unavailable", 404)
					return
				}
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), viewerKey{}, u)))
			})
		})
		s.communityJobRoutes(r)
		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			v, e := s.Items(r.Context(), viewer(r))
			if e != nil {
				http.Error(w, "Watchlist unavailable", 500)
				return
			}
			reply(w, v)
		})
		// Page-sized membership reads avoid loading a viewer's entire watchlist.
		r.Post("/membership", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Keys []string `json:"keys"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body) != nil || len(body.Keys) > 100 {
				http.Error(w, "Invalid titles", 400)
				return
			}
			keys := make([]core.RatingKey, 0, len(body.Keys))
			for _, raw := range body.Keys {
				k, err := core.ParseRatingKey(raw)
				if err != nil {
					http.Error(w, "Invalid title", 400)
					return
				}
				keys = append(keys, k)
			}
			found, err := s.ContainsMany(r.Context(), viewer(r).ID, keys)
			if err != nil {
				http.Error(w, "Watchlist unavailable", 500)
				return
			}
			out := make(map[string]bool, len(found))
			for _, k := range keys {
				out[k.String()] = found[k]
			}
			reply(w, out)
		})
		writeCatalog := func(w http.ResponseWriter, r *http.Request, add bool) {
			k, err := core.ParseRatingKey(chi.URLParam(r, "key"))
			if err != nil {
				http.Error(w, "Invalid title", 400)
				return
			}
			title, err := s.o.Catalog.Get(r.Context(), k)
			if err != nil || title == nil || !s.allowed(viewer(r), title) {
				http.Error(w, "Title unavailable", 404)
				return
			}
			if title.Type != core.TypeMovie && title.Type != core.TypeShow {
				http.Error(w, "Invalid title", 400)
				return
			}
			var linked bool
			var pushErr error
			if writer, ok := s.o.Trakt.(interface {
				SetWatchlist(context.Context, int64, core.Title, bool) (bool, error)
			}); ok {
				ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
				linked, pushErr = writer.SetWatchlist(ctx, viewer(r).ID, *title, add)
				cancel()
			}
			if add {
				err = s.upsert(r.Context(), viewer(r).ID, "manual", *title)
			} else {
				err = s.Remove(r.Context(), viewer(r).ID, k)
			}
			if err != nil {
				http.Error(w, "Watchlist unavailable", 500)
				return
			}
			reply(w, map[string]bool{"saved": add, "traktLinked": linked, "traktFailed": pushErr != nil})
		}
		r.Put("/catalog/{key}", func(w http.ResponseWriter, r *http.Request) { writeCatalog(w, r, true) })
		r.Delete("/catalog/{key}", func(w http.ResponseWriter, r *http.Request) { writeCatalog(w, r, false) })
		r.Post("/sync", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
			defer cancel()
			result := SyncResult{}
			syncErr := s.syncViewer(ctx, viewer(r), &result)
			itemsCtx, itemsCancel := context.WithTimeout(r.Context(), time.Minute)
			defer itemsCancel()
			v, e := s.Items(itemsCtx, viewer(r))
			if e != nil {
				http.Error(w, "Watchlist unavailable", 500)
				return
			}
			reply(w, struct {
				Items []Item `json:"items"`
				SyncResult
				Failed bool `json:"failed"`
			}{v, result, syncErr != nil})
		})
		r.Post("/check", func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
			defer cancel()
			if e := s.Check(ctx, viewer(r)); e != nil {
				http.Error(w, "Availability check incomplete", 502)
				return
			}
			itemsCtx, itemsCancel := context.WithTimeout(r.Context(), time.Minute)
			defer itemsCancel()
			v, e := s.Items(itemsCtx, viewer(r))
			if e != nil {
				http.Error(w, "Watchlist unavailable", 500)
				return
			}
			reply(w, v)
		})
		r.Put("/items/{key}", func(w http.ResponseWriter, r *http.Request) {
			k, e := core.ParseRatingKey(chi.URLParam(r, "key"))
			if e != nil {
				http.Error(w, "Invalid title", 400)
				return
			}
			if e = s.Remind(r.Context(), viewer(r), k); e != nil {
				http.Error(w, "Title unavailable", 404)
				return
			}
			reply(w, map[string]bool{"saved": true})
		})
		r.Delete("/items/{key}", func(w http.ResponseWriter, r *http.Request) {
			k, e := core.ParseRatingKey(chi.URLParam(r, "key"))
			if e != nil {
				http.Error(w, "Invalid title", 400)
				return
			}
			if s.Remove(r.Context(), viewer(r).ID, k) != nil {
				http.Error(w, "Could not remove", 500)
				return
			}
			reply(w, map[string]bool{"removed": true})
		})
	})
	return r
}

type viewerKey struct{}

func viewer(r *http.Request) *core.User {
	u, _ := r.Context().Value(viewerKey{}).(*core.User)
	return u
}
