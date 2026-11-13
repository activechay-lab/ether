package core

// FrontendHostPolicy contains host integration choices supplied by a frontend.
// Protocol names select wiring only; shared services consume these options.
type FrontendHostPolicy struct {
	ProfileQualityEvaluation bool
	InferBuffering           bool
	PlaybackTelemetry        bool
	LegacySessionIdentity    bool
	ProbeLiveInput           bool
	BestEffortLiveStart      bool
	PublicContentTags        bool

	ExternalIdentity           bool
	NativePlaybackCapabilities bool
	NativeClientFamilies       bool
	LandscapeThumb             bool
	SpikeExperience            bool
	MainListener               bool
	PublicURL                  bool
	SharedPrefix               string
	PasswordConnect            bool
}
