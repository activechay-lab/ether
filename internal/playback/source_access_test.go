package playback

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type accessTestSource struct {
	entered, unblock, closeEntered, closeUnblock chan struct{}
	producerDone                                 func()
	delayed                                      bool
	closes                                       atomic.Int32
	onClose                                      func()
	size                                         int64
}

func (s *accessTestSource) Size() int64     { return s.size }
func (*accessTestSource) Name() string      { return "exact.mkv" }
func (*accessTestSource) ReleaseID() string { return "release" }
func (s *accessTestSource) ReadAt(b []byte, off int64) (int, error) {
	return s.ReadAtContext(context.Background(), b, off)
}
func (s *accessTestSource) ReadAtContext(ctx context.Context, b []byte, _ int64) (int, error) {
	if s.delayed {
		done, ok := core.ReadRetirementFrom(ctx).Begin()
		if !ok {
			return 0, core.ErrNotAvailable
		}
		s.producerDone = done
		return 0, errors.New("producer still owns input after read error")
	}
	if s.entered != nil {
		close(s.entered)
		<-s.unblock
	}
	b[0] = 7
	return 1, nil
}
func (s *accessTestSource) Close() error {
	s.closes.Add(1)
	if s.onClose != nil {
		s.onClose()
	}
	if s.closeEntered != nil {
		close(s.closeEntered)
		<-s.closeUnblock
	}
	return nil
}
func accessFixture(t *testing.T, handles, holds int) (*SourceAccess, *SourceHandle, *accessTestSource) {
	t.Helper()
	m := &Manager{}
	life := newSourceLifetime(9, "plex", "device")
	s := &session{id: "same-id", ident: identity{userID: 9}, sources: life}
	src := &accessTestSource{size: 10}
	rev := core.SourceRevision(&core.PlaySession{Key: s.ident.key, Source: src})
	g := &generation{sess: s, src: src, token: "exact-token", observation: core.PlaybackSourceObservation{Revision: rev}, tmap: TimeMap{Size: 10}}
	s.cur = g
	m.sessions = map[string]*session{s.id: s}
	a, err := NewSourceAccess(m, handles, holds)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := m.reserveSourceOpen(life)
	if err != nil {
		t.Fatal(err)
	}
	g.finishSource = func(err error) { ticket.physical(err); ticket.settle(err) }
	h, err := a.Capture(context.Background(), 9, s.id, life, m.urlFor(g), rev)
	if err != nil {
		t.Fatal(err)
	}
	return a, h, src
}
func accessAcquire(t *testing.T, h *SourceHandle) (SourceHold, *core.ReadRetirement) {
	t.Helper()
	tracker := &core.ReadRetirement{}
	hold, err := h.Acquire(core.WithReadRetirement(context.Background(), tracker))
	if err != nil {
		t.Fatal(err)
	}
	return hold, tracker
}
func accessSeal(a *SourceAccess, h *SourceHandle) {
	m, s := a.manager, h.session
	m.mu.Lock()
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	h.lifetime.seal()
	delete(m.sessions, s.id)
	m.mu.Unlock()
	h.generation.retire()
}

