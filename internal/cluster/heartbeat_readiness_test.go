package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// The wall clock stored for peers must never become the clock used for this
// process's own readiness, including after slow heartbeat writes complete.
func TestHeartbeatReadinessUsesMonotonicReceipt(t *testing.T) {
	wall := time.Now()
	p, err := NewPostgres(Options{DB: store.OpenTest(t), Local: Local{Identity: Identity{NodeID: "delivery"}}, Now: func() time.Time { return wall }, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.HeartbeatAge(); ok {
		t.Fatal("unjoined node healthy")
	}
	for _, step := range []time.Duration{0, -time.Hour, 2 * time.Hour} {
		wall = wall.Add(step)
		if err := p.beat(context.Background()); err != nil {
			t.Fatal(err)
		}
		age, ok := p.HeartbeatAge()
		if !ok || age < 0 || age > 10*time.Second {
			t.Fatalf("wall step %v changed local freshness: %v %v", step, age, ok)
		}
		if !p.Self().LastSeen.Equal(wall) {
			t.Fatal("peer timestamp unexpectedly rewritten")
		}
	}
	p.mu.Lock()
	p.beatReceipt = time.Now().Add(-21 * time.Second)
	p.mu.Unlock()
	if age, ok := p.HeartbeatAge(); !ok || age < 21*time.Second {
		t.Fatalf("stalled heartbeat not aged: %v %v", age, ok)
	}
	p.mu.Lock()
	p.fatal = ErrDuplicateNode
	p.mu.Unlock()
	if _, ok := p.HeartbeatAge(); ok {
		t.Fatal("fatal membership remained healthy")
	}
}
