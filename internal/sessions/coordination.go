package sessions

import "github.com/braedonsaunders/ether/internal/frontend"

// Coordination holds the v39 playback owner history and lifecycle gates
// shared by every protocol adapter in one Ether process.
type Coordination struct {
	Owners *PlaybackOwners
	Gates  *Lifecycle
}

func NewCoordination() *Coordination {
	return &Coordination{Owners: NewPlaybackOwners(), Gates: NewLifecycle()}
}

// PlaybackOwners returns the owner history as the frontend host contract.
func (c *Coordination) PlaybackOwners() frontend.PlaybackOwners {
	if c == nil || c.Owners == nil {
		return nil
	}
	return c.Owners
}

// LifecycleGates returns the lifecycle gates as the frontend host contract.
func (c *Coordination) LifecycleGates() frontend.Lifecycle {
	if c == nil || c.Gates == nil {
		return nil
	}
	return c.Gates
}
