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

func TestCachedMastersDoNotRenderAgain(t *testing.T) {
	var renders atomic.Int64
	s := testService(t, Options{StaticRequests: func(context.Context, string) []core.ClipRequest { return []core.ClipRequest{{Moment: "disabled"}} }, Render: func(_ context.Context, _ scene.Document, _ raster.Bindings, out string) error {
		renders.Add(1)
		return os.WriteFile(out, []byte("master"), 0600)
	}})
	requests := []core.ClipRequest{{LookID: "nocturne", Moment: "ident", ChannelID: "cache"}, {LookID: "nocturne", Moment: "upnext", ChannelID: "cache", Bind: map[string]string{"next.title": "Programme", "next.time": "20:00"}}}
	for _, request := range requests {
		s.Ensure(t.Context(), request)
		deadline := time.Now().Add(time.Second)
		for {
			if _, ok := s.Clip(t.Context(), request); ok {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("master not ready")
			}
			time.Sleep(time.Millisecond)
		}
		for range 100 {
			s.Ensure(t.Context(), request)
		}
	}
	// Drain all preceding work before checking: a duplicate must not merely be
	// queued for later execution after Ensure returns.
	drained := make(chan struct{})
	if !s.enqueue(renderJob{Hash: "cache-test-drain", prepare: func(context.Context) error { close(drained); return nil }}) {
		t.Fatal("render queue unavailable")
	}
	select {
	case <-drained:
	case <-time.After(time.Second):
		t.Fatal("render queue did not drain")
	}
	if got := renders.Load(); got != 2 {
		t.Fatalf("ident/up-next rendered %d times", got)
	}
	s.mu.Lock()
	s.renderTimes["nocturne/upnext"] = 2 * time.Minute
	s.mu.Unlock()
	if s.RenderLead("nocturne", "upnext") < 8*time.Minute || s.RenderLead("nocturne", "") < 8*time.Minute || s.RenderLead("nocturne", "ident") < 3*time.Minute {
		t.Fatal("render lead did not cover measured work")
	}
}
