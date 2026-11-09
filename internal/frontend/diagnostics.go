package frontend

import (
	"github.com/braedonsaunders/ether/internal/diagnostics"
	"net/url"
)

// DiagnosticRecorder accepts neutral facts for owner-initiated bounded capture.
// The host owns recording lifetime, redaction, storage and export.
type DiagnosticRecorder interface {
	Recording() bool
	BeginSample(string) bool
	EndSample()
	Observe(DiagnosticEvent)
}
type DiagnosticClientFacts = diagnostics.ClientFacts
type DiagnosticClientInput = diagnostics.ClientInput
type DiagnosticEvent = diagnostics.Event
type DiagnosticHubSummary = diagnostics.HubSummary
type DiagnosticKind = diagnostics.Kind
type DiagnosticPlaybackCandidate = diagnostics.PlaybackCandidate
type DiagnosticPlaybackDecision = diagnostics.PlaybackDecision
type DiagnosticPlaybackVariant = diagnostics.PlaybackVariant
type DiagnosticResponseSummary = diagnostics.ResponseSummary
type DiagnosticViewerCandidate = diagnostics.ViewerCandidate
type DiagnosticViewerFacts = diagnostics.ViewerFacts
type DiagnosticViewerSection = diagnostics.ViewerSection

const (
	DiagnosticKindClientCapabilities = diagnostics.KindClientCapabilities
	DiagnosticKindDevice             = diagnostics.KindDevice
	DiagnosticKindPlaybackDecision   = diagnostics.KindPlaybackDecision
	DiagnosticKindPlaybackReported   = diagnostics.KindPlaybackReported
	DiagnosticKindRequest            = diagnostics.KindRequest
	DiagnosticKindRequestStarted     = diagnostics.KindRequestStarted
)

func ClassifyDiagnosticRequest(path string, status int, contentType string, bytesWritten int64) DiagnosticKind {
	return diagnostics.ClassifyRequest(path, status, contentType, bytesWritten)
}
func DiagnosticPlaybackRequestPath(path string) bool { return diagnostics.PlaybackRequestPath(path) }
func SafeDiagnosticQuery(query url.Values) string    { return diagnostics.SafeQuery(query) }
