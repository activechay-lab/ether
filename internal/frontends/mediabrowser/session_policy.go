package mediabrowser

import "github.com/braedonsaunders/ether/internal/frontend"

// SessionPolicy supplies the capabilities of the selected wire adapter.
func SessionPolicy(protocol frontend.Protocol) frontend.SessionPolicy {
	return frontend.SessionPolicy{DistributedBindings: true, MasterMetadata: protocol == "emby", PlainSourceGone: protocol == "jellyfin"}
}
