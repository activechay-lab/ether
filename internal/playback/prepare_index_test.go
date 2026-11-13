package playback

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestAdmissionIndexJoinIsBoundedAndCancellable(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("r1", 1<<20, time.Minute)}, []string{"r1"})
	ps := h.prepare(owner, movieKey, "index-join")
	s := h.m.lookup(ps.ID)
	g := s.current()
	<-g.indexReady
	// Simulate an existing metadata read that has not completed. The join
	// must not start another read, and must respect PrepareWithin's budget.
	ready := g.indexReady
	g.indexReady = make(chan struct{})
	defer func() { g.indexReady = ready }()
	caps := defaultCaps("index-join")
	caps.AwaitAdmissionIndex = true
	start := time.Now()
	got, _, err := h.m.snapshotForAdmission(context.Background(), s, caps, start, 20*time.Millisecond)
	if err != nil || got == nil {
		t.Fatalf("budget fallback: %v", err)
	}
	if elapsed := time.Since(start); elapsed < 15*time.Millisecond || elapsed > 250*time.Millisecond {
		t.Fatalf("budget join %v", elapsed)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start = time.Now()
	if _, _, err := h.m.snapshotForAdmission(ctx, s, caps, start, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatal("cancellation waited for index")
	}
}

func TestHLSAdmissionIncludesCompletedContainerIndex(t *testing.T) {
	b, want := indexedMKV()
	rel := &fakeRelease{ID: "K", Name: "K.mkv", Size: int64(len(b)), Duration: 10 * time.Second, Content: b}
	h := newHarness(t, []*fakeRelease{rel}, []string{"K"})
	caps := defaultCaps("index-ready")
	caps.AwaitAdmissionIndex = true
	ps, err := h.m.Prepare(context.Background(), owner, movieKey, caps)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps.Info.Keyframes) != len(want) {
		t.Fatalf("admitted %d keyframes, want %d", len(ps.Info.Keyframes), len(want))
	}
}
