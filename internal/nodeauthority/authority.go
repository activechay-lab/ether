// Package nodeauthority binds boot-local source capabilities to durable placement.
// It installs no routes and owns lease-expiry timers, never source retirement.
package nodeauthority

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
)

var ErrDenied = errors.New("nodeauthority: denied")
var ErrCapacity = errors.New("nodeauthority: capacity exhausted")

// Lookup must make a fresh authoritative observation on every call.
type Lookup interface {
	Lookup(context.Context, string) (placement.Record, error)
}

var _ Lookup = (*placement.Service)(nil)

// Options are trusted host configuration. Policy checks current viewer/source
// rights, controller and target membership/boots, and target transcode eligibility.
// ClockBoundKnown asserts synchronized local/DB/worker clocks with absolute
// pairwise skew <= ClockSkew, including drift over MaxLifetime. Unknown fails closed.
type Options struct {
	// ClockReady gates binds and minting (including renewal), never existing reads.
	ClockReady                                  func() bool
	Lookup                                      Lookup
	Policy                                      func(context.Context, placement.Target, nodesource.Scope) error
	Owner                                       placement.Target
	Now                                         func() time.Time
	ClockBoundKnown                             bool
	ClockSkew, MaxLifetime                      time.Duration
	MaxBindings, MaxGrants, MaxGrantsPerBinding int
}

// Handle is the immutable Start.Grant identity, distinct from a bearer.
type Handle struct{ value string }

func (Handle) String() string     { return "nodeauthority handle [redacted]" }
func (h Handle) GoString() string { return h.String() }

// Value is an explicit secret export for trusted transport composition.
func (h Handle) Value() string { return h.value }

// Lease redacts ordinary formatting. Transport explicitly exports its Grant.
type Lease struct{ grant nodesource.Grant }

func (Lease) String() string            { return "nodeauthority lease [redacted]" }
func (l Lease) GoString() string        { return l.String() }
func (l Lease) Grant() nodesource.Grant { return l.grant }

type localSource interface {
	Revision() string
	Size() int64
	Active() bool
	Acquire(context.Context) (playback.SourceHold, error)
	Revoke()
}
type binding struct {
	handle   Handle
	scope    nodesource.Scope
	source   localSource
	size     int64
	sealed   bool
	epoch    uint64 // changes only once: permanent sealing; cannot overflow
	current  string
	timer    *time.Timer
	deadline time.Time // monotonic local cap, never serialized
}
type credential struct {
	binding  *binding
	grant    nodesource.Grant
	deadline time.Time // monotonic immutable bearer expiry
}
type Authority struct {
	o        Options
	mu       sync.Mutex
	bindings map[string]*binding           // permanent bounded boot-local tombstones
	scopes   map[nodesource.Scope]*binding // same bounded entries, exact acquisition index
	grants   map[string]*credential
}

func (*Authority) String() string     { return "nodeauthority [redacted]" }
func (a *Authority) GoString() string { return a.String() }

var _ nodesource.Authority = (*Authority)(nil)

func New(o Options) (*Authority, error) {
	if o.Lookup == nil || o.Policy == nil || o.Now == nil || !valid(o.Owner.NodeID) || !valid(o.Owner.Boot) || !o.ClockBoundKnown || o.ClockSkew < 0 || o.ClockSkew > time.Minute || o.MaxLifetime <= 0 || o.MaxLifetime > time.Hour || o.ClockSkew >= o.MaxLifetime || !bound(o.MaxBindings) || !bound(o.MaxGrants) || !bound(o.MaxGrantsPerBinding) {
		return nil, ErrDenied
	}
	return &Authority{o: o, bindings: make(map[string]*binding), grants: make(map[string]*credential), scopes: make(map[nodesource.Scope]*binding)}, nil
}
func bound(n int) bool    { return n > 0 && n <= 4096 }
func valid(s string) bool { return len(s) > 0 && len(s) <= 256 }
func validScope(s nodesource.Scope) bool {
	f := s.Fence
	return f.Generation > 0 && valid(f.Session) && valid(f.Controller.ID) && valid(f.Controller.Boot) && valid(f.Target.NodeID) && valid(f.Target.Boot) && valid(s.Viewer) && valid(s.Revision)
}
func random() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", ErrDenied
	}
	return hex.EncodeToString(b[:]), nil
}

