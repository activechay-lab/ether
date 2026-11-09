package clusterplayback

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

type retentionVerifier struct{}

func (retentionVerifier) VerifyRetired(context.Context, placement.Fence, string) error { return nil }

func retentionLedger(t *testing.T, db *sql.DB) *placement.Service {
	t.Helper()
	ctx := context.Background()
	s, err := placement.New(db, placement.Options{MaxSessions: 64, MaxLease: time.Minute, LockTimeout: 5 * time.Second}, retentionVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetPool(ctx, "gpu", config.RoleTranscode, 64); err != nil {
		t.Fatal(err)
	}
	for _, node := range []string{"w1", "w2"} {
		if err = s.SetTarget(ctx, placement.Target{NodeID: node, Boot: "b"}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"gpu"}}); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func reserveFor(t *testing.T, s *placement.Service, session string) placement.Record {
	t.Helper()
	rec, err := s.Reserve(context.Background(), placement.Request{Session: session, Operation: "op-" + session, Controller: placement.Controller{ID: "c", Boot: "cb"}, Target: placement.Target{NodeID: "w1", Boot: "b"}, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: "gpu", Units: 1}}, Lease: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// seedSession writes one row for (session, generation) into every
// session-keyed clusterplayback table and the placement history receipts.
func seedSession(t *testing.T, db *sql.DB, cluster, session string, generation int64) {
	t.Helper()
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO clusterplayback_admissions(cluster,session,generation,controller,controller_boot,admission) VALUES(?,?,?,'c','cb','{}')`, []any{cluster, session, generation}},
		{`INSERT INTO clusterplayback_operation_gates(cluster,session,generation,gate) VALUES(?,?,?,'{}')`, []any{cluster, session, generation}},
		{`INSERT INTO clusterplayback_recovery_decisions(cluster,session,generation,decision) VALUES(?,?,?,'{}')`, []any{cluster, session, generation}},
		{`INSERT INTO clusterplayback_source_recovery(session,generation,request) VALUES(?,?,'{}')`, []any{session, generation}},
		{`INSERT INTO clusterplayback_source_moves(session,generation,source_generation,route,operation) VALUES(?,?,0,'{}','op')`, []any{session, generation}},
		{`INSERT INTO clusterplayback_plex_bindings(session,generation,binding) VALUES(?,?,'{}')`, []any{session, generation}},
		{`INSERT INTO clusterplayback_neutral_bindings(session,generation,protocol,viewer,device,expires,binding) VALUES(?,?,'jellyfin',1,'d',?,'x')`, []any{session, generation, time.Now().Add(time.Hour).UnixMilli()}},
		{`INSERT INTO clusterplayback_neutral_stops(session) VALUES(?)`, []any{session}},
		{`INSERT INTO clusterplayback_coordinators(session,generation,node,boot,epoch,deadline) VALUES(?,?,'c','cb',1,0)`, []any{session, generation}},
		{`INSERT INTO clusterplayback_routes(session,generation,route,touched_at) VALUES(?,?,'{}',0)`, []any{session, generation}},
		{`INSERT INTO placement_history_receipts(cluster,session,generation,controller,controller_boot,node,boot,receipt) VALUES(?,?,?,'c','cb','w1','b','{}')`, []any{cluster, session, generation}},
	} {
		if _, err := db.Exec(q.sql, q.args...); err != nil {
			t.Fatalf("%s: %v", q.sql, err)
		}
	}
}

func rowsFor(t *testing.T, db *sql.DB, session string) int {
	t.Helper()
	total := 0
	for _, table := range append(sessionTableNames(), "placement_history_receipts", "placement_sessions") {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE session=?`, session).Scan(&n); err != nil {
			t.Fatal(err)
		}
		total += n
	}
	return total
}

func sessionTableNames() []string {
	var out []string
	for _, t := range sessionTables {
		out = append(out, t.name)
	}
	return out
}

