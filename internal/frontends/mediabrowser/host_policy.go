package mediabrowser

import "github.com/braedonsaunders/ether/internal/core"

// HostPolicy describes the host services used by both native API surfaces.
func HostPolicy() core.FrontendHostPolicy {
	return core.FrontendHostPolicy{LandscapeThumb: true, PublicURL: true, PasswordConnect: true}
}
