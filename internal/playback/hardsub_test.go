package playback

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type subtitleResolver struct {
	*fakeResolver
	mu       sync.Mutex
	verdicts map[string]bool
	claimed  map[string]bool
	written  chan struct{}
	once     sync.Once
}

func (r *subtitleResolver) SubtitleVerdict(_ context.Context, id string) (bool, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.verdicts[id]
	return v, ok
}
func (r *subtitleResolver) RememberSubtitleVerdict(_ context.Context, id string, v bool) error {
	r.mu.Lock()
	r.verdicts[id] = v
	r.mu.Unlock()
	r.once.Do(func() { close(r.written) })
	return nil
}
func (r *subtitleResolver) ClaimSubtitleCheck(_ context.Context, id string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.claimed[id] {
		return false, nil
	}
	r.claimed[id] = true
	return true, nil
}

type subtitleTranscoder struct {
	core.Transcoder
	calls   atomic.Int64
	entered chan struct{}
	gate    chan struct{}
}

func (t *subtitleTranscoder) DetectHardSubs(ctx context.Context, _ string, _ time.Duration) (bool, bool) {
	if t.calls.Add(1) == 1 {
		close(t.entered)
	}
	select {
	case <-t.gate:
		return true, true
	case <-ctx.Done():
		return false, false
	}
}
func TestHardsubPostPictureDoesNotBlockStart(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("hidden", 10000, time.Minute)}, []string{"hidden"})
	r := &subtitleResolver{fakeResolver: h.resolver, verdicts: map[string]bool{}, claimed: map[string]bool{}, written: make(chan struct{})}
	tc := &subtitleTranscoder{Transcoder: h.m.deps.Transcoder, entered: make(chan struct{}), gate: make(chan struct{})}
	h.m.deps.Resolver = r
	h.m.deps.Transcoder = tc
	at := time.Now()
	ps := h.prepare(owner, movieKey, "c1")
	t.Logf("Prepare before frame check: %s", time.Since(at))
	if tc.calls.Load() != 0 {
		t.Fatal("decoder ran before first picture")
	}
	h.m.PlaybackProgress(owner.ID, "c1", movieKey, 0, "buffering")
	h.m.PlaybackProgress(owner.ID, "c1", movieKey, 0, "playing")
	if tc.calls.Load() != 0 {
		t.Fatal("decoder ran at first picture")
	}
	h.clock.Advance(11 * time.Second)
	at = time.Now()
	h.m.PlaybackProgress(owner.ID, "c1", movieKey, 11*time.Second, "playing")
	t.Logf("post-picture timeline dispatch: %s", time.Since(at))
	select {
	case <-tc.entered:
	case <-time.After(time.Second):
		t.Fatal("post-picture check not started")
	}
	// Hold the decoder indefinitely. A second viewer still prepares and sees the
	// existing first-picture accounting without waiting for this classification.
	at = time.Now()
	h.prepare(owner, movieKey, "c2")
	t.Logf("Prepare with decoder blocked: %s", time.Since(at))
	snap, ok := h.m.Session(ps.ID)
	if !ok || snap.Experience == nil || !snap.Experience.Pictured {
		t.Fatal("picture was not recorded before detector")
	}
	close(tc.gate)
	select {
	case <-r.written:
	case <-time.After(time.Second):
		t.Fatal("verdict not cached")
	}
	h.clock.Advance(time.Second)
	h.m.PlaybackProgress(owner.ID, "c1", movieKey, 12*time.Second, "playing")
	if tc.calls.Load() != 1 {
		t.Fatal("release checked more than once")
	}
}
