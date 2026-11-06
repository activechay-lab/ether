// Package nodeinput composes verified Go source transport with worker-local
// encoder input. It supplies no remote source retirement proof.
package nodeinput

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/generationfence"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

const sourceRangeBytes = 2 << 20

var ErrDenied = errors.New("nodeinput: unavailable input")

type Options struct {
	// ValidateScope checks current execution generation at the local byte consumer.
	ValidateScope         func(context.Context, nodesource.Scope) error
	WatchAuthority        func(string, func()) func()
	Client                *nodesource.Client
	Recover               func(context.Context, nodesource.Grant) (*nodesource.Client, nodesource.Grant, error)
	ReadTimeout           time.Duration
	MaxRecoveries         int
	Capacity, MaxRequests int
	RequestTimeout        time.Duration
}

// Gateway owns its loopback listener. Host runs Serve and drives Reconcile even
// during drain. Capacity includes all retained admissions, including failed opens;
// records are never evicted during this gateway lifetime.
type Gateway struct {
	mu       sync.Mutex
	o        Options
	listener net.Listener
	server   *http.Server
	entries  map[string]*Input
	closed   bool
	active   int
}

// Input is an opaque, exact lease and trusted gateway capability. Never serialize
// it. String formatting deliberately hides its URL, grant, and scope.
type Input struct {
	readMu                    sync.RWMutex
	grant                     nodesource.Grant
	recoveries                int
	ownedClients              []*nodesource.Client
	g                         *Gateway
	id                        remotetranscode.Identity
	handle                    string
	scope                     nodesource.Scope
	size                      int64
	url, key                  string
	reader                    *nodesource.Reader
	ctx                       context.Context
	cancel                    context.CancelFunc
	tracker                   core.ReadRetirement
	drained                   <-chan struct{}
	done                      chan struct{}
	sealed, claimed, complete bool
	callback                  func(error)
	renewal                   *remotetranscode.Renewal
	attempt                   *remotetranscode.Renewal
	renewing                  bool
	initialDeadline           time.Time
	sourceUntil               time.Time
	deadline                  time.Time
	timer                     *time.Timer
}

func (*Gateway) String() string   { return "nodeinput.Gateway[redacted]" }
func (*Gateway) GoString() string { return "nodeinput.Gateway[redacted]" }
func (*Input) String() string     { return "nodeinput.Input[redacted]" }
func (*Input) GoString() string   { return "nodeinput.Input[redacted]" }

func New(o Options) (*Gateway, error) {
	if o.Client == nil || o.Capacity < 1 || o.Capacity > 4096 || o.MaxRequests < 1 || o.MaxRequests > 4096 || o.RequestTimeout <= 0 || o.RequestTimeout > time.Minute {
		return nil, ErrDenied
	}
	if o.ReadTimeout == 0 {
		o.ReadTimeout = o.RequestTimeout
	}
	if o.ReadTimeout <= 0 || o.ReadTimeout > o.RequestTimeout || o.MaxRecoveries < 0 || o.MaxRecoveries > 8 {
		return nil, ErrDenied
	}
	if o.Recover != nil && o.MaxRecoveries == 0 {
		o.MaxRecoveries = 3
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("nodeinput: loopback bind failed")
	}
	g := &Gateway{o: o, listener: &limitedListener{Listener: l, max: o.MaxRequests}, entries: make(map[string]*Input)}
	g.server = &http.Server{Handler: g, ReadHeaderTimeout: o.RequestTimeout, ReadTimeout: o.RequestTimeout, WriteTimeout: 0, IdleTimeout: o.RequestTimeout, MaxHeaderBytes: 8192, ErrorLog: log.New(io.Discard, "", 0)}
	return g, nil
}

// Serve blocks until Close or host cancellation. No internal polling/renewal loop.
func (g *Gateway) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = g.Close() })
	defer stop()
	err := g.server.Serve(g.listener)
	if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// Close seals all admissions, cancels reads, closes sockets (including blocked
