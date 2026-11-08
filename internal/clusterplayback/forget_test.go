package clusterplayback

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

type doneLife struct{}

func (doneLife) Done() <-chan struct{} { return alreadyDone }
func (doneLife) Err() error            { return nil }

// One coordinator boot serves far more sessions than HistoryCapacity: once a
// session is retired in the ledger, its execution, owned source, preparation
// and media replica are forgotten after the grace period, so the admission
// guard never refuses and retained state stays flat. Live sessions, sessions
// with a displaced generation still charged, and entries inside the grace
// period are kept.
func TestForgetTerminalBoundsLifetimeSessions(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	ledger := retentionLedger(t, db)
	const capacity = 16
	r := &Runtime{o: Options{DB: db, Placement: ledger, HistoryCapacity: capacity, Self: nodewire.Identity{Cluster: "cluster-a", NodeID: "c", Boot: "cb"}},
		executions: map[string]*execution{}, sources: map[string]*ownedSource{}, preparations: map[string]*preparation{}, mediaSessions: map[string]*mediaReplica{}}
	past := time.Now().Add(-time.Second) // grace already elapsed
	track := func(rec placement.Record, retired bool) *execution {
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.executionSlotLocked(rec.Session) {
			t.Fatalf("session %s refused with %d tracked", rec.Session, len(r.executions))
		}
		route := Route{Fence: rec.Fence, PlayID: "play-" + rec.Session}
		x := &execution{route: route, reserved: true, retired: retired, done: alreadyDone, forgetAt: past}
		r.executions[rec.Session] = x
		r.sources[sourceKey(rec.Fence, 0)] = &ownedSource{route: route, playID: route.PlayID, life: doneLife{}, sealed: true, retiring: true, forgetAt: past}
		r.preparations["peer/boot/"+rec.Session] = &preparation{receipt: true, life: doneLife{}, forgetAt: past}
		r.mediaSessions[rec.Session] = &mediaReplica{route: route, forgetAt: past}
		return x
	}
	tracked := func() int {
		r.mu.Lock()
		defer r.mu.Unlock()
		return len(r.executions) + len(r.sources) + len(r.preparations) + len(r.mediaSessions)
	}
	for i := 0; i < 10000; i++ {
		rec := reserveFor(t, ledger, fmt.Sprintf("s%05d", i))
		track(rec, true)
		if err := ledger.Retire(ctx, rec.Fence, "proof"); err != nil {
			t.Fatal(err)
		}
		// A preparation becomes eligible only once the source sharing its
		// lifetime is gone (first pass), then serves its own grace period,
		// which the test elapses before the second pass.
		r.forgetTerminal(ctx)
		r.mu.Lock()
		for _, p := range r.preparations {
			p.forgetAt = past
		}
		r.mu.Unlock()
		r.forgetTerminal(ctx)
		if n := tracked(); n != 0 {
			t.Fatalf("session %d: %d entries retained", i, n)
		}
	}

	// Kept: a live session, a session whose displaced generation is still
	// charged, and a terminal session still inside its grace period.
	live := reserveFor(t, ledger, "live")
	track(live, false)
	failed := reserveFor(t, ledger, "failover")
	moved, err := ledger.Supersede(ctx, failed.Fence, placement.Request{Session: "failover", Operation: "recover", ExpectedGeneration: failed.Generation, Controller: failed.Controller, Target: placement.Target{NodeID: "w2", Boot: "b"}, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: "gpu", Units: 1}}, Lease: 30 * time.Second}, func(context.Context, *sql.Tx, placement.Record) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = ledger.Retire(ctx, moved.Fence, "proof"); err != nil {
		t.Fatal(err)
	}
	track(moved, true)
	recent := reserveFor(t, ledger, "recent")
	track(recent, true).forgetAt = time.Time{}
	if err = ledger.Retire(ctx, recent.Fence, "proof"); err != nil {
		t.Fatal(err)
	}
	r.forgetTerminal(ctx)
	r.mu.Lock()
	for _, id := range []string{"live", "failover", "recent"} {
		if r.executions[id] == nil {
			r.mu.Unlock()
			t.Fatalf("%s forgotten", id)
		}
	}
	if r.mediaSessions["live"] == nil || r.mediaSessions["failover"] == nil {
		r.mu.Unlock()
		t.Fatal("replica of a live or displaced session forgotten")
	}
	if r.sources[sourceKey(live.Fence, 0)] == nil || r.sources[sourceKey(moved.Fence, 0)] == nil {
		r.mu.Unlock()
		t.Fatal("source of a live or displaced session forgotten")
	}
	r.mu.Unlock()
	// Resolving the displaced generation makes "failover" terminal.
	if err = ledger.RetireDisplaced(ctx, failed.Fence, "proof"); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.executions["failover"].forgetAt = past
	r.sources[sourceKey(moved.Fence, 0)].forgetAt = past
	r.mu.Unlock()
	r.forgetTerminal(ctx)
	r.mu.Lock()
	gone := r.executions["failover"] == nil && r.sources[sourceKey(moved.Fence, 0)] == nil
	r.mu.Unlock()
	if !gone {
		t.Fatal("resolved failover not forgotten")
	}
	// A forgotten execution reads as retired to a held recovery session.
	s := &recoverySession{&bufferedSession{r: r, route: Route{Fence: placement.Fence{Session: "failover"}}}}
	if s.EncoderErr() != nil || !channelDone(s.EncoderDone()) {
		t.Fatal("forgotten execution not reported retired")
	}
}
