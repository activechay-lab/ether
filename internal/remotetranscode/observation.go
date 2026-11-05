package remotetranscode

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// observationBackend uses the real Manager Session signatures. In particular,
// transcode.Progress is the local type, not a structurally similar substitute.
// HTTP sessions currently lack these contracts and fail inspection explicitly.
type observationBackend interface {
	Info() core.MediaInfo
	Decision() core.Decision
	EncoderStatus() *core.EncoderStatus
	Progress() transcode.Progress
	GoodputKbps() int
	RecoveryBuffer() core.RecoveryBuffer
	RequestedPosition() time.Duration
	SegmentDuration() time.Duration
	Container() string
	Protocol() string
	HWAccelerated() bool
}

// ObservationContracts identifies the optional contracts supplied by this sample.
// Observe currently requires the complete bundle: unsupported backends return an
// error before any ObservedSession is constructed. A nil EncoderStatus is a real
// backend observation (no active run), not an unsupported-contract sentinel.
type ObservationContracts struct {
	Info, Decision, EncoderStatus, Progress, Goodput, RecoveryBuffer       bool
	RequestedPosition, SegmentDuration, Container, Protocol, HWAccelerated bool
}

type observation struct {
	Identity                           Identity
	SampledAt                          time.Time
	Info                               core.MediaInfo
	Decision                           core.Decision
	EncoderStatus                      *core.EncoderStatus
	Progress                           transcode.Progress
	GoodputKbps                        int
	RecoveryBuffer                     core.RecoveryBuffer
	RequestedPosition, SegmentDuration time.Duration
	Container, Protocol                string
	HWAccelerated                      bool
}

// ObservedSession is an immutable detached sample, not a live HLSSession. Its
// optional getters describe only this observation. Recovery callers must request
// a new Observe and reject errors/old samples before making a recovery decision.
// SampledAt is the worker clock. RequestStarted/ReceivedAt are controller-local
// times (including monotonic readings); account for RTT and host clock skew.
type ObservedSession struct {
	sample                     observation
	requestStarted, receivedAt time.Time
}

func (s *ObservedSession) Identity() Identity        { return s.sample.Identity }
func (s *ObservedSession) SampledAt() time.Time      { return s.sample.SampledAt }
func (s *ObservedSession) RequestStarted() time.Time { return s.requestStarted }
func (s *ObservedSession) ReceivedAt() time.Time     { return s.receivedAt }
func (s *ObservedSession) Contracts() ObservationContracts {
	return ObservationContracts{true, true, true, true, true, true, true, true, true, true, true}
}
func (s *ObservedSession) Info() core.MediaInfo    { return cloneObservation(s.sample).Info }
func (s *ObservedSession) Decision() core.Decision { return cloneObservation(s.sample).Decision }
func (s *ObservedSession) EncoderStatus() *core.EncoderStatus {
	return cloneObservation(s.sample).EncoderStatus
}
func (s *ObservedSession) Progress() transcode.Progress        { return s.sample.Progress }
func (s *ObservedSession) GoodputKbps() int                    { return s.sample.GoodputKbps }
func (s *ObservedSession) RecoveryBuffer() core.RecoveryBuffer { return s.sample.RecoveryBuffer }
func (s *ObservedSession) RequestedPosition() time.Duration    { return s.sample.RequestedPosition }
func (s *ObservedSession) SegmentDuration() time.Duration      { return s.sample.SegmentDuration }
func (s *ObservedSession) Container() string                   { return s.sample.Container }
func (s *ObservedSession) Protocol() string                    { return s.sample.Protocol }
func (s *ObservedSession) HWAccelerated() bool                 { return s.sample.HWAccelerated }

func cloneObservation(o observation) observation {
	// Successful observation data has already survived the bounded JSON roundtrip;
	// another independent copy preserves nested slice and optional pointer ownership.
	b, _ := json.Marshal(o)
	var copy observation
	_ = json.Unmarshal(b, &copy)
	return copy
}

