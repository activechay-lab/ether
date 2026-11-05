package remotetranscode

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type WorkerOptions struct {
	ValidateRenewal ValidateRenewal // Nil explicitly disables renewal.
	Boot            string
	Capacity        int // Includes all retained tombstones; never evicts unresolved work.
	MaxLease        time.Duration
	Security        Security
	Resolve         ResolveSource
	Encoder         core.OwnedTranscoder
}
type execution struct {
	start                     Start // Immutable original grant and expiry, separate from current lease.
	lease                     LeaseState
	id                        Identity
	hash                      [32]byte
	session                   core.OwnedEncoderSession
	retirement                core.EncoderRetirement
	input                     core.EncoderInputLease
	starting, stopped, failed bool
	cancel                    context.CancelFunc
	timer                     *time.Timer
}
type Worker struct {
	mu      sync.Mutex
	opts    WorkerOptions
	records map[Identity]*execution
	highest map[string]uint64
	closed  bool
}

func NewWorker(o WorkerOptions) (*Worker, error) {
	if !token(o.Boot) || o.Capacity < 1 || o.MaxLease <= 0 || o.MaxLease > 10*time.Minute || o.Security.Authenticate == nil || o.Security.Authorize == nil || o.Resolve == nil || o.Encoder == nil {
		return nil, errors.New("remotetranscode: explicit worker dependencies required")
	}
	return &Worker{opts: o, records: make(map[Identity]*execution), highest: make(map[string]uint64)}, nil
}

// Close stops admission and requests retirement of every exact execution. It does
// not claim completion; callers must reconcile pending records before exit.
func (w *Worker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	for _, e := range w.records {
		w.stop(e)
	}
	return nil
}

