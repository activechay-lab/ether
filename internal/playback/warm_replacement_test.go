package playback

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestWarmFailureReplacement(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10000000, time.Minute), mkv("B", 10000000, time.Minute)}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "warm-replace")
	s := h.m.lookup(ps.ID)
	old := s.current().res.Release
	if err := h.m.ReplaceWarmSource(t.Context(), owner, movieKey, s.caps, old, 30*time.Second, time.Minute, core.ErrArticleMissing); err != nil {
		t.Fatal(err)
	}
	if !h.resolver.isBad("A") || !h.m.titleReleaseRejected(t.Context(), movieKey, old) {
		t.Fatal("failed source not rejected")
	}
	rp, err := h.m.ResumePoint(t.Context(), owner.ID, movieKey)
	if err != nil || rp == nil || rp.ReleaseID() != "B" {
		t.Fatalf("replacement not remembered: %+v %v", rp, err)
	}
	h.engine.mu.Lock()
	defer h.engine.mu.Unlock()
	if len(h.engine.prefetch) == 0 {
		t.Fatal("replacement not warmed")
	}
	call := h.engine.prefetch[len(h.engine.prefetch)-1]
	if call.ReleaseID != "B" {
		t.Fatalf("wrong warmed release: %+v", call)
	}
}
