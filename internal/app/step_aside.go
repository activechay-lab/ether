package app

import (
	"context"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

// newStepAsideFor builds a protocol-scoped StepAside policy. Only the Plex
// surface steps aside for synced Plex Media Servers; Jellyfin, Emby, and Silo
// viewers do not see the owner's PMS and always receive nil.
func (a *App) newStepAsideFor(protocol core.Protocol) *experience.StepAside {
	if a == nil || protocol != "plex" {
		return nil
	}
	var mu sync.Mutex
	var cachedRev uint64
	var cachedHeld experience.StepAsideHeld

	sa := experience.NewStepAside(experience.StepAsideOptions{
		Held: func() experience.StepAsideHeld {
			if a.plexServers == nil {
				return experience.StepAsideHeld{}
			}
			snap := a.plexServers.Held()
			if snap.Empty() {
				return experience.StepAsideHeld{}
			}
			mu.Lock()
			defer mu.Unlock()
			if cachedRev == snap.Rev {
				return cachedHeld
			}
			shows := make([]core.RatingKey, 0, len(snap.Shows))
			for k := range snap.Shows {
				shows = append(shows, k)
			}
			cachedRev = snap.Rev
			cachedHeld = experience.StepAsideHeld{
				Rev:      snap.Rev,
				Movies:   snap.Movies,
				Episodes: snap.Episodes,
				Shows:    shows,
			}
			return cachedHeld
		},
		AiredEpisodes: func(ctx context.Context, shows []core.RatingKey) (map[core.RatingKey][]core.RatingKey, error) {
			if a.catalog == nil {
				return nil, nil
			}
			return a.catalog.AiredShowEpisodes(ctx, shows)
		},
		WriteGeneration: func() uint64 {
			if a.catalog == nil {
				return 0
			}
			return a.catalog.WriteGeneration()
		},
		State: a.state,
		Sessions: func() []*core.PlaySession {
			if a.playback == nil {
				return nil
			}
			return a.playback.Sessions()
		},
	})
	sa.Prime()
	return sa
}