// Bind accepts only an already captured local handle, never a URL or path. Size
// and revision must equal the handle's canonical trusted capture. Failed binds
// consume no slot; successful binds consume one for this authority's entire boot.
func (a *Authority) Bind(ctx context.Context, s nodesource.Scope, h *playback.SourceHandle, size int64) (Handle, error) {
	if h == nil {
		return Handle{}, ErrDenied
	}
	return a.bind(ctx, s, h, size)
}
func (a *Authority) bind(ctx context.Context, s nodesource.Scope, h localSource, size int64) (Handle, error) {
	if !a.clockReady() || !validScope(s) || size <= 0 || h.Revision() != s.Revision || h.Size() != size {
		return Handle{}, ErrDenied
	}
	b := &binding{scope: s, source: h, size: size}
	if a.check(ctx, b, time.Time{}) != nil {
		return Handle{}, ErrDenied
	}
	id, err := random()
	if err != nil {
		return Handle{}, err
	}
	b.handle = Handle{id}
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.bindings) >= a.o.MaxBindings {
		return Handle{}, ErrCapacity
	}
	for _, old := range a.bindings {
		if old.scope == s || old.source == h {
			return Handle{}, ErrDenied
		}
	}
	if !a.clockReady() || a.bindings[id] != nil {
		return Handle{}, ErrDenied
	}
	a.bindings[id] = b
	a.scopes[s] = b
	a.armLocked(b, a.o.Now().Add(a.o.MaxLifetime))
	return b.handle, nil
}

// check invokes every external dependency without registry locks. No stale
// authority is cached. Expiry is bounded by both local lifetime and DB deadline,
// subtracting the declared skew conservatively from each boundary.
func (a *Authority) check(ctx context.Context, b *binding, expiry time.Time) error {
	if ctx.Err() != nil || !b.source.Active() {
		return ErrDenied
	}
	if a.o.Policy(ctx, a.o.Owner, b.scope) != nil {
		return ErrDenied
	}
	r, err := a.o.Lookup.Lookup(ctx, b.scope.Fence.Session)
	if err != nil || r.Fence != b.scope.Fence || r.Retired || !r.Authority || r.Role != config.RoleTranscode {
		return ErrDenied
	}
	now := a.o.Now()
	if now.IsZero() || !r.Deadline.Add(-a.o.ClockSkew).After(now) {
		return ErrDenied
	}
	if !expiry.IsZero() && (!expiry.After(now.Add(a.o.ClockSkew)) || expiry.After(now.Add(a.o.MaxLifetime-a.o.ClockSkew)) || expiry.After(r.Deadline.Add(-a.o.ClockSkew))) {
		return ErrDenied
	}
	if ctx.Err() != nil || !b.source.Active() {
		return ErrDenied
	}
	return nil
}
func (a *Authority) live(b *binding, epoch uint64) bool {
	return b != nil && !b.sealed && time.Now().Before(b.deadline) && b.epoch == epoch && a.bindings[b.handle.value] == b
}
func (a *Authority) prune(now time.Time) {
	for key, c := range a.grants {
		if !c.grant.Expires.After(now) || !time.Now().Before(c.deadline) {
			delete(a.grants, key)
		}
	}
}
func (a *Authority) Issue(ctx context.Context, h Handle, expiry time.Time) (Lease, error) {
	return a.mint(ctx, h, "", expiry)
}

