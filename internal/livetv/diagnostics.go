package livetv

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// TuneError retains machine-readable evidence without retaining a provider URL,
// response body or headers in its printable message. Unwrap preserves existing
// cancellation, availability and stream-limit handling.
type TuneError struct {
	Code           string
	Stage          string
	UpstreamStatus int
	RetryAfter     time.Duration
	cause          error
}

func (e *TuneError) Unwrap() error { return e.cause }
func (e *TuneError) Error() string {
	switch e.Code {
	case "provider_busy":
		return "All connections allowed by this Live TV provider are in use."
	case "provider_paced":
		return "This Live TV source has reached its configured stream-start limit."
	case "source_cooldown":
		return "This Live TV source is cooling down after a failed connection."
	case "provider_auth":
		return "The Live TV provider rejected the stream credentials or access."
	case "provider_channel_missing":
		return "The Live TV provider could not find this channel."
	case "provider_rate_limited":
		return "The Live TV provider is limiting stream requests."
	case "provider_http":
		return "The Live TV provider returned an unsuccessful stream response."
	case "timeout":
		return "The Live TV stream did not become ready before the startup deadline."
	case "cancelled":
		return "The Live TV request was cancelled."
	case "dns":
		return "The Live TV provider address could not be resolved."
	case "network":
		return "The connection to the Live TV provider failed."
	case "stream_limit":
		return "The viewer has reached their simultaneous stream limit."
	case "forbidden":
		return "The viewer is not allowed to play this live channel."
	case "not_found":
		return "This live channel or session is no longer available."
	case "no_video":
		return "The Live TV source did not provide a usable video stream."
	case "unsupported":
		return "The Live TV source returned an unsupported stream format."
	default:
		return "The live channel could not be started."
	}
}

// Diagnose never copies an upstream error's text: net/url and ffmpeg errors
// commonly include credentials in path components, not just query parameters.
func Diagnose(stage string, err error) *TuneError {
	var existing *TuneError
	if errors.As(err, &existing) {
		return existing
	}
	e := &TuneError{Code: "source_unavailable", Stage: stage, cause: err}
	var dns *net.DNSError
	var network net.Error
	switch {
	case errors.Is(err, context.Canceled):
		e.Code = "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		e.Code, e.RetryAfter = "timeout", time.Second
	case errors.Is(err, ErrProviderBusy):
		e.Code, e.RetryAfter = "provider_busy", 5*time.Second
	case errors.Is(err, ErrProviderPaced):
		e.Code = "provider_paced"
	case errors.Is(err, core.ErrStreamLimit):
		e.Code, e.RetryAfter = "stream_limit", 5*time.Second
	case errors.Is(err, core.ErrUnauthorized):
		e.Code = "forbidden"
	case errors.Is(err, core.ErrNotFound):
		e.Code = "not_found"
	case errors.As(err, &dns):
		e.Code = "dns"
	case errors.As(err, &network):
		e.Code = "network"
		if network.Timeout() {
			e.Code = "timeout"
		}
	case errors.Is(err, errRelayUnsupported), errors.Is(err, errRelayDowngrade):
		e.Code = "unsupported"
	}
	return e
}

func providerResponseError(status int) *TuneError {
	e := &TuneError{Code: "provider_http", Stage: "upstream_headers", UpstreamStatus: status, cause: core.ErrNotAvailable}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		e.Code = "provider_auth"
	case http.StatusNotFound, http.StatusGone:
		e.Code = "provider_channel_missing"
	case http.StatusTooManyRequests:
		e.Code = "provider_rate_limited"
	}
	return e
}

func (e *TuneError) LogAttrs() []any {
	return []any{"failure_code", e.Code, "stage", e.Stage, "upstream_status", e.UpstreamStatus, "retry_after", e.RetryAfter}
}

func (s *Service) logger() *slog.Logger {
	if s.o.Logger != nil {
		return s.o.Logger
	}
	return slog.Default().With("component", "livetv")
}

func (s *Service) sessionLog(ss *session) *slog.Logger {
	return s.logger().With("session", ss.info.ID, "channel", ss.info.Channel.ID,
		"source", ss.info.Channel.SourceID, "client", ss.caps.ClientID, "product", ss.caps.Product)
}

// observeMedia forwards streaming writes without buffering. It records the
// first successful media response separately from a request or a ready encoder.
// Repeated failures are bounded to one warning per session per five seconds.
type mediaObservation struct {
	http.ResponseWriter
	s           *Service
	ss          *session
	started     time.Time
	status      int
	bytes       int
	writeFailed bool
}

func (w *mediaObservation) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *mediaObservation) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *mediaObservation) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	n, err := w.ResponseWriter.Write(p)
	w.bytes += n
	w.writeFailed = w.writeFailed || err != nil
	return n, err
}
func (w *mediaObservation) Flush() {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
func (s *Service) observeMedia(w http.ResponseWriter, ss *session) *mediaObservation {
	return &mediaObservation{ResponseWriter: w, s: s, ss: ss, started: time.Now()}
}
func (w *mediaObservation) done() {
	ss := w.ss
	success := w.status >= 200 && w.status < 300 && w.bytes > 0 && !w.writeFailed
	failed := w.status >= 400 || w.writeFailed
	now := time.Now()
	ss.mu.Lock()
	first := success && !ss.firstMediaDelivered
	if first {
		ss.firstMediaDelivered = true
	}
	warn := failed && (ss.lastMediaWarning.IsZero() || now.Sub(ss.lastMediaWarning) >= 5*time.Second)
	if warn {
		ss.lastMediaWarning = now
	}
	ss.mu.Unlock()
	attrs := []any{"status", w.status, "bytes", w.bytes, "elapsed", time.Since(w.started), "write_failed", w.writeFailed}
	if first {
		w.s.sessionLog(ss).Info("live first media response delivered", attrs...)
	}
	if warn {
		w.s.sessionLog(ss).Warn("live media response failed", attrs...)
	}
}
