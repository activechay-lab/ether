package onair

import (
	"context"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
)

// Cancelling a channel stops its running render, drops its queued renders
// and releases their pins; another channel's work is untouched.
func TestCancelChannelStopsQueuedAndRunningRenders(t *testing.T) {
	started := make(chan struct{}, 4)
	var renders atomic.Int64
	s := testService(t, Options{
		StaticRequests: func(context.Context, string) []core.ClipRequest { return []core.ClipRequest{{Moment: "disabled"}} },
		Render: func(ctx context.Context, _ scene.Document, _ raster.Bindings, out string) error {
			renders.Add(1)
			started <- struct{}{}
			<-ctx.Done()
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return os.WriteFile(out, []byte("master"), 0o600)
		},
	})
	gone := []core.ClipRequest{
		{LookID: "nocturne", Moment: "ident", ChannelID: "gone"},
		{LookID: "nocturne", Moment: "upnext", ChannelID: "gone", Bind: map[string]string{"next.title": "Programme", "next.time": "20:00"}},
	}
	for _, r := range gone {
		s.Ensure(t.Context(), r)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("render never started")
	}
	s.CancelChannel("gone")
	// The cancelled lane must free: a drain job behind everything runs.
	drained := make(chan struct{})
	if !s.enqueue(renderJob{Hash: "cancel-test-drain", prepare: func(context.Context) error { close(drained); return nil }}) {
		t.Fatal("render queue unavailable")
	}
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled renders never left the lane")
	}
	if got := renders.Load(); got != 1 {
		t.Fatalf("cancelled channel rendered %d masters, want only the interrupted one", got)
	}
	for _, r := range gone {
		if _, ok := s.Clip(t.Context(), r); ok {
			t.Fatalf("cancelled render for %s landed in the cache", r.Moment)
		}
	}
	var hashes []string
	for _, r := range gone {
		if j, ok := s.plan(r); ok {
			hashes = append(hashes, j.Hash)
		}
	}
	s.mu.Lock()
	pending, pins := len(s.pending), 0
	for _, h := range hashes {
		if _, pinned := s.pins[h]; pinned {
			pins++
		}
	}
	s.mu.Unlock()
	if pending != 0 || pins != 0 {
		t.Fatalf("after cancel: %d pending, %d pins remain", pending, pins)
	}
}
