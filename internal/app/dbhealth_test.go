package app

import (
	"context"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestDatabaseHealthReportsHeldProgress(t *testing.T) {
	if h := (&App{cluster: cluster.NewSolo(cluster.Local{}, nil)}).databaseHealth(); h.Clustered || h.State != "ok" || !h.Reachable {
		t.Fatalf("standalone %+v", h)
	}
	db := store.OpenTest(t)
	avail := store.NewAvailability(nil)
	fm := &fakeMembers{Solo: cluster.NewSolo(cluster.Local{Identity: cluster.Identity{NodeID: "self"}}, nil)}
	a := &App{cluster: fm, dbAvail: avail, state: state.New(db, state.Options{Availability: avail})}
	defer a.state.Close(context.Background())
	if h := a.databaseHealth(); !h.Clustered || h.State != "ok" || h.Held != 0 {
		t.Fatalf("healthy %+v", h)
	}
	avail.Fail(syscall.ECONNREFUSED)
	if err := a.state.Progress(context.Background(), 1, core.MovieKey(1), 10*time.Minute, time.Hour, "playing"); err != nil {
		t.Fatal(err)
	}
	h := a.databaseHealth()
	if h.State != "degraded" || h.Reachable || h.DownSince == nil || h.Held != 1 || len(h.Outboxes) != 1 ||
		!strings.Contains(h.Message, "progress is being held") {
		t.Fatalf("degraded %+v", h)
	}
	avail.OK()
	deadline := time.Now().Add(5 * time.Second)
	for a.databaseHealth().State != "ok" {
		if time.Now().After(deadline) {
			t.Fatalf("never recovered: %+v", a.databaseHealth())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDatabaseRecheckPreservesFindingsOnFailure(t *testing.T) {
	db := store.OpenTest(t)
	a := &App{db: db, log: quietLog(), cluster: cluster.NewSolo(cluster.Local{}, nil)}
	prior := store.Integrity{Findings: []string{"preserved evidence"}, Checked: time.Now()}
	a.recordIntegrity(prior)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.checkDatabase(ctx); err == nil {
		t.Fatal("cancelled check succeeded")
	}
	if a.integrity.Load().OK || a.integrity.Load().Findings[0] != "preserved evidence" {
		t.Fatal("cancel erased evidence")
	}
	a.integrityChecking.Store(true)
	if _, err := a.checkDatabase(context.Background()); err == nil {
		t.Fatal("concurrent check admitted")
	}
	a.integrityChecking.Store(false)
	if _, err := a.checkDatabase(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !a.integrity.Load().OK {
		t.Fatal("successful check did not replace stale alarm")
	}
	a.recordIntegrity(prior)
	db.Close()
	if _, err := a.checkDatabase(context.Background()); err == nil {
		t.Fatal("closed database check succeeded")
	}
	if a.integrity.Load().OK {
		t.Fatal("failed check erased evidence")
	}
}
