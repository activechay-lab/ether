package plex

import "github.com/braedonsaunders/ether/internal/core"

// HostPolicy keeps account, listener and capability integration with this frontend.
func HostPolicy() core.FrontendHostPolicy {
	return core.FrontendHostPolicy{ExternalIdentity: true, NativePlaybackCapabilities: true, NativeClientFamilies: true, SpikeExperience: true, MainListener: true}
}
