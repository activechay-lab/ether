package remotetranscode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func (s *fakeSession) Info() core.MediaInfo                { return s.observed.Info }
func (s *fakeSession) Decision() core.Decision             { return s.observed.Decision }
func (s *fakeSession) EncoderStatus() *core.EncoderStatus  { return s.observed.EncoderStatus }
func (s *fakeSession) Progress() transcode.Progress        { return s.observed.Progress }
func (s *fakeSession) GoodputKbps() int                    { return s.observed.GoodputKbps }
func (s *fakeSession) RecoveryBuffer() core.RecoveryBuffer { return s.observed.RecoveryBuffer }
func (s *fakeSession) RequestedPosition() time.Duration    { return s.observed.RequestedPosition }
func (s *fakeSession) SegmentDuration() time.Duration      { return s.observed.SegmentDuration }
func (s *fakeSession) Container() string                   { return s.observed.Container }
func (s *fakeSession) Protocol() string                    { return s.observed.Protocol }
func (s *fakeSession) HWAccelerated() bool                 { return s.observed.HWAccelerated }

type observationHiddenSession struct{ core.OwnedEncoderSession }

// Invoked by the actual mutual-TLS fixture; exercises the adapter, security
// handler, backend signatures and detached copies together.
func checkObservation(t *testing.T, w *Worker, f *fakeEncoder, s *Session) {
	t.Helper()
	now := time.Now().UTC()
	f.session.observed = observation{
		Info:          core.MediaInfo{Duration: time.Hour, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264"}}},
		Decision:      core.Decision{Kind: core.DecisionTranscode, AudioFilters: []string{"sampled-filter"}, SubtitleStream: -1},
		EncoderStatus: &core.EncoderStatus{Phase: "running", StartedAt: now.Add(-time.Minute), UpdatedAt: now, Speed: 0.8, PlayerPosition: durationPtr(12 * time.Second)},
		Progress:      transcode.Progress{Frame: 1234, FPS: 24, Speed: 0.8, OutTime: 50 * time.Second, Updated: now},
		GoodputKbps:   3210, RecoveryBuffer: core.RecoveryBuffer{Position: 12 * time.Second, ReadyUntil: 40 * time.Second, DeliveredUntil: 20 * time.Second, SeekAt: now.Add(-time.Second)},
		RequestedPosition: 12 * time.Second, SegmentDuration: 4 * time.Second, Container: "fmp4", Protocol: core.ProtocolHLS, HWAccelerated: true,
	}
	sample, err := s.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if sample.Identity() != s.id || sample.SampledAt().IsZero() || sample.ReceivedAt().Before(sample.RequestStarted()) || !sample.Contracts().RecoveryBuffer {
		t.Fatal("observation lost identity or timing")
	}
	if sample.Info().Duration != time.Hour || sample.Decision().Kind != core.DecisionTranscode || sample.EncoderStatus().Speed != 0.8 || sample.Progress().Frame != 1234 || sample.GoodputKbps() != 3210 || sample.RecoveryBuffer() != f.session.observed.RecoveryBuffer || sample.RequestedPosition() != 12*time.Second || sample.SegmentDuration() != 4*time.Second || sample.Container() != "fmp4" || sample.Protocol() != core.ProtocolHLS || !sample.HWAccelerated() {
		t.Fatal("backend observation values changed")
	}
	info := sample.Info()
	info.Streams[0].Codec = "mutation"
	decision := sample.Decision()
	decision.AudioFilters[0] = "mutation"
	status := sample.EncoderStatus()
	*status.PlayerPosition = 0
	if sample.Info().Streams[0].Codec != "h264" || sample.Decision().AudioFilters[0] != "sampled-filter" || *sample.EncoderStatus().PlayerPosition != 12*time.Second {
		t.Fatal("snapshot getters expose mutable ownership")
	}
	f.session.observed.GoodputKbps = 9999
	if sample.GoodputKbps() != 3210 {
		t.Fatal("detached observation changed with live backend")
	}
	fresh, err := s.Observe(context.Background())
	if err != nil || fresh.GoodputKbps() != 9999 {
		t.Fatal("fresh observation used stale cache", err)
	}
	// An idle run's nil status is a real supported backend result.
	f.session.observed.EncoderStatus = nil
	idle, err := s.Observe(context.Background())
	if err != nil || idle.EncoderStatus() != nil || !idle.Contracts().EncoderStatus {
		t.Fatal("nil backend run status lost", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Observe(ctx); err == nil {
		t.Fatal("canceled inspection succeeded")
	}
	// A credential/path-bearing or oversized backend result never becomes a sample.
	f.session.observed.Info.Streams[0].ExternalURL = "https://source.invalid/secret"
	if _, err := s.Observe(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsafe observation admitted: %v", err)
	}
	f.session.observed.Info.Streams[0].ExternalURL = ""
	f.session.observed.Decision.Reason = strings.Repeat("x", MaxMessage)
	if _, err := s.Observe(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("oversize observation admitted: %v", err)
	}
	f.session.observed.Decision.Reason = ""
	// Missing optional methods cannot be replaced with invented zero values.
	w.mu.Lock()
	e := w.records[s.id]
	backend := e.session
	e.session = observationHiddenSession{backend}
	w.mu.Unlock()
	if _, err := s.Observe(context.Background()); !errors.Is(err, ErrUnsupported) {
		t.Fatal("missing backend contracts supplied defaults", err)
	}
	w.mu.Lock()
	e.session = backend
	w.mu.Unlock()
	if _, ok := s.adapter.Session(s.ID()); !ok || s.EncoderErr() != core.ErrEncoderPending {
		t.Fatal("inspection failure discarded ownership")
	}
	if _, ok := any(s).(core.RecoveryBufferSource); ok {
		t.Fatal("live session claims no-error recovery getters")
	}
}
func durationPtr(d time.Duration) *time.Duration { return &d }
