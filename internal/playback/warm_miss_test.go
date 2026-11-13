package playback

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func waitWarmGone(t *testing.T, h *harness, id identity) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		h.m.mu.Lock()
		e := h.m.warm[id]
		h.m.mu.Unlock()
		if e == nil {
			return
		}
		<-e.done
	}
	t.Fatal("warm-up did not finish")
}

// Reopening a detail page whose warm-up found no source must not search
// (and grab) again on every refresh; Play still resolves in full (#224).
func TestFailedWarmIsNotRepeatedOnEveryPageOpen(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("r1", 2<<30, 90*time.Minute)}, nil)
	caps := defaultCaps("client")
	id := identity{userID: owner.ID, clientID: caps.ClientID, key: movieKey, surface: h.m.playbackSurface(string(caps.Protocol))}
	if !h.m.Prewarm(context.Background(), owner, movieKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail}) {
		t.Fatal("first warm skipped")
	}
	waitWarmGone(t, h, id)
	if got := h.resolver.callCount(); got != 1 {
		t.Fatalf("resolver calls %d, want 1", got)
	}
	for i := 0; i < 5; i++ {
		h.clock.Advance(time.Minute)
		if h.m.Prewarm(context.Background(), owner, movieKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail}) {
			t.Fatal("a failed warm-up was repeated on the next page open")
		}
	}
	if got := h.resolver.callCount(); got != 1 {
		t.Fatalf("resolver calls %d after reopening, want 1", got)
	}
	// The memo expires.
	h.clock.Advance(warmMissTTL)
	if !h.m.Prewarm(context.Background(), owner, movieKey, caps, core.PrewarmOptions{Reason: core.PrewarmDetail}) {
		t.Fatal("the warm-up memo did not expire")
	}
	waitWarmGone(t, h, id)
	// Play is not held back by the memo.
	h.resolver.mu.Lock()
	h.resolver.candidates = []core.Release{{ID: "r1", Name: "Release.r1"}}
	h.resolver.mu.Unlock()
	if _, err := h.m.Prepare(context.Background(), owner, movieKey, caps); err != nil {
		t.Fatalf("play after a failed warm-up: %v", err)
	}
}
