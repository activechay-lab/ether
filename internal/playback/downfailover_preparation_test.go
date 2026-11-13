package playback

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestDownfailoverPreparationAndMemory(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 300_000, time.Minute)}, []string{"A"})
	ps := h.prepare(owner, movieKey, "parallel")
	s := h.m.lookup(ps.ID)
	entered := make(chan string, 3)
	release := make(chan struct{})
	h.m.deps.FetchNZB = func(ctx context.Context, r core.Release) ([]byte, error) {
		entered <- r.ID
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return nil, core.ErrNotAvailable
	}
	done := make(chan struct{})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	go func() {
		defer close(done)
		h.m.raceAlternates(ctx, s, nil, []core.Release{{ID: "B"}, {ID: "C"}, {ID: "D"}}, nil, 3, nil, nil)
	}()
	for range 3 {
		select {
		case <-entered:
		case <-ctx.Done():
			t.Fatal("preparation waited for an earlier candidate")
		}
	}
	close(release)
	<-done
	bad := core.Release{ID: "incomplete"}
	h.m.rememberOpenFailure(s, bad, fmt.Errorf("release is incomplete: %w", core.ErrArticleMissing))
	other := &session{ident: s.ident}
	if !h.m.titleReleaseRejected(t.Context(), other.ident.key, bad) {
		t.Fatal("another session forgot incomplete release")
	}
	h.m.rememberOpenFailure(s, core.Release{ID: "timeout"}, context.DeadlineExceeded)
	if h.m.titleReleaseRejected(t.Context(), other.ident.key, core.Release{ID: "timeout"}) {
		t.Fatal("transient failure remembered as permanent")
	}
}