// Pruning never deletes what a live session, a displaced failover generation,
// a recently retired session or an active unreserved route needs; a terminal
// session leaves every table at once.
func TestPruneRetainedRemovesOnlyTerminalSessions(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	ledger := retentionLedger(t, db)
	const cluster = "cluster-a"
	r := &Runtime{o: Options{DB: db, Placement: ledger, Self: nodewire.Identity{Cluster: cluster, NodeID: "c", Boot: "cb"}}}

	done := reserveFor(t, ledger, "done")
	live := reserveFor(t, ledger, "live")
	failed := reserveFor(t, ledger, "failover")
	moved, err := ledger.Supersede(ctx, failed.Fence, placement.Request{Session: "failover", Operation: "recover", ExpectedGeneration: failed.Generation, Controller: failed.Controller, Target: placement.Target{NodeID: "w2", Boot: "b"}, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: "gpu", Units: 1}}, Lease: 30 * time.Second}, func(context.Context, *sql.Tx, placement.Record) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []placement.Fence{done.Fence, moved.Fence} {
		if err = ledger.Retire(ctx, f, "proof"); err != nil {
			t.Fatal(err)
		}
	}
	for _, rec := range []placement.Record{done, live, moved} {
		seedSession(t, db, cluster, rec.Session, rec.Generation)
	}
	// "done", "live" and "failover" are all a day old; "recent" retired now.
	if _, err = db.Exec(`UPDATE placement_sessions SET deadline=deadline-?,retired_at=CASE WHEN retired_at>0 THEN retired_at-? ELSE 0 END`, (25 * time.Hour).Milliseconds(), (25 * time.Hour).Milliseconds()); err != nil {
		t.Fatal(err)
	}
	recent := reserveFor(t, ledger, "recent")
	if err = ledger.Retire(ctx, recent.Fence, "proof"); err != nil {
		t.Fatal(err)
	}
	seedSession(t, db, cluster, "recent", recent.Generation)

	// Unreserved routes: stale, stale-but-active and fresh.
	old := time.Now().Add(-2 * time.Hour).UnixMilli()
	for _, q := range []struct {
		session string
		touched int64
	}{{"orphan", old}, {"playing", old}, {"fresh", time.Now().UnixMilli()}} {
		if _, err = db.Exec(`INSERT INTO clusterplayback_routes(session,generation,route,touched_at) VALUES(?,0,'{}',?)`, q.session, q.touched); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO plex_sessions(id,node_id,user_id,session_key,data,updated_at,active) VALUES('playing','c',1,1,'{}',0,1)`); err != nil {
		t.Fatal(err)
	}
	for i, created := range []int64{old, time.Now().UnixMilli()} {
		if _, err = db.Exec(`INSERT INTO clusterplayback_preparation_receipts(cluster,source,source_boot,controller,controller_boot,operation_hash,receipt,created_at) VALUES(?,'s','sb','c','cb',?,'{}',?)`, cluster, string(rune('a'+i)), created); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(`INSERT INTO placement_history_namespace(id,cluster) VALUES(1,?)`, cluster); err != nil {
		t.Fatal(err)
	}

	report, err := r.PruneRetained(ctx, RetentionOptions{Batch: 1})
	if err != nil || report.Sessions != 1 || report.Routes != 1 || report.Receipts != 1 || report.More {
		t.Fatalf("report %+v %v", report, err)
	}
	if n := rowsFor(t, db, "done"); n != 0 {
		t.Fatalf("terminal session kept %d rows", n)
	}
	// Live: one placement row plus every seeded row. Failover: its displaced
	// generation is still charged. Recent: retired inside the window.
	for session, want := range map[string]int{"live": 12, "failover": 12, "recent": 12} {
		if n := rowsFor(t, db, session); n != want {
			t.Fatalf("%s kept %d rows, want %d", session, n, want)
		}
	}
	var routes, receipts int
	if err = db.QueryRow(`SELECT COUNT(*) FROM clusterplayback_routes WHERE session IN ('orphan','playing','fresh')`).Scan(&routes); err != nil || routes != 2 {
		t.Fatalf("unreserved routes %d %v", routes, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM clusterplayback_preparation_receipts`).Scan(&receipts); err != nil || receipts != 1 {
		t.Fatalf("receipts %d %v", receipts, err)
	}

	// Resolving the displaced generation makes the failover session terminal.
	if err = ledger.RetireDisplaced(ctx, failed.Fence, "proof"); err != nil {
		t.Fatal(err)
	}
	if report, err = r.PruneRetained(ctx, RetentionOptions{}); err != nil || report.Sessions != 1 || rowsFor(t, db, "failover") != 0 {
		t.Fatalf("failover after resolution %+v %v", report, err)
	}
	// A reused public ID is admitted above every compacted generation.
	if again := reserveFor(t, ledger, "done"); again.Generation <= moved.Generation {
		t.Fatalf("compacted generation re-issued: %d <= %d", again.Generation, moved.Generation)
	}
}
