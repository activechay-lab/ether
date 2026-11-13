package plex

import "github.com/braedonsaunders/ether/internal/frontend"

// HostPolicy keeps account, listener and capability integration with this frontend.
func HostPolicy() frontend.FrontendHostPolicy {
	return frontend.FrontendHostPolicy{AutomaticSubtitleSelection: true, PlaybackTelemetry: true, LegacySessionIdentity: true, ExternalIdentity: true, NativePlaybackCapabilities: true, NativeClientFamilies: true, SpikeExperience: true, MainListener: true}
}
