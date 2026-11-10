package plex

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// sessionBroker is the mounted shared broker. Playback handlers publish
// through it; a missing broker fails closed for migrated calls.
func (i *plexInstance) sessionBroker() (frontend.Sessions, error) {
	if i.sessions == nil {
		return nil, core.ErrNotAvailable
	}
	return i.sessions, nil
}
