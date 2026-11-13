package mediabrowser

import "github.com/braedonsaunders/ether/internal/core"

// SessionPolicy supplies the capabilities of the selected wire adapter.
func SessionPolicy(protocol core.Protocol) core.SessionPolicy {
	return core.SessionPolicy{DistributedBindings: true, MasterMetadata: protocol == "emby", PlainSourceGone: protocol == "jellyfin"}
}