func TestSourceAccessExactAdmission(t *testing.T) {
	t.Run("verification-then-seal", func(t *testing.T) {
		a, h, _ := accessFixture(t, 2, 2)
		// Host verification has already found the handle; lifetime sealing wins
		// before atomic acquisition. No verification cache can reopen it.
		accessSeal(a, h)
		if hold, err := h.Acquire(core.WithReadRetirement(context.Background(), &core.ReadRetirement{})); err == nil || hold.Release != nil {
			t.Fatal("sealed acquisition admitted")
		}
		if _, err := a.Capture(context.Background(), 9, h.session.id, h.lifetime, a.manager.urlFor(h.generation), h.revision); err == nil {
			t.Fatal("sealed mint admitted")
		}
	})
	t.Run("same-session-replacement", func(t *testing.T) {
		a, h, _ := accessFixture(t, 3, 2)
		m := a.manager
		old := h.generation
		next := &generation{sess: h.session, src: &accessTestSource{size: 10}, token: "replacement", observation: old.observation, tmap: old.tmap}
		m.mu.Lock()
		h.session.mu.Lock()
		h.session.cur = next
		h.session.mu.Unlock()
		m.mu.Unlock()
		if hold, err := h.Acquire(core.WithReadRetirement(context.Background(), &core.ReadRetirement{})); err == nil || hold.Release != nil {
			t.Fatal("adopted replacement generation")
		}
		if _, err := a.Capture(context.Background(), 9, h.session.id, h.lifetime, m.urlFor(old), h.revision); err == nil {
			t.Fatal("minted historical generation")
		}
		replacement := &session{id: h.session.id, sources: newSourceLifetime(9, "plex", "other"), ident: identity{userID: 9}, cur: next}
		m.mu.Lock()
		m.sessions[replacement.id] = replacement
		m.mu.Unlock()
		if _, err := h.Acquire(core.WithReadRetirement(context.Background(), &core.ReadRetirement{})); err == nil {
			t.Fatal("adopted new lifetime with same id")
		}
		if _, err := a.Capture(context.Background(), 9, replacement.id, h.lifetime, m.urlFor(next), h.revision); err == nil {
			t.Fatal("minted replacement lifetime")
		}
		old.retire()
	})
	t.Run("viewer-and-canonical-revision", func(t *testing.T) {
		a, h, src := accessFixture(t, 4, 1)
		for _, bad := range []struct {
			viewer int64
			rev    string
		}{{8, h.revision}, {9, "caller-chosen"}} {
			if _, err := a.Capture(context.Background(), bad.viewer, h.session.id, h.lifetime, a.manager.urlFor(h.generation), bad.rev); err == nil {
				t.Fatal("invalid ownership/revision admitted")
			}
		}
		src.size++ // fresh canonical validation fails AFTER retaining responsibility
		hold, err := h.Acquire(core.WithReadRetirement(context.Background(), &core.ReadRetirement{}))
		if !errors.Is(err, core.ErrSourceChanged) || hold.Release == nil {
			t.Fatal("changed bytes lost acquired responsibility")
		}
		accessSeal(a, h)
		assertRetirementOpen(t, h.lifetime.Done(), "error hold")
		hold.Release()
		if a.Reconcile() != 0 {
			t.Fatal("error hold not released")
		}
		waitRetirementTest(t, h.lifetime.Done(), "error cleanup")
	})
}

func TestSourceAccessProducerAndCloseAccounting(t *testing.T) {
	for _, delayed := range []bool{false, true} {
		t.Run(map[bool]string{false: "blocked-sync-read", true: "delayed-producer-error"}[delayed], func(t *testing.T) {
			a, h, src := accessFixture(t, 1, 1)
			src.delayed = delayed
			src.entered, src.unblock = make(chan struct{}), make(chan struct{})
			src.closeEntered, src.closeUnblock = make(chan struct{}), make(chan struct{})
			var unblockOnce, closeOnce sync.Once
			unblock := func() { unblockOnce.Do(func() { close(src.unblock) }) }
			closeUnblock := func() { closeOnce.Do(func() { close(src.closeUnblock) }) }
			defer unblock()
			defer closeUnblock()
			hold, tracker := accessAcquire(t, h)
			// A different context's tracker cannot steal ownership from acquisition.
			alien := &core.ReadRetirement{}
			readDone := make(chan struct{})
			if delayed {
				_, err := hold.Source.(core.ContextReaderAt).ReadAtContext(core.WithReadRetirement(context.Background(), alien), make([]byte, 1), 0)
				if err == nil || src.producerDone == nil {
					t.Fatal("missing delayed producer")
				}
				close(readDone)
			} else {
				go func() {
					defer close(readDone)
					_, _ = hold.Source.(core.ContextReaderAt).ReadAtContext(core.WithReadRetirement(context.Background(), alien), make([]byte, 1), 0)
				}()
				waitRetirementTest(t, src.entered, "read entered")
			}
			// Release remains bounded even while ALL manager/source locks are held.
			a.manager.mu.Lock()
			h.session.mu.Lock()
			h.lifetime.mu.Lock()
			h.generation.mu.Lock()
			a.mu.Lock()
			hold.Release()
			hold.Release()
			a.mu.Unlock()
			h.generation.mu.Unlock()
			h.lifetime.mu.Unlock()
			h.session.mu.Unlock()
			a.manager.mu.Unlock()
			accessSeal(a, h)
			assertRetirementOpen(t, tracker.Seal(), "held read tracker")
			assertRetirementOpen(t, h.lifetime.Done(), "held source lifetime")
			if a.Reconcile() != 1 || src.closes.Load() != 0 {
				t.Fatal("unresolved producer released source")
			}
			if _, err := hold.Source.ReadAt(make([]byte, 1), 0); err == nil {
				t.Fatal("read after Release admitted")
			}
			if delayed {
				src.producerDone()
				src.producerDone()
			} else {
				unblock()
				waitRetirementTest(t, readDone, "read stopped")
			}
			src.onClose = func() {
				a.manager.mu.Lock()
				a.manager.mu.Unlock() //nolint:staticcheck // barrier: Close must run with this lock free
				h.session.mu.Lock()
				h.session.mu.Unlock() //nolint:staticcheck // barrier: Close must run with this lock free
				h.lifetime.mu.Lock()
				h.lifetime.mu.Unlock() //nolint:staticcheck // barrier: Close must run with this lock free
				a.mu.Lock()
				a.mu.Unlock() //nolint:staticcheck // barrier: Close must run with this lock free
			}
			reconciled := make(chan struct{})
			go func() { defer close(reconciled); a.Reconcile() }()
			waitRetirementTest(t, src.closeEntered, "Close entered outside locks")
			if a.Reconcile() != 1 {
				t.Fatal("concurrent reconcile forgot outstanding Close")
			}
			assertRetirementOpen(t, h.lifetime.Done(), "Close pending")
			closeUnblock()
			waitRetirementTest(t, reconciled, "reconciled")
			if a.Reconcile() != 0 || src.closes.Load() != 1 {
				t.Fatal("duplicate generation release")
			}
			waitRetirementTest(t, h.lifetime.Done(), "source retired")
			if h.lifetime.Err() != nil {
				t.Fatal(h.lifetime.Err())
			}
		})
	}
}

