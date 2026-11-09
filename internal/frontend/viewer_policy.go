package frontend

import (
	"context"
	"time"
)

// ViewerPolicy mutates household policy with host-side authorization.
type ViewerPolicy interface {
	SetMaxRating(context.Context, *Viewer, string) error
}

// PlaybackAccessPolicy is the optional playback admission check an access
// policy may expose. Absence means the caller applies no extra restriction.
type PlaybackAccessPolicy interface {
	CanPlay(*User, bool, time.Time) bool
}

// UserPolicySource is the optional per-viewer policy view an access policy
// may expose. It returns the user as policy currently sees them.
type UserPolicySource interface{ UserPolicy(*User) *User }

// ActiveUserSource checks current server membership for delegated stream
// access.
type ActiveUserSource interface {
	ActiveUser(context.Context, int64) (*User, error)
}

// ArtworkAccessPolicy is the optional restricted-viewer check an access
// policy may expose for artwork decisions.
type ArtworkAccessPolicy interface{ Restricted(*User) bool }
