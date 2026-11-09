package placement

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// age moves every ledger timestamp back, as if d had elapsed on the DB clock.
func age(t *testing.T, db *sql.DB, d time.Duration) {
	t.Helper()
	ms := d.Milliseconds()
	if _, err := db.Exec(`UPDATE placement_sessions SET deadline=deadline-?,initial_deadline=initial_deadline-?,retired_at=CASE WHEN retired_at>0 THEN retired_at-? ELSE 0 END`, ms, ms, ms); err != nil {
		t.Fatal(err)
	}
}

// Pruning never deletes what a live session, a displaced (still charged)
// failover generation or a recent retirement needs, and a compacted session
// can never be re-issued a generation that an old fence might still carry.
func TestPruneKeepsLiveDisplacedAndFencesCompactedGenerations(t *testing.T) {
	s, db := fixture(t)
	ctx := context.Background()
	for _, scope := range []string{"gpu", "streams"} {
		if err := s.SetPool(ctx, scope, config.RoleTranscode, 16); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetTarget(ctx, Target{"spare", "boot-s"}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"gpu", "streams"}}); err != nil {
		t.Fatal(err)
	}
	s.options.MaxSessions = 3
	done := mustReserve(t, s, request("done"))
	failed := mustReserve(t, s, request("failover"))
	live := mustReserve(t, s, request("live"))
	if err := s.Retire(ctx, done.Fence, "authenticated-retirement"); err != nil {
		t.Fatal(err)
	}
	replacement := request("failover")
	replacement.Operation, replacement.ExpectedGeneration, replacement.Target = "recover", failed.Generation, Target{"spare", "boot-s"}
	moved, err := s.Supersede(ctx, failed.Fence, replacement, func(context.Context, *sql.Tx, Record) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Retire(ctx, moved.Fence, "authenticated-retirement"); err != nil {
		t.Fatal(err)
	}
	// Recently retired rows are retained.
	opts := PruneOptions{Age: MinRetention, Limit: MaxPruneBatch}
	if n, err := s.PruneTombstones(ctx, opts); err != nil || n != 0 {
		t.Fatalf("pruned recent retirement: %d %v", n, err)
	}
	age(t, db, 2*time.Hour)
	var dependents []Pruned
	opts.Dependents = func(_ context.Context, _ *sql.Tx, p []Pruned) error {
		dependents = append(dependents, p...)
		return nil
	}
	// "failover" is retired but its displaced generation is still charged;
	// "live" is live. Only "done" may go.
	if n, err := s.PruneTombstones(ctx, opts); err != nil || n != 1 || len(dependents) != 1 || dependents[0] != (Pruned{"done", done.Generation}) {
		t.Fatalf("pruned %d %v %+v", n, err, dependents)
	}
	if _, err = s.Lookup(ctx, "live"); err != nil {
		t.Fatalf("live session pruned: %v", err)
	}
	if got, err := s.Lookup(ctx, "failover"); err != nil || got.Generation != moved.Generation {
		t.Fatalf("displaced session pruned: %+v %v", got, err)
	}
	if err = s.RetireDisplaced(ctx, failed.Fence, "authenticated-retirement"); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PruneTombstones(ctx, opts); err != nil || n != 1 || dependents[1].Session != "failover" {
		t.Fatalf("resolved failover not pruned: %d %v %+v", n, err, dependents)
	}
	// A failed Dependents callback rolls the batch back.
	if err = s.Retire(ctx, live.Fence, "authenticated-retirement"); err != nil {
		t.Fatal(err)
	}
	age(t, db, 2*time.Hour)
	boom := errors.New("boom")
	if _, err = s.PruneTombstones(ctx, PruneOptions{Age: MinRetention, Limit: 1, Dependents: func(context.Context, *sql.Tx, []Pruned) error { return boom }}); err == nil {
		t.Fatal("dependent failure committed")
	}
	if _, err = s.Lookup(ctx, "live"); err != nil {
		t.Fatalf("rolled-back prune lost tombstone: %v", err)
	}
	if n, err := s.PruneTombstones(ctx, opts); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}

	// Old fences stay fenced after compaction: a delayed replacement for an
	// old generation is refused, and a fresh session reusing the public ID
	// starts above every compacted generation.
	stale := request("done")
	stale.Operation, stale.ExpectedGeneration = "late", done.Generation
	if _, err = s.Reserve(ctx, stale); !errors.Is(err, ErrFenced) {
		t.Fatalf("stale generation admitted after compaction: %v", err)
	}
	fresh := request("done")
	fresh.Operation = "reuse"
	again := mustReserve(t, s, fresh)
	if again.Generation <= moved.Generation || again.Fence == done.Fence {
		t.Fatalf("compacted generation re-issued: %d (floor source %d)", again.Generation, moved.Generation)
	}
	if err = s.Renew(ctx, done.Fence, time.Now().Add(time.Second)); !errors.Is(err, ErrFenced) {
		t.Fatalf("compacted fence renewed: %v", err)
	}
	// Three sessions were admitted against MaxSessions=3 and a fourth now
	// succeeded: only live rows count. Usage reports one live, no tombstones.
	used, retired, err := s.LedgerUsage(ctx)
	if err != nil || used != 1 || retired != 0 {
		t.Fatalf("usage %d/%d %v", used, retired, err)
	}
	if _, err = db.Exec(`UPDATE placement_ledger SET retained=99`); err != nil {
		t.Fatal(err)
	}
	if err = s.ReconcileRetained(ctx); err != nil {
		t.Fatal(err)
	}
	if _, retired, err = s.LedgerUsage(ctx); err != nil || retired != 0 {
		t.Fatalf("reconcile %d %v", retired, err)
	}
	if _, err = s.PruneTombstones(ctx, PruneOptions{Age: time.Minute, Limit: 1}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("short retention accepted: %v", err)
	}
}
