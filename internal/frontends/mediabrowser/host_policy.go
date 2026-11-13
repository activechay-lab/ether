package mediabrowser

import "github.com/braedonsaunders/ether/internal/frontend"

// HostPolicy describes the host services used by both native API surfaces.
func HostPolicy() frontend.FrontendHostPolicy {
	return frontend.FrontendHostPolicy{ProfileQualityEvaluation: true, InferBuffering: true, PlaybackTelemetry: true, ProbeLiveInput: true, PublicContentTags: true, LandscapeThumb: true, PublicURL: true, PasswordConnect: true}
}