// writers), and returns without waiting for producers. Reconcile retains proof.
func (g *Gateway) Close() error {
	g.mu.Lock()
	g.closed = true
	for _, in := range g.entries {
		in.sealLocked()
	}
	g.mu.Unlock()
	_ = g.listener.Close()
	return g.server.Close()
}

// Admit opens one exact Reader itself: callers cannot substitute a Reader/source.
// A nonnil Input is responsibility even on error; SealInput and Reconcile it.
// Authority expiry must cover Start on the composing host's conservative clock
// policy. This local comparison does not establish cross-host clock agreement.
func (g *Gateway) Admit(ctx context.Context, s remotetranscode.Start, grant nodesource.Grant, size int64) (*Input, error) {
	f := grant.Scope.Fence
	if f.Generation <= 0 || s.Identity.Generation != uint64(f.Generation) || s.Identity.Controller != f.Controller.Boot || s.Identity.Worker != f.Target.Boot || s.Identity.Session != f.Session || s.Grant == "" || size < 0 || s.Expires.IsZero() || grant.Expires.Before(s.Expires) {
		return nil, ErrDenied
	}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, ErrDenied
	}
	key := hex.EncodeToString(random[:])
	life, cancel := context.WithCancel(context.Background())
	in := &Input{grant: grant, g: g, id: s.Identity, handle: s.Grant, scope: grant.Scope, size: size, key: key, initialDeadline: s.Expires, ctx: life, cancel: cancel, done: make(chan struct{})}
	in.url = "http://" + g.listener.Addr().String() + "/input/" + key
	g.mu.Lock()
	if g.closed || len(g.entries) >= g.o.Capacity {
		g.mu.Unlock()
		cancel()
		return nil, ErrDenied
	}
	g.entries[key] = in
	in.armLocked(s.Expires)
	end, _ := in.tracker.Begin() // Registered before Open, including its error path.
	g.mu.Unlock()
	combined, cancelOpen := context.WithCancel(ctx)
	stop := context.AfterFunc(life, cancelOpen)
	reader, err := g.o.Client.Open(combined, grant)
	stop()
	cancelOpen()
	g.mu.Lock()
	in.reader = reader
	if err != nil || reader.Size() != size || in.sealed {
		in.sealLocked()
		err = ErrDenied
	}
	g.mu.Unlock()
	end()
	if err != nil {
		return in, ErrDenied
	}
	return in, nil
}

// GatewayInput binds the exact remote execution and stable authority handle.
func (in *Input) GatewayInput(id remotetranscode.Identity, handle string) (string, core.EncoderInputLease, error) {
	in.g.mu.Lock()
	defer in.g.mu.Unlock()
	if in.id != id || in.handle != handle || in.sealed || in.reader == nil {
		return "", in, ErrDenied
	}
	return in.url, in, nil
}
func (in *Input) ClaimInput(callback func(error)) error {
	in.g.mu.Lock()
	defer in.g.mu.Unlock()
	if in.claimed || in.sealed || callback == nil {
		return ErrDenied
	}
	in.claimed = true
	in.callback = callback
	return nil
}
func (in *Input) HeadersForInput(raw string) (map[string]string, error) {
	in.g.mu.Lock()
	defer in.g.mu.Unlock()
	if raw != in.url || in.sealed || !in.claimed {
		return nil, ErrDenied
	}
	// Local scope credential activates existing encoder command-log redaction.
	return map[string]string{core.EncoderInputScopeHeader: in.key}, nil
}
func (in *Input) sealLocked() {
	if !in.sealed {
		in.sealed = true
		if in.timer != nil {
			in.timer.Stop()
		}
		in.cancel()
		in.drained = in.tracker.Seal()
	}
	if in.reader != nil {
		_ = in.reader.Close()
	}
}
func (in *Input) SealInput()                 { in.g.mu.Lock(); in.sealLocked(); in.g.mu.Unlock() }
func (in *Input) InputDone() <-chan struct{} { return in.done }
func (in *Input) InputErr() error {
	select {
	case <-in.done:
		return nil
	default:
		return core.ErrEncoderInputPending
	}
}

