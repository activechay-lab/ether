package app

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/clusterplayback"
	"github.com/braedonsaunders/ether/internal/core"
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
					for i := range assignments {
						if assignments[i].Playback == nil {
							assignments[i].Playback = a.clusterPlaybackDisplay(ctx, assignments[i].SessionID, assignments[i].Generation)
							if assignments[i].Playback == nil {
								assignments[i].Playback = &clusterplayback.PlaybackDisplay{}
							}
							if assignments[i].Playback.Viewer == "" && a.users != nil && assignments[i].ViewerID > 0 {
								if viewer, err := a.users.User(ctx, assignments[i].ViewerID); err == nil && viewer != nil {
									assignments[i].Playback.Viewer = viewer.Username
								}
							}
						}
					}
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

// Project only display facts out of the durable protocol snapshot. The opaque
// credential-bearing object is never returned, logged or restored for serving.
func (a *App) clusterPlaybackDisplay(ctx context.Context, id string, generation int64) *clusterplayback.PlaybackDisplay {
	var raw string
	if a.db == nil || a.db.QueryRowContext(ctx, `SELECT binding FROM clusterplayback_plex_bindings WHERE session=? AND generation=?`, id, generation).Scan(&raw) != nil {
		return nil
	}
	return clusterPlaybackDisplayJSON(raw)
}
func clusterPlaybackDisplayJSON(raw string) *clusterplayback.PlaybackDisplay {
	if len(raw) > 256<<10 {
		return nil
	}
	var snapshot struct {
		Source struct {
			TitleName, Username string
			Key                 core.RatingKey
			Client              core.ClientCaps
			Info                core.MediaInfo
		}
		Binding struct {
			Key      core.RatingKey
			Decision core.Decision
		}
	}
	if json.Unmarshal([]byte(raw), &snapshot) != nil {
		return nil
	}
	source, decision := snapshot.Source, snapshot.Binding.Decision
	d := &clusterplayback.PlaybackDisplay{Protocol: string(source.Client.Protocol), Decision: decision.Kind, VideoCopy: decision.VideoCopy, AudioCopy: decision.AudioCopy, Title: source.TitleName, Viewer: source.Username, Device: source.Client.DeviceName, App: source.Client.Product, BitrateKbps: decision.BitrateKbps}
	key := source.Key
	if key == 0 {
		key = snapshot.Binding.Key
	}
	if key != 0 {
		d.TitleKey = key.String()
	}
	if d.Device == "" {
		d.Device = source.Client.Device
	}
	height := 0
	if video := source.Info.Video(); video != nil {
		height = video.Height
		if decision.VideoCopy {
			decision.VideoCodec = video.Codec
		} else if decision.MaxHeight > 0 && decision.MaxHeight < height {
			height = decision.MaxHeight
		}
	}
	d.Video = videoLabel(decision.VideoCodec, height, "")
	return d
}