func TestSourceAccessCapacity(t *testing.T) {
	a, h, _ := accessFixture(t, 1, 1)
	if _, err := a.Capture(context.Background(), 9, h.session.id, h.lifetime, a.manager.urlFor(h.generation), h.revision); err == nil {
		t.Fatal("handle cap bypassed")
	}
	hold, tracker := accessAcquire(t, h)
	done, ok := tracker.Begin()
	if !ok {
		t.Fatal("tracker sealed early")
	}
	if _, err := h.Acquire(core.WithReadRetirement(context.Background(), &core.ReadRetirement{})); err == nil {
		t.Fatal("hold cap bypassed")
	}
	hold.Release()
	if a.Reconcile() != 1 {
		t.Fatal("pending hold evicted")
	}
	done()
	if a.Reconcile() != 0 {
		t.Fatal("capacity not restored")
	}
	hold, _ = accessAcquire(t, h)
	h.Revoke()
	h.Revoke()
	if _, err := h.Acquire(core.WithReadRetirement(context.Background(), &core.ReadRetirement{})); err == nil {
		t.Fatal("revoked handle admitted")
	}
	if _, err := a.Capture(context.Background(), 9, h.session.id, h.lifetime, a.manager.urlFor(h.generation), h.revision); err != nil {
		t.Fatal("handle capacity not restored", err)
	}
	accessSeal(a, h)
	hold.Release()
	a.Reconcile()
}

func TestSourceAccessRange(t *testing.T) {
	a, h, _ := accessFixture(t, 1, 1)
	hold, _ := accessAcquire(t, h)
	defer func() { accessSeal(a, h); hold.Release(); a.Reconcile() }()
	for _, tc := range []struct {
		off     int64
		size, n int
		err     error
	}{
		{9, 2, 1, io.EOF}, {10, 1, 0, io.EOF}, {10, 0, 0, nil}, {0, 0, 0, nil}, {9, 1, 1, nil},
	} {
		n, err := hold.Source.ReadAt(make([]byte, tc.size), tc.off)
		if n != tc.n || !errors.Is(err, tc.err) {
			t.Fatalf("off=%d size=%d: (%d,%v), want (%d,%v)", tc.off, tc.size, n, err, tc.n, tc.err)
		}
	}
	if hold.Source.Size() != 10 || hold.Source.Close() == nil {
		t.Fatal("facade size/Close contract")
	}
}

func TestSourceHandleActiveBarrier(t *testing.T) {
	a, h, _ := accessFixture(t, 1, 1)
	if !h.Active() {
		t.Fatal("live handle inactive")
	}
	h.Revoke()
	if h.Active() {
		t.Fatal("revoked handle active")
	}
	accessSeal(a, h)
	if h.Active() {
		t.Fatal("sealed handle active")
	}
	var zero *SourceHandle
	if zero.Active() || new(SourceHandle).Active() {
		t.Fatal("zero handle active")
	}
}
