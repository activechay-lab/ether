package app

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/config"
)

type readinessHeartbeatCluster struct {
	cluster.Cluster
	age   time.Duration
	owned bool
}

func (c *readinessHeartbeatCluster) HeartbeatAge() (time.Duration, bool) { return c.age, c.owned }

func TestDeliveryReadinessClockAndLateHeartbeatInvariant(t *testing.T) {
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	roles := config.NodeRoles{config.RoleDelivery}
	solo := cluster.NewSolo(cluster.Local{Identity: cluster.Identity{NodeID: "delivery"}, Roles: roles}, nil)
	defer solo.Close()
	c := &readinessHeartbeatCluster{Cluster: solo, owned: true}
	p := &deploymentRuntime{settings: clusterconfig.Deployment{Roles: roles}}
	p.started.Store(true)
	a := &App{cfg: cfg, cluster: c, deployment: p}
	// No placement service is installed: established delivery must never need
	// an empty target write, even with the quota operation's deadline exhausted.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var window privateReadinessWindow
	now := time.Now()
	check := func(at time.Time, want bool) privateReadinessSnapshot {
		t.Helper()
		var observation privateReadinessSnapshot
		err := a.reconcilePrivatePlacement(ctx, &observation)
		if got := window.ready(at, err == nil, true, observation.reason); got != want {
			t.Fatalf("age=%v elapsed=%v stage=%s ready=%v want=%v", c.age, at.Sub(now), observation.reason, got, want)
		}
		return observation
	}
	check(now, true)
	// Freshness here is independent of the persisted wall-clock timestamp;
	// the production Postgres receipt invariant is checked in cluster's tests.
	c.age = 12 * time.Second // one missed 5s heartbeat, with scheduling/PG delay
	check(now.Add(12*time.Second), true)
	c.age = 21 * time.Second
	if s := check(now.Add(21*time.Second), true); s.reason != readinessStale {
		t.Fatalf("lost stale diagnostic: %+v", s)
	}
	check(now.Add(40*time.Second), true)
	check(now.Add(41*time.Second), false) // bounded, never indefinitely healthy
	c.age = 0
	check(now.Add(42*time.Second), true)
	c.owned = false
	check(now.Add(43*time.Second), false) // fatal ownership loss gets no grace
	c.owned = true
	check(now.Add(44*time.Second), true)
	if window.ready(now.Add(45*time.Second), true, false, readinessPublic) {
		t.Fatal("failed public service received grace")
	}
	if window.ready(now.Add(46*time.Second), false, true, readinessTarget) {
		t.Fatal("unestablished service received grace")
	}
}
