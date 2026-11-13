package sessions

import "github.com/braedonsaunders/ether/internal/core"

func testSessionPolicy(p core.Protocol) core.SessionPolicy {
	switch p {
	case "plex":
		return core.SessionPolicy{ExternalAdmission: true}
	case "jellyfin":
		return core.SessionPolicy{DistributedBindings: true, PlainSourceGone: true}
	case "emby":
		return core.SessionPolicy{DistributedBindings: true, MasterMetadata: true}
	}
	return core.SessionPolicy{}
}