// Observe performs one authenticated, authorized read of this exact generation.
// Failure neither changes map presence nor supplies any retirement evidence.
func (s *Session) Observe(ctx context.Context) (*ObservedSession, error) {
	if !s.adapter.Capabilities().Observations {
		return nil, ErrUnsupported
	}
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return nil, ErrUncertain
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	started := time.Now()
	response, err := s.adapter.call(ctx, message{Op: "inspect", Identity: s.id}, nil)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	// Read the entire bounded DTO before decoding; LimitReader alone on a decoder
	// can accept a valid JSON prefix while ignoring an oversized trailing payload.
	b, err := io.ReadAll(io.LimitReader(response.Body, MaxMessage+1))
	if err != nil || len(b) > MaxMessage {
		return nil, ErrUncertain
	}
	var sample observation
	if json.Unmarshal(b, &sample) != nil || !validObservation(sample, s.id) {
		return nil, ErrUncertain
	}
	s.mu.Lock()
	stopped = s.stopped
	s.mu.Unlock()
	if stopped || ctx.Err() != nil {
		return nil, ErrUncertain
	}
	return &ObservedSession{sample: sample, requestStarted: started, receivedAt: time.Now()}, nil
}

func validObservation(o observation, id Identity) bool {
	if o.Identity != id || o.SampledAt.IsZero() || len(o.Info.Keyframes) > 0 || len(o.Info.Streams) > 64 || len(o.Info.Chapters) > 128 {
		return false
	}
	for _, stream := range o.Info.Streams {
		if stream.External || stream.ExternalURL != "" {
			return false
		}
	}
	// Values are backend data. Do not reinterpret 0/negative/unknown sentinels or
	// synthesize ranges, timing, hardware, goodput or progress from start requests.
	return true
}

func (w *Worker) inspect(out http.ResponseWriter, r *http.Request, e *execution, s core.OwnedEncoderSession) {
	if !w.opts.Capabilities.Observations {
		http.Error(out, "inspection unsupported", 422)
		return
	}
	backend, ok := s.(observationBackend)
	if !ok {
		http.Error(out, "inspection unsupported", 422)
		return
	}
	// This bundle is sampled sequentially under each existing backend method's
	// synchronization; it is NOT an atomic multi-field encoder snapshot. See README.
	sample := observation{Identity: e.id, Info: backend.Info(), Decision: backend.Decision(), EncoderStatus: backend.EncoderStatus(), Progress: backend.Progress(), GoodputKbps: backend.GoodputKbps(), RecoveryBuffer: backend.RecoveryBuffer(), RequestedPosition: backend.RequestedPosition(), SegmentDuration: backend.SegmentDuration(), Container: backend.Container(), Protocol: backend.Protocol(), HWAccelerated: backend.HWAccelerated()}
	sample.SampledAt = time.Now().UTC()
	b, err := json.Marshal(sample)
	if err != nil || len(b) > MaxMessage || !validObservation(sample, e.id) {
		http.Error(out, "inspection unavailable", 422)
		return
	}
	// A stop, expiry, cancellation or terminal transition during sampling cannot
	// turn the old running check into a new usable observation.
	w.mu.Lock()
	w.expire(e)
	active := !e.stopped && result(e) == "running"
	w.mu.Unlock()
	if !active || r.Context().Err() != nil {
		http.Error(out, "execution unavailable", 409)
		return
	}
	out.Header().Set("Content-Type", "application/json")
	out.Header().Set("Cache-Control", "no-store")
	_, _ = out.Write(b)
}

var _ core.RecoveryBufferSource = (*ObservedSession)(nil)
var _ core.PlaybackPosition = (*ObservedSession)(nil)
var _ core.GoodputSession = (*ObservedSession)(nil)
var _ observationBackend = (*ObservedSession)(nil)

// Compile against the actual local Manager session, in addition to transport fakes.
var _ observationBackend = (*transcode.Session)(nil)