// Renew accepts only the current bearer; a successful replay cannot create more
// grants. Old bearers remain valid only until their original immutable expiry.
func (a *Authority) Renew(ctx context.Context, h Handle, old nodesource.Grant, expiry time.Time) (Lease, error) {
	a.mu.Lock()
	c := a.grants[old.Credential]
	ok := c != nil && c.grant.Scope == old.Scope && c.grant.Credential == old.Credential && c.grant.Expires.Equal(old.Expires) && c.binding.handle == h
	a.mu.Unlock()
	if !ok || !expiry.After(old.Expires) {
		return Lease{}, ErrDenied
	}
	return a.mint(ctx, h, old.Credential, expiry)
}
func (a *Authority) mint(ctx context.Context, h Handle, old string, expiry time.Time) (Lease, error) {
	if !a.clockReady() || expiry.IsZero() {
		return Lease{}, ErrDenied
	}
	a.mu.Lock()
	b := a.bindings[h.value]
	var epoch uint64
	if b == nil || b.sealed || b.current != old {
		a.mu.Unlock()
		return Lease{}, ErrDenied
	}
	epoch = b.epoch
	a.mu.Unlock()
	if a.check(ctx, b, expiry) != nil {
		return Lease{}, ErrDenied
	}
	key, err := random()
	if err != nil {
		return Lease{}, err
	}
	now := a.o.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.clockReady() || !a.live(b, epoch) || b.current != old || !expiry.After(now.Add(a.o.ClockSkew)) {
		return Lease{}, ErrDenied
	}
	if old != "" {
		c := a.grants[old]
		if c == nil || !time.Now().Before(c.deadline) || !c.grant.Expires.After(now.Add(a.o.ClockSkew)) {
			return Lease{}, ErrDenied
		}
	}
	a.prune(now)
	n := 0
	for _, c := range a.grants {
		if c.binding == b {
			n++
		}
	}
	if n >= a.o.MaxGrantsPerBinding || len(a.grants) >= a.o.MaxGrants {
		return Lease{}, ErrCapacity
	}
	if a.grants[key] != nil {
		return Lease{}, ErrDenied
	}
	g := nodesource.Grant{Scope: b.scope, Credential: key, Expires: expiry.UTC().Round(0)}
	a.grants[key] = &credential{binding: b, grant: g, deadline: time.Now().Add(expiry.Sub(now) - a.o.ClockSkew)}
	a.armLocked(b, expiry)
	b.current = key
	return Lease{g}, nil
}
func (a *Authority) Verify(ctx context.Context, p nodesource.Peer, key string) (nodesource.Grant, error) {
	if len(key) != 64 {
		return nodesource.Grant{}, ErrDenied
	}
	a.mu.Lock()
	c := a.grants[key]
	var epoch uint64
	if c == nil || c.binding.sealed {
		a.mu.Unlock()
		return nodesource.Grant{}, ErrDenied
	}
	b := c.binding
	epoch = b.epoch
	a.mu.Unlock()
	if p != (nodesource.Peer{NodeID: b.scope.Fence.Target.NodeID, Boot: b.scope.Fence.Target.Boot}) || a.check(ctx, b, c.grant.Expires) != nil {
		return nodesource.Grant{}, ErrDenied
	}
	now := a.o.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.live(b, epoch) || a.grants[key] != c || !time.Now().Before(c.deadline) || !c.grant.Expires.After(now.Add(a.o.ClockSkew)) {
		return nodesource.Grant{}, ErrDenied
	}
	return c.grant, nil
}

// Acquire is installed only on the private nodesource handler, which checks Peer
// and bearer via Verify. It independently checks live binding authority before
// retaining the exact SourceHandle under playback's manager barrier.
func (a *Authority) Acquire(ctx context.Context, s nodesource.Scope) (nodesource.Hold, error) {
	a.mu.Lock()
	b := a.scopes[s]
	if b == nil || b.sealed {
		a.mu.Unlock()
		return nodesource.Hold{}, ErrDenied
	}
	epoch := b.epoch
	a.mu.Unlock()
	if a.check(ctx, b, time.Time{}) != nil {
		return nodesource.Hold{}, ErrDenied
	}
	a.mu.Lock()
	ok := a.live(b, epoch)
	a.mu.Unlock()
	if !ok {
		return nodesource.Hold{}, ErrDenied
	}
	held, err := b.source.Acquire(ctx)
	hold := nodesource.Hold{Scope: s, Source: held.Source, Release: held.Release}
	a.mu.Lock()
	ok = a.live(b, epoch)
	a.mu.Unlock()
	if !ok || err != nil {
		// Preserve cleanup ownership, but never expose a usable facade across revoke.
		hold.Source = nil
		return hold, ErrDenied
	}
	return hold, nil
}

