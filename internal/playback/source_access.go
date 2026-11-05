package playback

import (
	"context"
	"io"
	"sync"

	"github.com/braedonsaunders/ether/internal/core"
)

// SourceAccess is a bounded, host-owned bridge to exact playback generations.
// Construct one per manager. It performs no network authorization. The host must
// bind handles to full placement fences and current viewer policy on every use.
// It owns no goroutines; lifecycle code must Reconcile even after HTTP drain.
type SourceAccess struct {
	manager              *Manager
	mu                   sync.Mutex
	maxHandles, maxHolds int
	handles              map[*SourceHandle]bool
	holds                map[*sourceAccessHold]bool
}

// NewSourceAccess sets fail-closed admission bounds, including released holds
// whose producers or backend Close have not finished. Bounds cannot exceed 4096.
func NewSourceAccess(m *Manager, maxHandles, maxHolds int) (*SourceAccess, error) {
	if m == nil || maxHandles < 1 || maxHandles > 4096 || maxHolds < 1 || maxHolds > 4096 {
		return nil, core.ErrNotAvailable
	}
	return &SourceAccess{manager: m, maxHandles: maxHandles, maxHolds: maxHolds, handles: make(map[*SourceHandle]bool), holds: make(map[*sourceAccessHold]bool)}, nil
}

// SourceHandle is a local capability, never a wire credential. Its zero value
// grants nothing. A revoked or stale handle cannot adopt another generation.
type SourceHandle struct {
	access     *SourceAccess
	session    *session
	lifetime   *sourceLifetime
	generation *generation
	viewer     int64
	revision   string
	size       int64
}

func (*SourceHandle) String() string     { return "playback source handle" }
func (h *SourceHandle) GoString() string { return h.String() }
func (h *SourceHandle) Revision() string {
	if h == nil {
		return ""
	}
	return h.revision
}
func (h *SourceHandle) Size() int64 {
	if h == nil {
		return 0
	}
	return h.size
}

