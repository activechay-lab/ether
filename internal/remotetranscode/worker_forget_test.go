package remotetranscode

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// One worker boot accepts far more starts than Capacity over its lifetime:
// retired records whose generation placement has made terminal are forgotten,
// so a start is never refused for capacity and retained state stays flat.
// Unresolved or non-terminal records are kept, the per-session generation
// floor survives while that session has a record, and a forgotten identity
// reports retired without minting a stop-before-start tombstone.
func TestForgetBoundsLifetimeStarts(t *testing.T) {
	f := &fakeEncoder{}
	var mu sync.Mutex
	terminal := map[Identity]bool{}
	w, err := NewWorker(WorkerOptions{Boot: "worker-boot", Capacity: 4, MaxLease: time.Minute, Encoder: f,
		Security: Security{
			Authenticate: func(*http.Request) (string, error) { return "controller-boot", nil },
			Authorize:    func(context.Context, string, string, Identity, *Start) error { return nil },
		},
		Resolve: func(context.Context, string, Start) (Source, error) {
			return Source{URL: "https://10.0.0.2/input", Lease: &fakeLease{done: make(chan struct{})}}, nil
		},
		Terminal: func(_ context.Context, id Identity) bool { mu.Lock(); defer mu.Unlock(); return terminal[id] },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	w.forgetGrace = 0
	ctx := context.Background()
	start := func(session string, generation uint64) Start {
		return Start{Identity: Identity{Controller: "controller-boot", Worker: "worker-boot", Session: session, Generation: generation}, Grant: "opaque", Expires: time.Now().Add(30 * time.Second), Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1}}
	}
	retire := func(s Start) {
		if state(t, rpc(w, "stop", s, true)) != "pending" {
			t.Fatal("stop manufactured proof")
		}
		close(f.session.done)
		if state(t, rpc(w, "status", s, true)) != "retired" {
			t.Fatal("not retired")
		}
	}
	forget := func() int { return w.Forget(ctx, 64) + w.Forget(ctx, 64) } // mark, then forget
	for i := 0; i < 10000; i++ {
		s := start(fmt.Sprintf("s%d", i), 1)
		if got := state(t, rpc(w, "start", s, true)); got != "running" {
			t.Fatalf("start %d refused: %s", i, got)
		}
		retire(s)
		mu.Lock()
		terminal[s.Identity] = true
		mu.Unlock()
		if forget() != 1 || w.Records() != 0 {
			t.Fatalf("start %d: %d records retained", i, w.Records())
		}
		mu.Lock()
		delete(terminal, s.Identity)
		mu.Unlock()
	}
	if len(w.highest) != 0 {
		t.Fatalf("generation floors grew: %d", len(w.highest))
	}

	// Retired but not durably terminal: kept.
	kept := start("kept", 1)
	if state(t, rpc(w, "start", kept, true)) != "running" {
		t.Fatal("start")
	}
	retire(kept)
	if forget() != 0 || w.Records() != 1 {
		t.Fatal("forgot a record placement has not made terminal")
	}
	// Generation 2 of the same session starts; generation 1 becomes terminal
	// and is forgotten, but the session's floor stays with generation 2.
	next := start("kept", 2)
	if state(t, rpc(w, "start", next, true)) != "running" {
		t.Fatal("replacement start")
	}
	mu.Lock()
	terminal[kept.Identity] = true
	mu.Unlock()
	if forget() != 1 || w.Records() != 1 {
		t.Fatal("terminal generation 1 not forgotten")
	}
	if rpc(w, "start", start("kept", 1), true).Code != 409 {
		t.Fatal("late start for a forgotten generation admitted below the floor")
	}
	// The forgotten identity reports retired and mints no tombstone.
	if state(t, rpc(w, "stop", kept, true)) != "retired" || w.Records() != 1 {
		t.Fatal("forgotten identity minted a stop-before-start tombstone")
	}
	// A running record is never forgotten, even when marked terminal.
	mu.Lock()
	terminal[next.Identity] = true
	mu.Unlock()
	if forget() != 0 || w.Records() != 1 {
		t.Fatal("forgot a running record")
	}
}
