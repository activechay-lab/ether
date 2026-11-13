package playback

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// A play request that gives up must not decide for the next one (#304):
// Plex Web drops its decision request, then start.mpd or a retry asks for
// the same title. The next caller joins the running preparation (a single
// open, no inherited context.Canceled). When nobody comes back, the
// prepared source is released once the grace ends.
func TestAbandonedPlayPreparationIsJoinedOrReleased(t *testing.T) {
	for _, rejoin := range []bool{true, false} {
		t.Run(map[bool]string{true: "rejoined", false: "unclaimed"}[rejoin], func(t *testing.T) {
			db := store.OpenTest(t)
			h := newHarness(t, []*fakeRelease{mkv("A", 10000, time.Minute)}, []string{"A"}, func(h *harness, o *Options) {
				h.db = db
				o.AbandonedPrepareGrace = 200 * time.Millisecond
			})
			e := &ownedTestEngine{Engine: h.engine, openEntered: make(chan struct{}), openRelease: make(chan struct{})}
			var once sync.Once
			unblock := func() { once.Do(func() { close(e.openRelease) }) }
			defer unblock()
			h.m.deps.Engine = e
			caps := defaultCaps("web")

			decision, dropDecision := context.WithCancel(context.Background())
			first := make(chan error, 1)
			go func() {
				_, err := h.m.PrepareWithin(decision, 0, owner, movieKey, caps, "", false)
				first <- err
			}()
			waitRetirementTest(t, e.openEntered, "source open")
			h.m.mu.Lock()
			var original *flight
			for _, f := range h.m.flights {
				original = f
			}
			h.m.mu.Unlock()
			dropDecision()
			if err := <-first; !errors.Is(err, context.Canceled) {
				t.Fatalf("dropped decision: %v", err)
			}
			if !rejoin {
				unblock()
				waitFor(t, func() bool {
					h.m.mu.Lock()
					defer h.m.mu.Unlock()
					return len(h.m.flights) == 0 && len(h.m.sessions) == 0
				})
				return
			}
			type result struct {
				ps  *core.PlaySession
				err error
			}
			start := make(chan result, 1)
			go func() {
				ps, err := h.m.Prepare(context.Background(), owner, movieKey, caps)
				start <- result{ps, err}
			}()
			waitFor(t, func() bool {
				h.m.mu.Lock()
				defer h.m.mu.Unlock()
				for _, f := range h.m.flights {
					if f.waiters == 1 {
						if f != original {
							t.Fatal("start began a second preparation instead of joining the dropped one")
						}
						return true
					}
				}
				return false
			})
			unblock()
			got := <-start
			if got.err != nil || got.ps == nil {
				t.Fatalf("start after a dropped decision: %+v", got)
			}
			if n := h.engine.openCount(); n != 1 {
				t.Fatalf("opens=%d, want the dropped decision's open reused", n)
			}
		})
	}
}
