// Package nodesource transports exact source bytes on a private mTLS endpoint.
// It installs no routes and owns neither source Close nor encoder retirement.
package nodesource

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/generationfence"
	"github.com/braedonsaunders/ether/internal/placement"
)

const Path = "/internal/source/v1/bytes"
const CredentialHeader = "X-Ether-Source-Grant"

type Scope struct {
	SourceGeneration uint64
	Fence            placement.Fence
	Viewer           string
	Revision         string
}
type Peer struct{ NodeID, Boot string }
type Grant struct {
	Scope      Scope
	Credential string
	Expires    time.Time
}

// Verify checks current placement, viewer policy and source revocation. It must
// bind the opaque credential to the complete scope; a certificate is not a grant.
type Authority interface {
	Verify(context.Context, Peer, string) (Grant, error)
}

// Hold must refer to the exact acquired source object. Release relinquishes only
// this hold; it is NOT evidence of successful MediaSource.Close/SourceRetirement.
// Acquire receives a core.ReadRetirement context and must register any producers
// before starting them, including producers left behind on an error return.
// Acquire MUST atomically check current scope and the exact source lifetime
// sealing barrier while taking its hold. Prior Verify is not admission: a
// retirement barrier can seal between verification and acquisition.
// An error may return a nonempty Hold, which is retained until its tracker drains.
// Release must be bounded, nonblocking accounting, and must not perform I/O.
type Hold struct {
	Scope   Scope
	Source  core.MediaSource
	Release func()
}
type Options struct {
	WatchAuthority func(string, func()) func()
	Authority      Authority
	Peer           func(*x509.Certificate) (Peer, error)
	Acquire        func(context.Context, Scope) (Hold, error)
	Now            func() time.Time
	MaxRequests    int
	MaxLifetime    time.Duration
	Revalidate     time.Duration
}
type pending struct {
	scope   Scope
	tracker core.ReadRetirement
	done    <-chan struct{}
	release func()
	source  core.MediaSource
}
type Handler struct {
	o       Options
	mu      sync.Mutex
	entries map[*pending]bool
}

func validScope(s Scope) bool {
	f := s.Fence
	for _, v := range []string{f.Session, f.Controller.ID, f.Controller.Boot, f.Target.NodeID, f.Target.Boot, s.Viewer, s.Revision} {
		if len(v) == 0 || len(v) > 256 {
			return false
		}
	}
	return f.Generation > 0
}
func validGrant(g Grant, now time.Time, max time.Duration) bool {
	return validScope(g.Scope) && len(g.Credential) > 0 && len(g.Credential) <= 4096 && g.Expires.After(now) && g.Expires.Sub(now) <= max
}
func NewHandler(o Options) (*Handler, error) {
	if o.Authority == nil || o.Peer == nil || o.Acquire == nil || o.Now == nil || o.MaxRequests < 1 || o.MaxRequests > 4096 || o.MaxLifetime <= 0 || o.MaxLifetime > time.Hour || o.Revalidate <= 0 || o.Revalidate > time.Second || o.Revalidate > o.MaxLifetime {
		return nil, errors.New("nodesource: invalid dependencies or bounds")
	}
	return &Handler{o: o, entries: make(map[*pending]bool)}, nil
}