// Capture accepts only trusted local observations: expected is the previously
// captured lifetime; input is that exact manager-issued generation URL, never
// input supplied by a remote caller. No URL is opened or parsed. Revision must
// match the generation's canonical historical open observation, not arbitrary
// caller metadata. Unsupported/unknown revisions fail closed. No source method
// runs under a lock. Historical, provisional and replaced generations all fail.
func (a *SourceAccess) Capture(ctx context.Context, viewer int64, id string, expected core.SourceRetirement, input, revision string) (*SourceHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	lifetime, ok := expected.(*sourceLifetime)
	if !ok || lifetime == nil || revision == "" || input == "" {
		return nil, core.ErrNotAvailable
	}
	m := a.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if m.closed || s == nil || s.sources != lifetime || s.ident.userID != viewer || lifetime.user != viewer {
		return nil, core.ErrNotAvailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	g := s.cur
	if s.closed || g == nil || g.sess != s || m.urlFor(g) != input {
		return nil, core.ErrNotAvailable
	}
	lifetime.mu.Lock()
	defer lifetime.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	if lifetime.sealed || g.retired || g.closed || g.src == nil || g.observation.Revision != revision || g.tmap.Size <= 0 {
		return nil, core.ErrNotAvailable
	}
	if _, ok := g.src.(core.ContextReaderAt); !ok {
		return nil, core.ErrNotAvailable
	}
	if len(a.handles) >= a.maxHandles {
		return nil, core.ErrNotAvailable
	}
	h := &SourceHandle{access: a, session: s, lifetime: lifetime, generation: g, viewer: viewer, revision: revision, size: g.tmap.Size}
	a.handles[h] = true
	return h, nil
}

// Active checks exact current registration and lifetime without source callbacks.
// It is a point-in-time observation, not acquisition or retirement proof.
func (h *SourceHandle) Active() bool {
	if h == nil || h.access == nil {
		return false
	}
	a, s, g := h.access, h.session, h.generation
	m := a.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	h.lifetime.mu.Lock()
	defer h.lifetime.mu.Unlock()
	a.mu.Lock()
	defer a.mu.Unlock()
	g.mu.Lock()
	defer g.mu.Unlock()
	return !m.closed && m.sessions[s.id] == s && s.sources == h.lifetime && !s.closed && s.cur == g && s.ident.userID == h.viewer && !h.lifetime.sealed && a.handles[h] && !g.retired && !g.closed && g.src != nil
}

// Revoke rejects future acquisitions and returns handle capacity. Already
// acquired holds continue to pin their original generation until reconciliation.
func (h *SourceHandle) Revoke() {
	if h == nil || h.access == nil {
		return
	}
	h.access.mu.Lock()
	delete(h.access.handles, h)
	h.access.mu.Unlock()
}

// SourceHold owns one existing generation reference. Even on Acquire error a
// nonempty hold MUST be released. Source.Close cannot close the backend. Release
// is idempotent, bounded accounting only; it seals the original request tracker
// and rejects future reads. It never drops a generation or calls a source.
type SourceHold struct {
	Source  core.MediaSource
	Release func()
}
type sourceAccessHold struct {
	handle     *SourceHandle
	tracker    *core.ReadRetirement
	mu         sync.Mutex
	done       <-chan struct{}
	processing bool // guarded by access.mu
}

// Acquire requires a request tracker installed before any producers. Lifetime,
// session ownership, current generation and sealing are checked while retaining
// the exact generation atomically. Fresh metadata checks happen outside locks;
// failure returns the hold, preserving cleanup responsibility. Context readers
// must register delayed producers with the tracker they receive before starting.
func (h *SourceHandle) Acquire(ctx context.Context) (SourceHold, error) {
	if h == nil || h.access == nil {
		return SourceHold{}, core.ErrNotAvailable
	}
	if err := ctx.Err(); err != nil {
		return SourceHold{}, err
	}
	tracker := core.ReadRetirementFrom(ctx)
	if tracker == nil {
		return SourceHold{}, core.ErrNotAvailable
	}
	a, s, g := h.access, h.session, h.generation
	m := a.manager
	// Lock order for both Capture and Acquire is manager -> session -> lifetime
	// -> access -> generation. Reconcile never calls a generation under access.mu.
	m.mu.Lock()
	s.mu.Lock()
	h.lifetime.mu.Lock()
	a.mu.Lock()
	valid := !m.closed && m.sessions[s.id] == s && s.sources == h.lifetime && !s.closed && s.cur == g && s.ident.userID == h.viewer && !h.lifetime.sealed && a.handles[h] && len(a.holds) < a.maxHolds
	var entry *sourceAccessHold
	if valid {
		// Begin also refuses a request already sealed before acquisition. This
		// temporary registration starts no work and its completion does no I/O.
		if done, ok := tracker.Begin(); ok {
			if g.acquire() {
				entry = &sourceAccessHold{handle: h, tracker: tracker}
				a.holds[entry] = true
			}
			done()
		}
	}
	a.mu.Unlock()
	h.lifetime.mu.Unlock()
	s.mu.Unlock()
	m.mu.Unlock()
	if entry == nil {
		return SourceHold{}, core.ErrNotAvailable
	}
	hold := SourceHold{Source: &sourceAccessReader{entry: entry}, Release: entry.release}
	if err := ctx.Err(); err != nil {
		return hold, err
	}
	if !h.unchanged() {
		return hold, core.ErrSourceChanged
	}
	return hold, nil
}
func (h *SourceHandle) unchanged() bool {
	src := h.generation.src
	return src.Size() == h.size && core.SourceRevision(&core.PlaySession{Key: h.session.ident.key, Source: src}) == h.revision
}
func (p *sourceAccessHold) release() {
	p.mu.Lock()
	if p.done == nil {
		p.done = p.tracker.Seal()
	}
	p.mu.Unlock()
}

// Reconcile may BLOCK in backend Close. Call only on an explicit cleanup path,
// never heartbeat, expiry, HTTP control or bounded release paths. Capacity stays
// charged until generation.release/Close returns. Concurrent reconciliation
// cannot release twice or report an outstanding Close as drained. The returned
// count includes active, producer-blocked and Close-blocked holds; zero is NOT
// successful SourceRetirement or durable cleanup proof. Consult that lifetime.
func (a *SourceAccess) Reconcile() int {
	a.mu.Lock()
	var ready []*sourceAccessHold
	for p := range a.holds {
		p.mu.Lock()
		done := p.done
		p.mu.Unlock()
		if done == nil || p.processing {
			continue
		}
		select {
		case <-done:
			p.processing = true
			ready = append(ready, p)
		default:
		}
	}
	a.mu.Unlock()
	for _, p := range ready {
		p.handle.generation.release()
		a.mu.Lock()
		delete(a.holds, p)
		a.mu.Unlock()
	}
	a.mu.Lock()
	n := len(a.holds)
	a.mu.Unlock()
	return n
}

type sourceAccessReader struct{ entry *sourceAccessHold }

func (r *sourceAccessReader) Size() int64 { return r.entry.handle.size }

// Metadata intentionally exposes no provider identifier or source path.
func (*sourceAccessReader) Name() string      { return "source" }
func (*sourceAccessReader) ReleaseID() string { return "" }
func (*sourceAccessReader) Close() error      { return core.ErrNotAvailable }
func (r *sourceAccessReader) ReadAt(b []byte, off int64) (int, error) {
	return r.ReadAtContext(context.Background(), b, off)
}
func (r *sourceAccessReader) ReadAtContext(ctx context.Context, b []byte, off int64) (int, error) {
	p := r.entry
	// Always register against the acquisition tracker, even if a caller supplies
	// a different context. Release serializes with this registration and seals it.
	p.mu.Lock()
	if p.done != nil {
		p.mu.Unlock()
		return 0, core.ErrNotAvailable
	}
	done, ok := p.tracker.Begin()
	p.mu.Unlock()
	if !ok {
		return 0, core.ErrNotAvailable
	}
	defer done()
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	h := p.handle
	if !h.unchanged() {
		return 0, core.ErrSourceChanged
	}
	if off < 0 {
		return 0, core.ErrNotAvailable
	}
	if len(b) == 0 {
		return 0, nil
	}
	if off >= h.size {
		return 0, io.EOF
	}
	requested := len(b)
	if int64(len(b)) > h.size-off {
		b = b[:h.size-off]
	}
	n, err := h.generation.src.(core.ContextReaderAt).ReadAtContext(core.WithReadRetirement(ctx, p.tracker), b, off)
	if n < requested && err == nil {
		err = io.EOF
	}
	return n, err
}
