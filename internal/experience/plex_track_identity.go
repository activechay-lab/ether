package experience

import "github.com/braedonsaunders/ether/internal/frontend"

// Plex track identity types and translation live in internal/frontend
// (host_sessions_experience.go).
type PlexIssuedIdentity = frontend.PlexIssuedIdentity
type UnsupportedPlexTrackIdentityError = frontend.UnsupportedPlexTrackIdentityError

var TranslatePlexTrackIdentity = frontend.TranslatePlexTrackIdentity