// Revoke seals registry admissions first and closes playback's acquisition
// barrier before returning. It never releases holds, closes sources or retires
// placement. Repeated calls also wait for the handle's barrier by calling Revoke.
func (a *Authority) Revoke(h Handle) error {
	a.mu.Lock()
	b := a.bindings[h.value]
	if b == nil {
		a.mu.Unlock()
		return ErrDenied
	}
	if b.timer != nil {
		b.timer.Stop()
	}
	if !b.sealed {
		b.sealed = true
		b.epoch++
	}
	for key, c := range a.grants {
		if c.binding == b {
			delete(a.grants, key)
		}
	}
	a.mu.Unlock()
	b.source.Revoke()
	return nil
}

// Resolve is trusted host composition for immutable Start.Grant values. The
// caller supplies the authenticated intended worker identity; no wire endpoint
// is installed. Success does not issue a bearer or establish retirement proof.
func (a *Authority) Resolve(ctx context.Context, peer nodesource.Peer, stable string) (Handle, nodesource.Scope, int64, error) {
	deny := func() (Handle, nodesource.Scope, int64, error) { return Handle{}, nodesource.Scope{}, 0, ErrDenied }
	if len(stable) != 64 {
		return deny()
	}
	a.mu.Lock()
	b := a.bindings[stable]
	if b == nil || b.sealed {
		a.mu.Unlock()
		return deny()
	}
	epoch := b.epoch
	a.mu.Unlock()
	if peer != (nodesource.Peer{NodeID: b.scope.Fence.Target.NodeID, Boot: b.scope.Fence.Target.Boot}) || a.check(ctx, b, time.Time{}) != nil {
		return deny()
	}
	a.mu.Lock()
	ok := a.live(b, epoch)
	a.mu.Unlock()
	if !ok {
		return deny()
	}
	return b.handle, b.scope, b.size, nil
}

// armLocked bounds local authority even if wall time moves backwards or the
// controller disappears. A queued callback must compare the accepted cap before
// sealing; an old timer can never revoke a successfully renewed binding.
func (a *Authority) armLocked(b *binding, expiry time.Time) {
	if b.timer != nil {
		b.timer.Stop()
	}
	b.deadline = time.Now().Add(expiry.Sub(a.o.Now()) - a.o.ClockSkew)
	cap := b.deadline
	b.timer = time.AfterFunc(time.Until(cap), func() {
		a.mu.Lock()
		if b.sealed || b.deadline != cap {
			a.mu.Unlock()
			return
		}
		b.sealed = true
		b.epoch++
		for key, c := range a.grants {
			if c.binding == b {
				delete(a.grants, key)
			}
		}
		a.mu.Unlock()
		b.source.Revoke()
	})
}

func (a *Authority) clockReady() bool { return a.o.ClockReady == nil || a.o.ClockReady() }

// CheckLocal checks revocation and monotonic lease expiry without durable I/O.
// It supplements Verify during an already admitted streaming request.
func (a *Authority) CheckLocal(ctx context.Context, p nodesource.Peer, key string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	c := a.grants[key]
	if c == nil || c.binding.sealed || !time.Now().Before(c.deadline) || !time.Now().Before(c.binding.deadline) || p != (nodesource.Peer{NodeID: c.binding.scope.Fence.Target.NodeID, Boot: c.binding.scope.Fence.Target.Boot}) {
		return ErrDenied
	}
	return nil
}