// stop is called under mu. OwnedEncoderSession.RetireEncoder is bounded by contract.
func (w *Worker) stop(e *execution) {
	e.stopped = true
	if e.timer != nil {
		e.timer.Stop()
	}
	if e.cancel != nil {
		e.cancel()
	}
	if e.input != nil {
		e.input.SealInput()
	}
	if e.session != nil {
		_ = e.session.RetireEncoder()
	}
}
func result(e *execution) string {
	if e.starting {
		return "pending"
	}
	if e.retirement != nil {
		select {
		case <-e.retirement.EncoderDone():
			if e.retirement.EncoderErr() != nil {
				return "unverified"
			}
		default:
			if !e.stopped && !e.failed {
				return "running"
			}
			return "pending"
		}
	} else if !e.stopped {
		return "pending"
	}
	if e.input != nil {
		select {
		case <-e.input.InputDone():
			if e.input.InputErr() != nil {
				return "unverified"
			}
		default:
			return "pending"
		}
	}
	return "retired"
}
func (w *Worker) ServeHTTP(out http.ResponseWriter, r *http.Request) {
	// TLS verification is mandatory even when an injected callback is permissive.
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 {
		http.Error(out, "peer authentication required", 401)
		return
	}
	peer, err := w.opts.Security.Authenticate(r)
	if err != nil || !token(peer) {
		http.Error(out, "peer authentication required", 401)
		return
	}
	if r.Method != "POST" {
		http.Error(out, "method", 405)
		return
	}
	var m message
	dec := json.NewDecoder(http.MaxBytesReader(out, r.Body, MaxMessage))
	dec.DisallowUnknownFields()
	if dec.Decode(&m) != nil || dec.Decode(new(any)) != io.EOF || m.Version != Version || !validIdentity(m.Identity) || m.Identity.Worker != w.opts.Boot || peer != m.Identity.Controller {
		http.Error(out, "invalid request", 400)
		return
	}
	if m.Op != "renew" && m.Op != "start" && m.Op != "stop" && m.Op != "status" && m.Op != "playlist" && m.Op != "segment" {
		http.Error(out, "unsupported operation", 422)
		return
	}
	if m.Op == "start" {
		if m.Start == nil || len(m.Start.ReleaseID) > 512 || m.Start.Identity != m.Identity || len(m.Start.Grant) == 0 || len(m.Start.Grant) > 4096 || !validPlan(m.Start.Info, m.Start.Decision, m.Start.Offset, m.Start.SegmentDuration) {
			http.Error(out, "unsupported start", 422)
			return
		}
	} else if m.Start != nil {
		http.Error(out, "invalid request", 400)
		return
	}
	if m.Op == "renew" {
		if m.Renewal == nil || m.Renewal.Identity != m.Identity || !validRenewal(*m.Renewal) {
			http.Error(out, "invalid renewal", 400)
			return
		}
	} else if m.Renewal != nil {
		http.Error(out, "invalid request", 400)
		return
	}
	if m.Segment < -1 || m.Segment > 10000000 {
		http.Error(out, "invalid segment", 400)
		return
	}
	if w.opts.Security.Authorize(r.Context(), peer, m.Op, m.Identity, m.Start) != nil {
		http.Error(out, "operation denied", 403)
		return
	}
	if m.Op == "renew" {
		w.renew(out, r, peer, *m.Renewal)
		return
	}
	if m.Op == "start" {
		w.start(out, r, peer, *m.Start)
		return
	}
	w.mu.Lock()
	e := w.records[m.Identity]
	if e == nil && m.Op == "stop" && len(w.records) < w.opts.Capacity && !w.closed {
		// Stop-before-start is a permanent tombstone for this generation.
		e = &execution{id: m.Identity, stopped: true}
		w.records[m.Identity] = e
		key := m.Identity.Controller + "/" + m.Identity.Session
		if m.Identity.Generation > w.highest[key] {
			w.highest[key] = m.Identity.Generation
		}
	}
	if e == nil {
		w.mu.Unlock()
		http.Error(out, "unknown execution", 409)
		return
	}
	w.expire(e)
	if m.Op == "stop" {
		w.stop(e)
	}
	state := result(e)
	s := e.session
	if m.Op == "status" || m.Op == "stop" {
		snapshot := w.snapshot(e)
		w.mu.Unlock()
		replyStatus(out, snapshot)
		return
	}
	if e.stopped || state != "running" || s == nil {
		w.mu.Unlock()
		http.Error(out, "execution unavailable", 409)
		return
	}
	w.mu.Unlock()
	// Only these request headers reach the local session, never caller credentials.
	request := r.Clone(r.Context())
	request.Method = "GET"
	if m.Head {
		request.Method = "HEAD"
	}
	request.Header = make(http.Header)
	for _, h := range []string{"Range", "If-Range", "If-None-Match", "If-Modified-Since"} {
		request.Header[h] = r.Header.Values(h)
	}
	if m.Op == "playlist" {
		s.ServePlaylist(out, request)
	} else {
		s.ServeSegment(out, request, m.Segment)
	}
}
func reply(out http.ResponseWriter, state string, id Identity) {
	out.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(out).Encode(status{State: state, Identity: id})
}
func (w *Worker) start(out http.ResponseWriter, r *http.Request, peer string, s Start) {
	encoded, _ := json.Marshal(s)
	hash := sha256.Sum256(encoded)
	w.mu.Lock()
	if e := w.records[s.Identity]; e != nil {
		w.expire(e)
		snapshot := w.snapshot(e)
		same := e.hash == hash && !e.stopped
		w.mu.Unlock()
		if !same {
			http.Error(out, "generation conflict", 409)
			return
		}
		replyStatus(out, snapshot)
		return
	}
	key := s.Identity.Controller + "/" + s.Identity.Session
	if w.closed || len(w.records) >= w.opts.Capacity || s.Identity.Generation <= w.highest[key] || !s.Expires.After(time.Now()) || time.Until(s.Expires) > w.opts.MaxLease {
		w.mu.Unlock()
		http.Error(out, "admission denied", 409)
		return
	}
	// A replacement cannot overlap any unresolved prior encoder for this session.
	for _, old := range w.records {
		if old.id.Controller == s.Identity.Controller && old.id.Session == s.Identity.Session && result(old) != "retired" {
			w.mu.Unlock()
			http.Error(out, "prior execution unresolved", 409)
			return
		}
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	e := &execution{start: s, lease: LeaseState{Deadline: s.Expires.UTC(), Renewable: w.opts.ValidateRenewal != nil}, id: s.Identity, hash: hash, starting: true, cancel: cancel}
	w.records[s.Identity] = e
	w.highest[key] = s.Identity.Generation
	w.arm(e)
	w.mu.Unlock()
	// Request cancellation seals a late start, but never forgets its responsibility.
	disconnect := context.AfterFunc(r.Context(), func() { w.mu.Lock(); defer w.mu.Unlock(); w.stop(e) })
	defer disconnect()
	source, err := w.opts.Resolve(ctx, peer, s)
	var session core.HLSSession
	var retirement core.EncoderRetirement
	invoked := false
	if err == nil && (!privateURL(source.URL) || source.Lease == nil) {
		err = errors.New("invalid authorized source")
	}
	w.mu.Lock()
	e.input = source.Lease
	w.expire(e)
	stopped := e.stopped
	w.mu.Unlock()
	if stopped {
		err = context.Canceled
	}
	if err == nil {
		invoked = true
		session, retirement, err = w.opts.Encoder.StartHLSOwned(ctx, core.HLSRequest{ReleaseID: s.ReleaseID, SessionID: executionID(s.Identity), InputURL: source.URL, InputLease: source.Lease, Info: s.Info, Decision: s.Decision, StartOffset: s.Offset, SegmentDuration: s.SegmentDuration, RequireIndependentProof: s.IndependentProof, Protocol: core.ProtocolHLS})
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.expire(e)
	e.starting = false
	e.retirement = retirement
	if session != nil {
		e.session, _ = session.(core.OwnedEncoderSession)
		if e.session == nil {
			_ = session.Close()
			err = ErrUncertain
		}
	}
	if retirement == nil && invoked {
		e.failed = true
		e.starting = true /* no proof: permanently pending */
	}
	if err != nil || e.stopped || ctx.Err() != nil {
		e.failed = true
		w.stop(e)
	}
	if err == nil && e.session == nil {
		e.failed = true
		w.stop(e)
	}
	if e.failed {
		http.Error(out, "start failed; reconcile execution", 409)
		return
	}
	replyStatus(out, w.snapshot(e))
}
func executionID(i Identity) string {
	b, _ := json.Marshal(i)
	h := sha256.Sum256(b)
	const hex = "0123456789abcdef"
	out := make([]byte, 64)
	for j, v := range h {
		out[j*2] = hex[v>>4]
		out[j*2+1] = hex[v&15]
	}
	return string(out)
}

// All lease transitions and deadline checks run under mu. Timer.Stop alone is
// insufficient: an already queued old callback must compare the accepted lease.
func (w *Worker) expire(e *execution) {
	if !e.lease.Deadline.IsZero() && !time.Now().Before(e.lease.Deadline) {
		w.stop(e)
	}
}
func (w *Worker) arm(e *execution) {
	if e.timer != nil {
		e.timer.Stop()
	}
	version, deadline := e.lease.Version, e.lease.Deadline
	e.timer = time.AfterFunc(time.Until(deadline), func() { w.expireVersion(e, version, deadline) })
}
func (w *Worker) expireVersion(e *execution, version uint64, deadline time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if e.lease.Version == version && e.lease.Deadline.Equal(deadline) {
		w.expire(e)
	}
}
func (w *Worker) snapshot(e *execution) status {
	return status{State: result(e), Identity: e.id, Lease: e.lease, Stopped: e.stopped}
}
func replyStatus(out http.ResponseWriter, s status) {
	out.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(out).Encode(s)
}
func (w *Worker) renew(out http.ResponseWriter, r *http.Request, peer string, n Renewal) {
	if w.opts.ValidateRenewal == nil {
		http.Error(out, "renewal unsupported", 422)
		return
	}
	w.mu.Lock()
	e := w.records[n.Identity]
	if e == nil {
		w.mu.Unlock()
		http.Error(out, "unknown execution", 409)
		return
	}
	w.expire(e)
	// An exact latest replay reports history even after stop, without new authority.
	if e.lease.Accepted != nil && sameRenewal(*e.lease.Accepted, n) {
		snapshot := w.snapshot(e)
		w.mu.Unlock()
		replyStatus(out, snapshot)
		return
	}
	if !w.canRenew(e, n) {
		w.mu.Unlock()
		http.Error(out, "lease conflict", 409)
		return
	}
	original := e.start
	w.mu.Unlock()
	receipt, err := w.opts.ValidateRenewal(r.Context(), peer, original, n)
	if err != nil || !sameRenewal(receipt.Renewal, n) || receipt.PlacementUntil.Before(n.Deadline) || receipt.SourceUntil.Before(n.Deadline) {
		http.Error(out, "renewal authority denied", 403)
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.expire(e)
	// Concurrent duplicate validation may complete after this operation committed.
	if e.lease.Accepted != nil && sameRenewal(*e.lease.Accepted, n) {
		replyStatus(out, w.snapshot(e))
		return
	}
	if r.Context().Err() != nil || !w.canRenew(e, n) {
		http.Error(out, "lease conflict", 409)
		return
	}
	accepted := n
	e.lease.Deadline, e.lease.Version, e.lease.Accepted = n.Deadline.UTC(), n.Operation, &accepted
	w.arm(e)
	replyStatus(out, w.snapshot(e))
}
func (w *Worker) canRenew(e *execution, n Renewal) bool {
	now := time.Now()
	return !w.closed && !e.stopped && !e.failed && !e.starting && e.lease.Deadline.After(now) && result(e) == "running" &&
		e.lease.Version == n.PreviousVersion && e.lease.Deadline.Equal(n.PreviousDeadline) &&
		n.Deadline.After(now) && n.Deadline.Sub(now) <= w.opts.MaxLease
}