type Accounting struct{ Retained, Pending, Requests int }

// Reconcile releases registry ownership before signaling Done/callback outside
// locks. Pending=0 means no retained producers, not callback completion.
// A sealed callback is immutable; concurrent reconciliation publishes it once.
// Tombstones retain only bounded identity/capability state, never a Reader.
func (g *Gateway) Reconcile() Accounting {
	var finished []*Input
	g.mu.Lock()
	a := Accounting{Retained: len(g.entries), Requests: g.active}
	for _, in := range g.entries {
		if !in.complete && in.sealed {
			select {
			case <-in.drained:
				ready := in.reader == nil
				if in.reader != nil {
					select {
					case <-in.reader.Done():
						ready = true
					default:
					}
				}
				if ready {
					in.reader = nil
					for _, c := range in.ownedClients {
						c.Close()
					}
					in.ownedClients = nil
					in.complete = true
					finished = append(finished, in)
				}
			default:
			}
		}
		if !in.complete {
			a.Pending++
		}
	}
	g.mu.Unlock()
	for _, in := range finished {
		close(in.done)
		if in.callback != nil {
			in.callback(nil)
		}
	}
	return a
}

// RenewalEvidence is local evidence for composing ValidateRenewal, not placement
// proof. No bearer is exposed and no raw expiry setter exists.
type RenewalEvidence struct {
	Renewal     remotetranscode.Renewal
	Identity    remotetranscode.Identity
	Handle      string
	Scope       nodesource.Scope
	Size        int64
	SourceUntil time.Time
	InputLease  core.EncoderInputLease
}

// Renew may conservatively extend source authority even if stop or worker CAS
// later rejects acceptance. It never attests worker acceptance or placement.
// One operation may validate at a time; contention fails closed without waiting.
// Successful source operations are never replayed as current authority evidence:
// if worker acceptance is uncertain, reconcile worker history. If unaccepted,
// retire this generation; source-validation replay is deliberately unsupported.
// Failed attempts may retry only their exact payload against fresh authority.
func (in *Input) Renew(ctx context.Context, n remotetranscode.Renewal, authority func(context.Context, nodesource.Grant) (nodesource.Grant, error)) (RenewalEvidence, error) {
	g := in.g
	in.readMu.RLock()
	defer in.readMu.RUnlock()
	g.mu.Lock()
	if !time.Now().Before(in.deadline) {
		in.sealLocked()
	}
	if in.sealed || in.reader == nil || in.renewing || ctx.Err() != nil || n.Identity != in.id || authority == nil || n.Operation == 0 || n.PreviousVersion == ^uint64(0) || n.Operation != n.PreviousVersion+1 || !n.Deadline.After(n.PreviousDeadline) {
		g.mu.Unlock()
		return RenewalEvidence{}, ErrDenied
	}
	previous := in.renewal
	version, deadline := uint64(0), in.initialDeadline
	if previous != nil {
		version, deadline = previous.Operation, previous.Deadline
	}
	// Strict history includes the exact preceding deadline. A Reader bearer CAS
	// alone cannot fence remote operation versions or changed retry payloads.
	if n.PreviousVersion != version || !n.PreviousDeadline.Equal(deadline) || (in.attempt != nil && n.Operation <= in.attempt.Operation && !equalRenewal(*in.attempt, n)) {
		g.mu.Unlock()
		return RenewalEvidence{}, ErrDenied
	}
	end, ok := in.tracker.Begin()
	if !ok {
		g.mu.Unlock()
		return RenewalEvidence{}, ErrDenied
	}
	attempt := n
	in.attempt = &attempt
	in.renewing = true
	reader := in.reader
	g.mu.Unlock()
	defer end()
	combined, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(in.ctx, cancel)
	defer cancel()
	defer stop()
	var until time.Time
	var acceptedGrant nodesource.Grant
	err := reader.Renew(combined, func(c context.Context, old nodesource.Grant) (nodesource.Grant, error) {
		next, e := authority(c, old)
		if e != nil || next.Expires.Before(n.Deadline) {
			return nodesource.Grant{}, ErrDenied
		}
		until = next.Expires
		acceptedGrant = next
		return next, nil
	})
	g.mu.Lock()
	defer g.mu.Unlock()
	in.renewing = false
	if !time.Now().Before(in.deadline) {
		in.sealLocked()
	}
	if err != nil || in.sealed || in.reader != reader || in.renewal != previous {
		return RenewalEvidence{}, ErrDenied
	}
	// Record successful source history even if caller cancellation means we cannot
	// issue evidence. Its source mutation cannot safely be forgotten or replayed.
	accepted := n
	in.renewal = &accepted
	in.sourceUntil = until
	in.grant = acceptedGrant
	in.armLocked(n.Deadline)
	if combined.Err() != nil {
		return RenewalEvidence{}, ErrDenied
	}
	return in.evidence(n), nil
}

