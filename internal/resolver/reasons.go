package resolver

// Rejection reasons for the resolution ceiling and floor, shown in decision
// traces and the explain output.
const (
	ReasonAboveProfileMaxResolution = "above profile max resolution"
	ReasonAboveClientMaxResolution  = "above client max resolution"
	ReasonBelowProfileMinResolution = "below profile min resolution"
)
