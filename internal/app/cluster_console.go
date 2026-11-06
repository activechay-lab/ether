package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/clusterplayback"
)

// Mounted with the existing authenticated owner cluster API. Read-only and
// deliberately excludes grants, private URLs, viewer IDs and source revisions.
func (a *App) clusterConsoleHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		if access, ok := admin.RequestAccess(r.Context()); !ok || !access.Owner {
			http.Error(w, `{"error":"owner_required"}`, http.StatusUnauthorized)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		out := struct {
			SampledAt   time.Time                       `json:"sampledAt"`
			Assignments []clusterplayback.Assignment    `json:"assignments"`
			Events      []clusterplayback.RecoveryEvent `json:"events"`
			Truncated   bool                            `json:"truncated"`
		}{SampledAt: time.Now().UTC(), Assignments: []clusterplayback.Assignment{}, Events: []clusterplayback.RecoveryEvent{}}
		if p := a.deployment; p != nil && p.playback != nil {
			if reader, ok := p.playback.(interface {
				Assignments(context.Context) ([]clusterplayback.Assignment, error)
			}); ok {
				assignments, err := reader.Assignments(ctx)
				if err != nil {
					http.Error(w, `{"error":"assignments_unavailable"}`, 503)
					return
				}
				out.Truncated = len(assignments) > 200
				if out.Truncated {
					assignments = assignments[:200]
				}
				if assignments != nil {
					out.Assignments = assignments
				}
			}
			if reader, ok := p.playback.(clusterplayback.RecoveryEventReader); ok {
				events, err := reader.RecoveryEvents(ctx, 100)
				if err != nil {
					http.Error(w, `{"error":"recovery_unavailable"}`, 503)
					return
				}
				if len(events) > 100 {
					events = events[:100]
				}
				if events != nil {
					out.Events = events
				}
			}
		}
		_ = json.NewEncoder(w).Encode(out)
	})
}
