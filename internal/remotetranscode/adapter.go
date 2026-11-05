package remotetranscode

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type Assignment struct {
	Identity Identity
	Grant    string
	Expires  time.Time
}
type AdapterOptions struct {
	Endpoint string
	Client   *http.Client // Transport must be *http.Transport with verified mutual TLS.
	Assign   func(context.Context, core.HLSRequest) (Assignment, error)
	Decide   func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision
	Hardware core.HWAccel
	Capacity int
}
type Adapter struct {
	opts     AdapterOptions
	client   *http.Client
	mu       sync.Mutex
	sessions map[Identity]*Session
}

func NewAdapter(o AdapterOptions) (*Adapter, error) {
	u, e := url.Parse(o.Endpoint)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || o.Client == nil || o.Assign == nil || o.Decide == nil || o.Capacity < 1 {
		return nil, errors.New("remotetranscode: explicit adapter dependencies required")
	}
	transport, ok := o.Client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		return nil, errors.New("remotetranscode: mutual TLS transport required")
	}
	tc := transport.TLSClientConfig
	if tc.InsecureSkipVerify || tc.RootCAs == nil || tc.VerifyConnection == nil || (len(tc.Certificates) == 0 && tc.GetClientCertificate == nil) {
		return nil, errors.New("remotetranscode: verified mutual TLS required")
	}
	t := transport.Clone()
	t.TLSClientConfig = tc.Clone()
	if t.TLSClientConfig.MinVersion < tls.VersionTLS12 {
		t.TLSClientConfig.MinVersion = tls.VersionTLS12
	}
	t.Proxy = nil
	client := *o.Client
	client.Transport = t
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Adapter{opts: o, client: &client, sessions: make(map[Identity]*Session)}, nil
}
func (a *Adapter) Capabilities() Capabilities                             { return Supported() }
func (a *Adapter) Probe(context.Context, string) (*core.MediaInfo, error) { return nil, ErrUnsupported }
func (a *Adapter) Decide(i core.MediaInfo, c core.ClientCaps, o core.DecideOptions) core.Decision {
	return a.opts.Decide(i, c, o)
}
func (a *Adapter) HWAccel() core.HWAccel { return a.opts.Hardware }
func (a *Adapter) StartHLS(ctx context.Context, r core.HLSRequest) (core.HLSSession, error) {
	s, _, e := a.StartHLSOwned(ctx, r)
	return s, e
}
func (a *Adapter) StartHLSOwned(ctx context.Context, r core.HLSRequest) (core.HLSSession, core.EncoderRetirement, error) {
	if !supported(r) || !token(r.SessionID) {
		return nil, nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	assignment, err := a.opts.Assign(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	if !validIdentity(assignment.Identity) || assignment.Identity.Session != r.SessionID || len(assignment.Grant) == 0 || len(assignment.Grant) > 4096 || !assignment.Expires.After(time.Now()) {
		return nil, nil, errors.New("remotetranscode: invalid assignment")
	}
	start := Start{ReleaseID: r.ReleaseID, Identity: assignment.Identity, Grant: assignment.Grant, Expires: assignment.Expires, Info: r.Info, Decision: r.Decision, Offset: r.StartOffset, SegmentDuration: r.SegmentDuration, IndependentProof: r.RequireIndependentProof}
	b, err := json.Marshal(start)
	if err != nil || len(b) > MaxMessage-1024 {
		return nil, nil, ErrUnsupported
	}
	a.mu.Lock()
	s := a.sessions[assignment.Identity]
	if s != nil && !bytes.Equal(s.start, b) {
		a.mu.Unlock()
		return nil, nil, errors.New("remotetranscode: assignment conflict")
	}
	if s == nil {
		if len(a.sessions) >= a.opts.Capacity {
			a.mu.Unlock()
			return nil, nil, errors.New("remotetranscode: adapter capacity")
		}
		s = &Session{adapter: a, id: assignment.Identity, start: b, done: make(chan struct{})}
		a.sessions[s.id] = s
	}
	a.mu.Unlock()
	response, err := a.call(ctx, message{Op: "start", Identity: s.id, Start: &start}, nil)
	if err != nil {
		return nil, s, ErrUncertain
	}
	defer response.Body.Close()
	if s.observe(response) != nil {
		return nil, s, ErrUncertain
	}
	s.mu.Lock()
	running := s.state == "running"
	s.mu.Unlock()
	if !running {
		return nil, s, ErrUncertain
	}
	s.touch()
	return s, s, nil
}
func (a *Adapter) Session(id string) (core.HLSSession, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var newest *Session
	for _, s := range a.sessions {
		if s.id.Session == id && (newest == nil || s.id.Generation > newest.id.Generation) {
			newest = s
		}
	}
	if newest == nil {
		return nil, false
	}
	newest.mu.Lock()
	defer newest.mu.Unlock()
	return newest, newest.state == "running" && !newest.stopped
}
func (a *Adapter) Stop(id string) {
	a.mu.Lock()
	var latest *Session
	for _, s := range a.sessions {
		if s.id.Session == id && (latest == nil || s.id.Generation > latest.id.Generation) {
			latest = s
		}
	}
	a.mu.Unlock()
	if latest != nil {
		_ = latest.RetireEncoder()
	}
}

// Close closes idle transport connections only; owned executions must be stopped
// and reconciled explicitly. A connection close never proves encoder retirement.
func (a *Adapter) Close() { a.client.CloseIdleConnections() }
func (a *Adapter) call(ctx context.Context, m message, headers http.Header) (*http.Response, error) {
	m.Version = Version
	b, err := json.Marshal(m)
	if err != nil || len(b) > MaxMessage {
		return nil, ErrUnsupported
	}
	req, err := http.NewRequestWithContext(ctx, "POST", a.opts.Endpoint, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		if v := headers.Values(h); len(v) > 0 {
			req.Header[h] = v
		}
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, ErrUncertain
	}
	if m.Op != "playlist" && m.Op != "segment" && (resp.StatusCode < 200 || resp.StatusCode >= 300) {
		resp.Body.Close()
		return nil, ErrUncertain
	}
	return resp, nil
}

type Session struct {
	adapter  *Adapter
	id       Identity
	start    []byte
	mu       sync.Mutex
	done     chan struct{}
	state    string
	terminal error
	stopped  bool
	access   atomic.Int64
}

func (s *Session) ID() string                                { return s.id.Session }
func (s *Session) LastAccess() time.Time                     { return time.Unix(0, s.access.Load()) }
func (s *Session) touch()                                    { s.access.Store(time.Now().UnixNano()) }
func (s *Session) EncoderRetirement() core.EncoderRetirement { return s }
func (s *Session) EncoderDone() <-chan struct{}              { return s.done }
func (s *Session) EncoderErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return s.terminal
	default:
		return core.ErrEncoderPending
	}
}
func (s *Session) SwapInput(context.Context, string, core.MediaInfo, time.Duration) error {
	return ErrUnsupported
}
func (s *Session) Close() error { return s.RetireEncoder() }
func (s *Session) RetireEncoder() error {
	s.mu.Lock()
	s.stopped = true
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, e := s.adapter.call(ctx, message{Op: "stop", Identity: s.id}, nil)
	if e != nil {
		return ErrUncertain
	}
	defer r.Body.Close()
	if e = s.observe(r); e != nil {
		return e
	}
	return s.EncoderErr()
}

