package cluster

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestRoleEligibilityAndPublication(t *testing.T) {
	now := time.Now()
	nodes := []Node{
		{ID: "old", LastSeen: now, Addrs: []string{"https://old"}},
		{ID: "worker", LastSeen: now, Addrs: []string{"https://private"}, Caps: Caps{Roles: config.NodeRoles{config.RoleTranscode}}},
		{ID: "delivery", LastSeen: now, Addrs: []string{"https://delivery"}, Caps: Caps{Roles: config.NodeRoles{config.RoleDelivery}}},
		{ID: "invalid", LastSeen: now, Addrs: []string{"https://invalid"}, Caps: Caps{Roles: config.NodeRoles{config.RoleServer, "unknown"}}},
	}
	got := PeerAddrs(nodes, "self", now, time.Minute)
	if len(got) != 1 || got[0] != "https://old" {
		t.Fatalf("published %v", got)
	}
	if !nodes[0].ServerEligible() || nodes[0].RemoteCompatible(0) || nodes[0].RemoteCompatible(1) {
		t.Fatal("legacy member eligibility changed")
	}
	n := Node{ProtocolVersion: 2}
	if !n.RemoteCompatible(2) || n.RemoteCompatible(1) {
		t.Fatal("protocol compatibility")
	}
}

func TestLocalRolesAreAuthoritative(t *testing.T) {
	roles := config.NodeRoles{config.RoleTranscode}
	local := Local{Identity: Identity{NodeID: "worker"}, Roles: roles, Caps: func() Caps {
		return Caps{Roles: config.NodeRoles{config.RoleServer}, Weight: 7, Transcode: true}
	}}
	p, err := NewPostgres(Options{DB: new(sql.DB), Local: local, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	roles[0] = config.RoleServer // constructor owns its snapshot
	self := p.Self()
	if self.ServerEligible() || self.Caps.Weight != 7 || !self.Caps.Transcode {
		t.Fatal("callback overrides roles or legacy caps")
	}
	self.Caps.Roles[0] = config.RoleServer
	if p.Self().ServerEligible() {
		t.Fatal("Self exposes mutable roles")
	}
	// Worker election must return before attempting any DB connection, even
	// when no eligible coordinator exists. The uninitialized pool would fail.
	if p.electionStep(context.Background(), nil) != nil || p.IsLeader() || p.Epoch() != 0 {
		t.Fatal("worker entered election")
	}
	local.Roles = config.NodeRoles{config.RoleTranscode}
	s := NewSolo(local, discardLogger())
	if s.Start(context.Background()) == nil || s.IsLeader() || s.Epoch() != 0 {
		t.Fatal("standalone worker accepted or elected")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.RunAsLeader(ctx, "singleton", func(context.Context) { t.Fatal("worker ran singleton") })
	a := NewSolo(Local{}, discardLogger()).Self()
	b := NewSolo(Local{}, discardLogger()).Self()
	if a.Incarnation == "" || a.Incarnation == b.Incarnation {
		t.Fatal("boot incarnation reused")
	}
}

func TestPostgresSpecialistEligibility(t *testing.T) {
	node := pgCluster(t)
	worker := node("worker", func(o *Options) {
		o.Local.Roles = config.NodeRoles{config.RoleTranscode, config.RoleBackground}
		o.Local.InternalAddress = "https://worker.internal:8443"
		o.Local.ProtocolVersion = 1
		o.Local.Addrs = func(context.Context) []string { return []string{"https://must-not-publish"} }
	})
	// Direct election steps are deterministic and must not acquire a lock.
	worker.electionStep(context.Background(), nil)
	if worker.IsLeader() {
		t.Fatal("worker leads alone")
	}
	server := node("server", nil)
	waitFor(t, "server leads", 5*time.Second, server.IsLeader)
	if server.othersCanLead(context.Background()) {
		t.Fatal("worker is a handoff candidate")
	}
	nodes, err := server.Nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range nodes {
		if n.ID != worker.Self().ID {
			continue
		}
		found = true
		if n.ServerEligible() || n.Leader || len(n.Addrs) != 0 || n.InternalAddress != "https://worker.internal:8443" || !n.RemoteCompatible(1) || n.Incarnation != worker.Self().Incarnation {
			t.Fatalf("bad worker membership: %+v", n)
		}
	}
	if !found {
		t.Fatal("worker membership missing")
	}
	server.StepDown(context.Background())
	worker.electionStep(context.Background(), nil)
	if worker.IsLeader() {
		t.Fatal("worker succeeded resigned server")
	}
}

// Exercise portable membership SQL offline, including both insert and update.
func TestSpecialistMembershipRoundTripSQLite(t *testing.T) {
	t.Setenv(store.TestPostgresEnv, "")
	db := store.OpenTest(t)
	local := Local{Identity: Identity{NodeID: "worker"}, Roles: config.NodeRoles{config.RoleSource}, InternalAddress: "https://source.internal:8443", ProtocolVersion: 1}
	p, err := NewPostgres(Options{DB: db, Local: local, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for range 2 {
		if err := p.beat(ctx); err != nil {
			t.Fatal(err)
		}
		ns, err := p.AllNodes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(ns) != 1 || ns[0].InternalAddress != local.InternalAddress || ns[0].ProtocolVersion != 1 || ns[0].Incarnation != p.Self().Incarnation || ns[0].ServerEligible() {
			t.Fatalf("membership mismatch: %+v", ns)
		}
	}
	server, err := NewPostgres(Options{DB: db, Local: Local{Identity: Identity{NodeID: "server"}}, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if server.othersCanLead(ctx) {
		t.Fatal("worker counted as coordinator")
	}
	if err := server.beat(ctx); err != nil {
		t.Fatal(err)
	}
	if !p.othersCanLead(ctx) {
		t.Fatal("legacy server cannot lead")
	}
	oldInc := p.Self().Incarnation
	restarted, err := NewPostgres(Options{DB: db, Local: local, Logger: discardLogger()})
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Self().ID != p.Self().ID || restarted.Self().Incarnation == oldInc {
		t.Fatal("restart identity/incarnation invariant")
	}
}
