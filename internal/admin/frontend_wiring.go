package admin

import (
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontends/mediabrowser"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
)

// accountSetupID identifies the frontend that supplies external account setup.
const accountSetupID = "plex" // Wire the owner account setup identity.

// consoleFrontendPolicy wires frontend-owned connection options into the console.
func consoleFrontendPolicy(id string) core.FrontendHostPolicy {
	switch id {
	case "plex": // Wire the externally managed account and main listener.
		return plex.HostPolicy()
	case "jellyfin", "emby": // Wire native API connection options.
		p := mediabrowser.HostPolicy()
		p.SharedPrefix = "/" + id
		return p
	case "silo":
		return core.FrontendHostPolicy{PublicURL: true, SharedPrefix: "/" + id, PasswordConnect: true}
	case "stremio", "webdav", "m3u", "dlna":
		return core.FrontendHostPolicy{PublicURL: true, SharedPrefix: "/" + id}
	default:
		return core.FrontendHostPolicy{PublicURL: true}
	}
}
