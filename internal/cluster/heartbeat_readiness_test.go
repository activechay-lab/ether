package cluster

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

// Both peer freshness and this process's readiness survive local wall steps.
// Persisted membership uses database time; local readiness uses its receipt.
func TestHeartbeatReadinessUsesMonotonicReceipt(t *testing.T) {
	wall := time.Now()
	var protocol uint32
	p, err := NewPostgres(Options{DB: store.OpenTest(t), Local: Local{Identity: Identity{NodeID: "delivery"}, Roles: config.NodeRoles{config.RoleDelivery}, Protocol: func() uint32 { return protocol }}, Now: func() time.Time { return wall }, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.HeartbeatAge(); ok {
		t.Fatal("unjoined node healthy")
	}
	if err := p.beat(context.Background()); err != nil {
		t.Fatal(err)
	}
	protocol = 1 // listener is usable, but the next heartbeat has not run
	if p.PublishedProtocol(1) || !p.Self().RemoteCompatible(1) {
		t.Fatal("local capability confused with published capability")
	}
	nodes, err := p.AllNodes(context.Background())
	if err != nil || len(nodes) != 1 || nodes[0].RemoteCompatible(1) || nodes[0].Incarnation != p.inc {
		t.Fatalf("fresh exact boot unexpectedly authorized: %v %v", nodes, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.beat(canceled); err == nil || p.PublishedProtocol(1) {
		t.Fatal("failed heartbeat published capability")
	}
	for _, step := range []time.Duration{0, -3 * time.Second, 6 * time.Second} {
		wall = wall.Add(step)
		if err := p.beat(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !p.PublishedProtocol(1) {
			t.Fatal("successful owned heartbeat did not publish capability")
		}
		age, ok := p.HeartbeatAge()
		if !ok || age < 0 || age > 10*time.Second {
			t.Fatalf("wall step %v changed local freshness: %v %v", step, age, ok)
		}
		if absClock(time.Since(p.Self().LastSeen)) > time.Second {
			t.Fatal("heartbeat used the writer's local clock")
		}
		nodes, err := p.AllNodes(context.Background())
		if err != nil || len(nodes) != 1 || !nodes[0].Live || !nodes[0].RemoteCompatible(1) {
			t.Fatalf("step %s changed membership: %v %v", step, nodes, err)
		}
		if age := nodes[0].LastSeenAge(wall); age < 0 || age > time.Second {
			t.Fatalf("step %s changed DB age: %s", step, age)
		}
	}
	if _, err := p.db.Exec(`UPDATE cluster_nodes SET last_seen=last_seen-21000`); err != nil {
		t.Fatal(err)
	}
	nodes, err = p.AllNodes(context.Background())
	if err != nil || len(nodes) != 1 || nodes[0].Live {
		t.Fatalf("genuinely stale heartbeat accepted: %v %v", nodes, err)
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
	if p.PublishedProtocol(1) {
		t.Fatal("fatal membership retained publication readiness")
	}
}