// Reconcile is called by the host lifecycle, including after drain/expiry and
// when no more requests arrive. It never treats elapsed time as retirement.
// Pending includes active, cancelled and disconnected requests with producers.
// No callbacks run under registry locks. There are no background goroutines.
func (h *Handler) Reconcile() int {
	var releases []func()
	h.mu.Lock()
	for p := range h.entries {
		if p.done != nil {
			select {
			case <-p.done:
				delete(h.entries, p)
				if p.release != nil {
					releases = append(releases, p.release)
				}
			default:
			}
		}
	}
	h.mu.Unlock()
	for _, release := range releases {
		release()
	}
	h.mu.Lock()
	n := len(h.entries)
	h.mu.Unlock()
	return n
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != Path || r.URL.RawQuery != "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.TLS == nil || len(r.TLS.VerifiedChains) == 0 || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "unauthorized", 401)
		return
	}
	peer, err := h.o.Peer(r.TLS.PeerCertificates[0])
	if err != nil {
		http.Error(w, "unauthorized", 401)
		return
	}
	values := r.Header.Values(CredentialHeader)
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > 4096 {
		http.Error(w, "forbidden", 403)
		return
	}
	grant, err := h.o.Authority.Verify(r.Context(), peer, values[0])
	if err != nil || grant.Credential != values[0] || !validGrant(grant, h.o.Now(), h.o.MaxLifetime) || peer != (Peer{grant.Scope.Fence.Target.NodeID, grant.Scope.Fence.Target.Boot}) {
		http.Error(w, "forbidden", 403)
		return
	}
	if ranges := r.Header.Values("Range"); len(ranges) > 1 || (len(ranges) == 1 && strings.Contains(ranges[0], ",")) {
		http.Error(w, "multiple ranges unsupported", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	h.Reconcile()
	p := &pending{scope: grant.Scope}
	h.mu.Lock()
	if len(h.entries) >= h.o.MaxRequests {
		h.mu.Unlock()
		http.Error(w, "busy", 503)
		return
	}
	h.entries[p] = true
	h.mu.Unlock()
	// Install tracker before Acquire; ALL exits below seal it, including errors.
	defer func() { done := p.tracker.Seal(); h.mu.Lock(); p.done = done; h.mu.Unlock(); h.Reconcile() }()
	ctx, cancel := context.WithDeadline(core.WithReadRetirement(r.Context(), &p.tracker), grant.Expires)
	defer cancel()
	hold, err := h.o.Acquire(ctx, grant.Scope)
	h.mu.Lock()
	p.release = hold.Release
	p.source = hold.Source
	h.mu.Unlock()
	if err != nil || hold.Release == nil || hold.Source == nil || hold.Scope != grant.Scope {
		http.Error(w, "source unavailable", 503)
		return
	}
	if ctx.Err() != nil || !h.o.Now().Before(grant.Expires) {
		http.Error(w, "expired", 403)
		return
	}
	reader, ok := hold.Source.(core.ContextReaderAt)
	if !ok || hold.Source.Size() < 0 {
		http.Error(w, "source unavailable", 503)
		return
	}
	sr := &sourceReader{ctx: ctx, source: reader, size: hold.Source.Size(), check: func() error {
		now := h.o.Now()
		if !now.Before(grant.Expires) {
			return errors.New("nodesource: expired")
		}
		fresh, e := h.o.Authority.Verify(ctx, peer, grant.Credential)
		if e != nil || fresh.Scope != grant.Scope || fresh.Credential != grant.Credential || !fresh.Expires.Equal(grant.Expires) {
			return errors.New("nodesource: revoked")
		}
		return nil
	}}
	w.Header().Set("ETag", etag(grant.Scope))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	guarded := &generationfence.Writer{ResponseWriter: w, Check: sr.check, Interval: h.o.Revalidate, LocalCheck: func() error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !h.o.Now().Before(grant.Expires) {
			return generationfence.ErrStale
		}
		if local, ok := h.o.Authority.(interface {
			CheckLocal(context.Context, Peer, string) error
		}); ok {
			return local.CheckLocal(ctx, peer, grant.Credential)
		}
		return nil
	}}
	if h.o.WatchAuthority != nil {
		guarded.Watch = func(fn func()) func() { return h.o.WatchAuthority(grant.Scope.Fence.Session, fn) }
	}
	defer guarded.Close()
	sr.check = guarded.Validate

	http.ServeContent(guarded, r.WithContext(ctx), "source", time.Time{}, sr)
}
func etag(s Scope) string { b, _ := json.Marshal(s); return fmt.Sprintf("\"%x\"", sha256.Sum256(b)) }

type sourceReader struct {
	ctx       context.Context
	source    core.ContextReaderAt
	size, pos int64
	check     func() error
}

func (s *sourceReader) Read(b []byte) (int, error) {
	if err := s.ctx.Err(); err != nil {
		return 0, err
	}
	if err := s.check(); err != nil {
		return 0, err
	}
	if s.pos >= s.size {
		return 0, io.EOF
	}
	if int64(len(b)) > s.size-s.pos {
		b = b[:s.size-s.pos]
	}
	n, err := s.source.ReadAtContext(s.ctx, b, s.pos)
	if cancelled := s.ctx.Err(); cancelled != nil {
		return 0, cancelled
	}
	if e := s.check(); e != nil {
		return 0, e
	}
	s.pos += int64(n)
	return n, err
}
func (s *sourceReader) Seek(off int64, whence int) (int64, error) {
	base := int64(0)
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = s.pos
	case io.SeekEnd:
		base = s.size
	default:
		return 0, errors.New("invalid seek")
	}
	next := base + off
	if next < 0 || (off > 0 && next < base) {
		return 0, errors.New("invalid seek")
	}
	s.pos = next
	return next, nil
}

// Pending returns exact-scope hold count, including cancelled/expired requests.
// A zero count proves only that transport holds have been relinquished, never
// source Close, encoder retirement or durable placement retirement.
func (h *Handler) Pending(scope Scope) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for p := range h.entries {
		if p.scope == scope {
			n++
		}
	}
	return n
}
func (g Grant) String() string   { return "source grant (redacted)" }
func (g Grant) GoString() string { return g.String() }
