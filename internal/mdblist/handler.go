package mdblist

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// HandlerDeps are explicitly provided by the owner-authenticated admin API.
// Viewer validates that an ID identifies a current viewer, never a caller-
// supplied arbitrary account. Authentication is enforced by the mounting API.
type HandlerDeps struct {
	Tokens  *TokenStore
	Service func() Service
	Viewer  func(context.Context, int64) bool
	Enabled func() bool
}

func Handler(d HandlerDeps) http.Handler {
	mux := http.NewServeMux()
	run := func(w http.ResponseWriter, r *http.Request, op string) {
		user, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
		if err != nil || user <= 0 || d.Viewer == nil || !d.Viewer(r.Context(), user) {
			http.Error(w, "viewer not found", http.StatusNotFound)
			return
		}
		if d.Tokens == nil || d.Service == nil || d.Enabled == nil {
			http.Error(w, "integration unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		var result any
		switch op {
		case "status":
			_, loadErr := d.Tokens.Load(r.Context(), user)
			err = loadErr
			if errors.Is(err, sql.ErrNoRows) {
				err = nil
				result = map[string]any{"connected": false}
			} else if err == nil {
				var snapshot, warning string
				var queued int
				var backoff int64
				err = d.Tokens.DB.QueryRowContext(r.Context(), `SELECT snapshot,last_warning,backoff_until,(SELECT COUNT(*) FROM mdblist_outbox WHERE user_id=?) FROM mdblist_accounts WHERE user_id=?`, user, user).Scan(&snapshot, &warning, &backoff, &queued)
				if err == nil {
					result = map[string]any{"connected": true, "needsReconnect": warning == "auth_rejected", "lastSync": snapshot, "warning": warning, "backoffUntil": backoff, "queued": queued}
				}
			}
		case "connect":
			if !d.Enabled() {
				http.Error(w, "integration disabled", http.StatusConflict)
				return
			}
			var body struct {
				APIKey string `json:"apiKey"`
			}
			if err = json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&body); err != nil {
				http.Error(w, "invalid request", http.StatusBadRequest)
				return
			}
			if err = d.Tokens.Save(r.Context(), user, strings.TrimSpace(body.APIKey), time.Now()); err != nil {
				break
			}
			result = map[string]bool{"connected": true}
		case "disconnect":
			err = d.Tokens.Delete(r.Context(), user)
			result = map[string]bool{"connected": false}
		case "sync":
			if !d.Enabled() {
				http.Error(w, "integration disabled", http.StatusConflict)
				return
			}
			result, err = d.Service().Sync(r.Context(), user)
		}
		if err != nil {
			code := http.StatusBadGateway
			if errors.Is(err, ErrBusy) {
				code = http.StatusConflict
			}
			http.Error(w, http.StatusText(code), code)
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}
	mux.HandleFunc("GET /{user}/status", func(w http.ResponseWriter, r *http.Request) { run(w, r, "status") })
	mux.HandleFunc("POST /{user}/connect", func(w http.ResponseWriter, r *http.Request) { run(w, r, "connect") })
	mux.HandleFunc("POST /{user}/disconnect", func(w http.ResponseWriter, r *http.Request) { run(w, r, "disconnect") })
	mux.HandleFunc("POST /{user}/sync", func(w http.ResponseWriter, r *http.Request) { run(w, r, "sync") })
	return mux
}
