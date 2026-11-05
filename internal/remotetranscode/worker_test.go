package remotetranscode

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type fakeLease struct {
	done chan struct{}
	once sync.Once
}

func (l *fakeLease) ClaimInput(func(error)) error                      { return nil }
func (l *fakeLease) HeadersForInput(string) (map[string]string, error) { return nil, nil }
func (l *fakeLease) SealInput()                                        { l.once.Do(func() { close(l.done) }) }
func (l *fakeLease) InputDone() <-chan struct{}                        { return l.done }
func (l *fakeLease) InputErr() error {
	select {
	case <-l.done:
		return nil
	default:
		return core.ErrEncoderInputPending
	}
}

type fakeSession struct {
	observed observation
	soft     *int
	touched  bool
	playback string
	done     chan struct{}
	stopped  bool
	input    *fakeLease
}

func (s *fakeSession) ID() string { return "backend" }
func (s *fakeSession) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Write([]byte("#EXTM3U\n"))
}
func (s *fakeSession) ServeSegment(w http.ResponseWriter, r *http.Request, n int) {
	w.Header().Set("Content-Range", "bytes 0-2/3")
	w.WriteHeader(206)
	w.Write([]byte("abc"))
}
func (s *fakeSession) SwapInput(context.Context, string, core.MediaInfo, time.Duration) error {
	return nil
}
func (s *fakeSession) LastAccess() time.Time                     { return time.Now() }
func (s *fakeSession) Close() error                              { return s.RetireEncoder() }
func (s *fakeSession) RetireEncoder() error                      { s.stopped = true; return nil }
func (s *fakeSession) EncoderRetirement() core.EncoderRetirement { return s }
func (s *fakeSession) EncoderDone() <-chan struct{}              { return s.done }
func (s *fakeSession) EncoderErr() error {
	select {
	case <-s.done:
		return nil
	default:
		return core.ErrEncoderPending
	}
}

type fakeEncoder struct {
	calls            int
	request          core.HLSRequest
	session          *fakeSession
	entered, release chan struct{}
}