// Reconcile obtains proof for this exact execution after a failed/late start,
// disconnect or expiry. No polling goroutine or timeout manufactures proof.
func (s *Session) Reconcile(ctx context.Context) error {
	r, e := s.adapter.call(ctx, message{Op: "status", Identity: s.id}, nil)
	if e != nil {
		return e
	}
	defer r.Body.Close()
	if e = s.observe(r); e != nil {
		return e
	}
	return s.EncoderErr()
}
func (s *Session) observe(r *http.Response) error {
	var st status
	d := json.NewDecoder(io.LimitReader(r.Body, 1025))
	if d.Decode(&st) != nil || d.Decode(new(any)) != io.EOF {
		return ErrUncertain
	}
	if st.Identity != s.id {
		return ErrUncertain
	}
	if st.State != "running" && st.State != "pending" && st.State != "retired" && st.State != "unverified" {
		return ErrUncertain
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.done:
		return nil
	default:
	}
	s.state = st.State
	if st.State == "retired" || st.State == "unverified" {
		if st.State == "unverified" {
			s.terminal = core.ErrEncoderInputUnverified
		}
		close(s.done)
	}
	return nil
}
func (s *Session) ServePlaylist(w http.ResponseWriter, r *http.Request) { s.serve(w, r, "playlist", 0) }
func (s *Session) ServeSegment(w http.ResponseWriter, r *http.Request, n int) {
	s.serve(w, r, "segment", n)
}
func (s *Session) serve(w http.ResponseWriter, r *http.Request, op string, n int) {
	if n < -1 || n > 10000000 {
		http.Error(w, "invalid segment", 400)
		return
	}
	s.mu.Lock()
	blocked := s.stopped
	s.mu.Unlock()
	if blocked {
		http.Error(w, "execution stopped", 410)
		return
	}
	response, err := s.adapter.call(r.Context(), message{Op: op, Identity: s.id, Segment: n, Head: r.Method == http.MethodHead}, r.Header)
	if err != nil {
		http.Error(w, "remote execution unavailable", 502)
		return
	}
	defer response.Body.Close()
	s.touch()
	// Preserve representation/range/cache headers, never cookies or peer secrets.
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Cache-Control", "ETag", "Last-Modified", "Content-Disposition"} {
		if v := response.Header.Values(h); len(v) > 0 {
			w.Header()[h] = v
		}
	}
	w.WriteHeader(response.StatusCode)
	// io.Copy applies downstream backpressure; request context cancels upstream.
	_, _ = io.Copy(w, response.Body)
}

var _ core.Transcoder = (*Adapter)(nil)
var _ core.OwnedTranscoder = (*Adapter)(nil)
var _ core.OwnedEncoderSession = (*Session)(nil)
