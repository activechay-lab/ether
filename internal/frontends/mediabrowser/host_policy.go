package mediabrowser

import "github.com/braedonsaunders/ether/internal/core"

// HostPolicy describes the host services used by both native API surfaces.
func HostPolicy() core.FrontendHostPolicy {
	return core.FrontendHostPolicy{ProfileQualityEvaluation: true, InferBuffering: true, PlaybackTelemetry: true, ProbeLiveInput: true, PublicContentTags: true, LandscapeThumb: true, PublicURL: true, PasswordConnect: true}
}