func (f *fakeEncoder) StartHLSOwned(ctx context.Context, r core.HLSRequest) (core.HLSSession, core.EncoderRetirement, error) {
	f.calls++
	f.request = r
	if f.entered != nil {
		close(f.entered)
		<-f.release
	}
	f.session = &fakeSession{soft: r.SoftSubtitleStream, done: make(chan struct{}), input: r.InputLease.(*fakeLease)}
	return f.session, f.session, nil
}
func fixture(t *testing.T) (*Worker, *fakeEncoder, Start) {
	t.Helper()
	f := &fakeEncoder{}
	w, e := NewWorker(WorkerOptions{Boot: "worker-boot", Capacity: 16, MaxLease: time.Minute, Encoder: f, Security: Security{
		Authenticate: func(r *http.Request) (string, error) {
			if r.Header.Get("Peer") == "unknown" {
				return "", errors.New("unknown")
			}
			return "controller-boot", nil
		},
		Authorize: func(ctx context.Context, p, op string, id Identity, s *Start) error {
			if id.Session == "denied" {
				return errors.New("denied")
			}
			return nil
		},
	}, Resolve: func(context.Context, string, Start) (Source, error) {
		return Source{URL: "https://10.0.0.2/input", Lease: &fakeLease{done: make(chan struct{})}}, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { w.Close() })
	return w, f, Start{Identity: Identity{Controller: "controller-boot", Worker: "worker-boot", Session: "session", Generation: 1}, Grant: "opaque", Expires: time.Now().Add(30 * time.Second), Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1}}
}
func rpc(w *Worker, op string, s Start, tlsOK bool) *httptest.ResponseRecorder {
	m := message{Version: Version, Op: op, Identity: s.Identity}
	if op == "start" {
		m.Start = &s
	}
	b, _ := json.Marshal(m)
	r := httptest.NewRequest("POST", "https://worker/private", bytes.NewReader(b))
	if tlsOK {
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
	}
	out := httptest.NewRecorder()
	w.ServeHTTP(out, r)
	return out
}
func state(t *testing.T, r *httptest.ResponseRecorder) string {
	t.Helper()
	if r.Code != 200 {
		t.Fatalf("status=%d body=%s", r.Code, r.Body)
	}
	var s status
	if json.Unmarshal(r.Body.Bytes(), &s) != nil {
		t.Fatal("bad status")
	}
	return s.State
}
func TestSecurityAndFencing(t *testing.T) {
	w, f, s := fixture(t)
	if rpc(w, "start", s, false).Code != 401 {
		t.Fatal("plaintext accepted")
	}
	for _, op := range []string{"start", "status", "stop", "playlist", "segment"} {
		bad := s
		bad.Identity.Session = "denied"
		if rpc(w, op, bad, true).Code != 403 {
			t.Fatal("authorization bypass", op)
		}
	}
	wrong := s
	wrong.Identity.Worker = "old-boot"
	if rpc(w, "start", wrong, true).Code != 400 {
		t.Fatal("wrong boot")
	}
	if f.calls != 0 {
		t.Fatal("rejected work started")
	}
	if state(t, rpc(w, "start", s, true)) != "running" {
		t.Fatal("not running")
	}
	if state(t, rpc(w, "start", s, true)) != "running" || f.calls != 1 {
		t.Fatal("duplicate started")
	}
	if state(t, rpc(w, "stop", s, true)) != "pending" {
		t.Fatal("stop manufactured proof")
	}
	replacement := s
	replacement.Identity.Generation = 2
	if rpc(w, "start", replacement, true).Code != 409 {
		t.Fatal("replacement overlaps")
	}
	close(f.session.done)
	if state(t, rpc(w, "status", s, true)) != "retired" {
		t.Fatal("proof missing")
	}
	if state(t, rpc(w, "start", replacement, true)) != "running" {
		t.Fatal("replacement denied")
	}
	current := f.session
	_ = rpc(w, "stop", s, true)
	if current.stopped {
		t.Fatal("stale stop killed replacement")
	}
	if rpc(w, "start", s, true).Code != 409 {
		t.Fatal("late start replay")
	}
}
func TestLateStartStopAndExpiry(t *testing.T) {
	w, f, s := fixture(t)
	f.entered = make(chan struct{})
	f.release = make(chan struct{})
	finished := make(chan *httptest.ResponseRecorder, 1)
	go func() { finished <- rpc(w, "start", s, true) }()
	<-f.entered
	if state(t, rpc(w, "stop", s, true)) != "pending" {
		t.Fatal("late start retired early")
	}
	close(f.release)
	if (<-finished).Code != 409 {
		t.Fatal("late start published")
	}
	if !f.session.stopped {
		t.Fatal("late encoder not stopped")
	}
	close(f.session.done)
	if state(t, rpc(w, "status", s, true)) != "retired" {
		t.Fatal("late retirement missing")
	}
	// Stop arriving before start is an irreversible tombstone.
	next := s
	next.Identity.Generation = 2
	if state(t, rpc(w, "stop", next, true)) != "retired" {
		t.Fatal("stop tombstone")
	}
	if rpc(w, "start", next, true).Code != 409 {
		t.Fatal("stopped generation started")
	}
}
func TestAdapterAdmissionAndUncertainProof(t *testing.T) {
	a := &Adapter{}
	for _, r := range []core.HLSRequest{{Protocol: core.ProtocolDASH}, {LiveInput: true}, {InputHeaders: map[string]string{"X": "secret"}}} {
		if _, proof, e := a.StartHLSOwned(context.Background(), r); !errors.Is(e, ErrUnsupported) || proof != nil {
			t.Fatal("unsupported admission")
		}
	}
	s := &Session{done: make(chan struct{})}
	if s.EncoderErr() != core.ErrEncoderPending {
		t.Fatal("optimistic proof")
	}
	r := httptest.NewRecorder()
	reply(r, "pending", s.id)
	if s.observe(r.Result()) != nil || s.EncoderErr() != core.ErrEncoderPending {
		t.Fatal("pending proof")
	}
	r = httptest.NewRecorder()
	reply(r, "retired", s.id)
	if s.observe(r.Result()) != nil || s.EncoderErr() != nil {
		t.Fatal("retirement proof")
	}
}