func (in *Input) evidence(n remotetranscode.Renewal) RenewalEvidence {
	return RenewalEvidence{Renewal: n, Identity: in.id, Handle: in.handle, Scope: in.scope, Size: in.size, SourceUntil: in.sourceUntil, InputLease: in}
}
func equalRenewal(a, b remotetranscode.Renewal) bool {
	return a.Identity == b.Identity && a.Operation == b.Operation && a.PreviousVersion == b.PreviousVersion && a.PreviousDeadline.Equal(b.PreviousDeadline) && a.Deadline.Equal(b.Deadline)
}
func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.WriteHeader(405)
		return
	}
	g.mu.Lock()
	in := g.entries[strings.TrimPrefix(r.URL.Path, "/input/")]
	if g.closed || in == nil || in.sealed || !in.claimed || in.reader == nil || r.URL.Path != "/input/"+in.key || r.URL.RawQuery != "" || r.URL.RawPath != "" || r.URL.ForceQuery || r.URL.Fragment != "" || r.URL.Opaque != "" || r.URL.User != nil || (r.URL.Scheme != "" && r.URL.Scheme != "http") || (r.URL.Host != "" && r.URL.Host != g.listener.Addr().String()) || len(r.Header.Values("Range")) > 1 || len(r.Header.Values(core.EncoderInputScopeHeader)) != 1 || r.Host != g.listener.Addr().String() || r.Header.Get(core.EncoderInputScopeHeader) != in.key || g.active >= g.o.MaxRequests {
		g.mu.Unlock()
		w.WriteHeader(403)
		return
	}
	// Verification and producer registration share the exact sealing mutex.
	end, ok := in.tracker.Begin()
	if !ok {
		g.mu.Unlock()
		w.WriteHeader(403)
		return
	}
	g.active++
	g.mu.Unlock()
	defer func() { end(); g.mu.Lock(); g.active--; g.mu.Unlock() }()
	ctx, cancel := context.WithCancel(r.Context())
	if g.o.Recover == nil {
		cancel()
		ctx, cancel = context.WithTimeout(r.Context(), g.o.RequestTimeout)
	}
	responseControl := http.NewResponseController(w)
	_ = responseControl.SetWriteDeadline(time.Now().Add(g.o.RequestTimeout))
	stop := context.AfterFunc(in.ctx, cancel)
	defer cancel()
	defer stop()
	ctx = core.WithReadRetirement(ctx, &in.tracker)
	guarded := &generationfence.Writer{ResponseWriter: w, LocalCheck: func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		g.mu.Lock()
		if !time.Now().Before(in.deadline) {
			in.sealLocked()
		}
		sealed := in.sealed
		g.mu.Unlock()
		if sealed {
			return ErrDenied
		}
		return nil
	}, Check: func() error {
		if g.o.ValidateScope != nil {
			return in.validateScope(ctx)
		}
		return nil
	}}
	if g.o.WatchAuthority != nil {
		guarded.Watch = func(fn func()) func() { return g.o.WatchAuthority(in.id.Session, fn) }
	}
	defer guarded.Close()
	w = guarded
	start, length, status, err := byteRange(r.Header.Get("Range"), in.size)
	w.Header().Set("Accept-Ranges", "bytes")
	if err != nil {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", in.size))
		w.WriteHeader(416)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	if status == 206 {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, in.size))
	}
	w.WriteHeader(status)
	if r.Method == "HEAD" {
		return
	}
	// One 2 MiB source range replaces 32 separate 64 KiB admissions. At
	// 20 Mbps this is ~1.2 admissions/s. Bound scratch by the actual response
	// length; failed ranges are retried atomically at the same byte offset.
	buf := make([]byte, min(length, int64(sourceRangeBytes)))
	for length > 0 {
		if ctx.Err() != nil {
			return
		}
		want := min(int64(len(buf)), length)
		n, e := in.readAt(ctx, buf[:want], start)
		if ctx.Err() != nil || e != nil {
			return
		}
		if n > 0 {
			_ = responseControl.SetWriteDeadline(time.Now().Add(g.o.RequestTimeout))
			written, we := w.Write(buf[:n])
			if we != nil || written != n {
				return
			}
			start += int64(n)
			length -= int64(n)
		}
		if e != nil || n == 0 {
			return
		}
	}
}
func byteRange(raw string, size int64) (int64, int64, int, error) {
	if raw == "" {
		return 0, size, 200, nil
	}
	if !strings.HasPrefix(raw, "bytes=") || strings.Contains(raw, ",") || size == 0 {
		return 0, 0, 416, ErrDenied
	}
	parts := strings.Split(strings.TrimPrefix(raw, "bytes="), "-")
	if len(parts) != 2 {
		return 0, 0, 416, ErrDenied
	}
	parse := func(s string) (int64, error) {
		if s == "" {
			return 0, ErrDenied
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return 0, ErrDenied
			}
		}
		return strconv.ParseInt(s, 10, 64)
	}
	if parts[0] == "" {
		n, e := parse(parts[1])
		if e != nil || n == 0 {
			return 0, 0, 416, ErrDenied
		}
		n = min(n, size)
		return size - n, n, 206, nil
	}
	lo, e := parse(parts[0])
	if e != nil || lo >= size {
		return 0, 0, 416, ErrDenied
	}
	hi := size - 1
	if parts[1] != "" {
		hi, e = parse(parts[1])
		if e != nil || hi < lo {
			return 0, 0, 416, ErrDenied
		}
		hi = min(hi, size-1)
	}
	return lo, hi - lo + 1, 206, nil
}

var _ core.EncoderInputLease = (*Input)(nil)

// limitedListener rejects excess sockets before net/http can spawn a handler.
// No accept-wait goroutines or unbounded connection queue is added here.
type limitedListener struct {
	net.Listener
	mu          sync.Mutex
	active, max int
}
type countedConn struct {
	net.Conn
	owner *limitedListener
	once  sync.Once
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		l.mu.Lock()
		if l.active >= l.max {
			l.mu.Unlock()
			_ = c.Close()
			continue
		}
		l.active++
		l.mu.Unlock()
		return &countedConn{Conn: c, owner: l}, nil
	}
}
func (c *countedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.owner.mu.Lock(); c.owner.active--; c.owner.mu.Unlock() })
	return err
}

// The timer owns a monotonic deadline; stale callbacks cannot seal a renewal.
func (in *Input) armLocked(expiry time.Time) {
	if in.timer != nil {
		in.timer.Stop()
	}
	now := time.Now()
	in.deadline = now.Add(expiry.Sub(now))
	cap := in.deadline
	in.timer = time.AfterFunc(time.Until(cap), func() {
		in.g.mu.Lock()
		defer in.g.mu.Unlock()
		if in.deadline == cap {
			in.sealLocked()
		}
	})
}
