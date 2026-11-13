package playback

import "github.com/braedonsaunders/ether/internal/core"

// brokerTestPolicy supplies the adapter semantics exercised by playback fixtures.
func brokerTestPolicy(protocol core.Protocol) core.SessionPolicy {
	switch protocol {
	case "plex":
		return core.SessionPolicy{ExternalAdmission: true}
	case "jellyfin":
		return core.SessionPolicy{DistributedBindings: true, PlainSourceGone: true}
	case "emby":
		return core.SessionPolicy{DistributedBindings: true, MasterMetadata: true}
	}
	return core.SessionPolicy{}
}
