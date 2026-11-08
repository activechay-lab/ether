package placement

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Retention keeps the ledger proportional to live work plus a bounded window
// of recent history, instead of every session the cluster has ever admitted.
//
// A terminal tombstone (retired, no displaced generation still charged, both
// its authority deadline and its retirement older than the retention age) is
// compacted into one cluster-wide generation floor. Every generation the
// ledger allocates for a session it has no row for starts above that floor,
// so a fence, route, gate or receipt that survives anywhere for a compacted
// session is strictly older than any generation that can be issued again.
// The floor, not the per-session tombstone, is what keeps generation fencing
// monotonic after compaction.
//
// What this does NOT relax: a live or displaced generation is never pruned,
// claims are never released here, and no record is pruned while its lease
// could still be renewed. Delayed Reserve replay of an expected generation
// above zero still fails closed (ErrFenced) after compaction. A replay of a
// generation-zero operation is only reachable from the controller boot that
// never learnt its outcome; that controller cannot have retired the row
// (retirement needs the route it publishes after learning the outcome), so a
// row it could replay is never terminal and never pruned.
func init() {
	store.Register("placement", 5, `
ALTER TABLE placement_sessions ADD COLUMN retired_at BIGINT NOT NULL DEFAULT 0;
CREATE INDEX placement_sessions_live ON placement_sessions(session) WHERE retired=0;
CREATE INDEX placement_sessions_tombstones ON placement_sessions(deadline) WHERE retired=1;
CREATE TABLE placement_ledger (id INTEGER PRIMARY KEY CHECK(id=1), generation_floor BIGINT NOT NULL, retained BIGINT NOT NULL, retained_at BIGINT NOT NULL);
INSERT INTO placement_ledger(id,generation_floor,retained,retained_at) SELECT 1,0,COUNT(*),0 FROM placement_sessions WHERE retired=1;
`)
}

// Migration 5 builds two partial indexes with plain CREATE INDEX inside the
// migration transaction (the store runs migrations transactionally, so
// CONCURRENTLY is unavailable). On PostgreSQL that holds a SHARE lock on
// placement_sessions — reads continue, Reserve/Renew/Retire wait — for the
// build: about a second per million rows. The ADD COLUMN with a constant
// default is metadata-only on PostgreSQL 11+.

// MinRetention is the shortest tombstone age PruneTombstones accepts. It is
// far above the maximum lease, the coordinator lease and every in-process
// reconciliation interval, so no process can still be acting on a pruned row.
const MinRetention = time.Hour

// MaxPruneBatch bounds one prune transaction (and so how long it holds the
// ledger lock against Reserve/Renew/Lookup).
const MaxPruneBatch = 512

// Pruned identifies one compacted terminal session and its final generation.
type Pruned struct {
	Session    string
	Generation int64
}

// PruneOptions bounds one compaction transaction.
type PruneOptions struct {
	// Age is how long ago both the authority deadline and the retirement must
	// be, measured on the database clock. At least MinRetention and MaxLease.
	Age time.Duration
	// Limit is the number of tombstones per transaction, 1..MaxPruneBatch.
	Limit int
	// Dependents deletes caller-owned rows for exactly these sessions inside
	// the same transaction, before the tombstones go. An error rolls back the
	// whole batch. Nil prunes only placement-owned rows.
	Dependents func(context.Context, *sql.Tx, []Pruned) error
}

// PruneTombstones compacts at most Limit terminal tombstones in one bounded
// transaction under the ledger lock and reports how many it removed. Call it
// from one node (the cluster leader) off request paths, repeating while it
// returns Limit. It is idempotent and safe to run concurrently: each batch
// re-selects under the lock.
func (s *Service) PruneTombstones(ctx context.Context, o PruneOptions) (int, error) {
	if o.Limit < 1 || o.Limit > MaxPruneBatch || o.Age < MinRetention || o.Age < s.options.MaxLease {
		return 0, ErrInvalid
	}
	var pruned []Pruned
	err := s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		pruned = pruned[:0]
		now, err := s.now(ctx, tx)
		if err != nil {
			return err
		}
		cutoff := now - o.Age.Milliseconds()
		// The tombstone index orders by deadline; retired_at and the displaced
		// probe filter that bounded range. Legacy rows retired before
		// retired_at existed carry 0 and age by deadline alone.
		rows, err := tx.QueryContext(ctx, `SELECT session,generation FROM placement_sessions p WHERE p.retired=1 AND p.deadline<? AND p.retired_at<? AND NOT EXISTS (SELECT 1 FROM placement_displaced d WHERE d.session=p.session) ORDER BY p.deadline LIMIT ?`, cutoff, cutoff, o.Limit)
		if err != nil {
			return err
		}
		var floor int64
		for rows.Next() {
			var p Pruned
			if err = rows.Scan(&p.Session, &p.Generation); err != nil {
				rows.Close()
				return err
			}
			floor = max(floor, p.Generation)
			pruned = append(pruned, p)
		}
		if err = rows.Close(); err != nil {
			return err
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(pruned) == 0 {
			return nil
		}
		if o.Dependents != nil {
			if err = o.Dependents(ctx, tx, append([]Pruned(nil), pruned...)); err != nil {
				return err
			}
		}
		in, args := sessionList(pruned)
		// Receipts are keyed (cluster, session, ...): bind the namespace's one
		// cluster as a value so the planner always uses that primary-key prefix.
		var cluster string
		switch err = tx.QueryRowContext(ctx, `SELECT cluster FROM placement_history_namespace WHERE id=1`).Scan(&cluster); err {
		case nil:
			if _, err = tx.ExecContext(ctx, `DELETE FROM placement_history_receipts WHERE cluster=? AND session IN (`+in+`)`, append([]any{cluster}, args...)...); err != nil {
				return err
			}
		case sql.ErrNoRows: // no receipt was ever published
		default:
			return err
		}
		result, err := tx.ExecContext(ctx, `DELETE FROM placement_sessions WHERE retired=1 AND session IN (`+in+`)`, args...)
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if int(n) != len(pruned) {
			return ErrConflict // the lock makes this impossible; never commit a partial floor
		}
		_, err = tx.ExecContext(ctx, `UPDATE placement_ledger SET generation_floor=CASE WHEN generation_floor<? THEN ? ELSE generation_floor END, retained=CASE WHEN retained>=? THEN retained-? ELSE 0 END WHERE id=1`, floor, floor, n, n)
		return err
	})
	if err != nil {
		return 0, err
	}
	return len(pruned), nil
}

// ReconcileRetained recounts retained tombstones under the ledger lock,
// correcting any drift in the maintained counter (for example from a node
// that predates it during a rolling upgrade). The count covers retained
// tombstones only, so it is bounded by the retention window, not by history.
// The counter is display state only; admission never reads it.
func (s *Service) ReconcileRetained(ctx context.Context) error {
	return s.transaction(ctx, func(ctx context.Context, tx *sql.Tx) error {
		now, err := s.now(ctx, tx)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `UPDATE placement_ledger SET retained=(SELECT COUNT(*) FROM placement_sessions WHERE retired=1),retained_at=? WHERE id=1`, now)
		return err
	})
}

// RetainedReconciledAt reports when ReconcileRetained last ran (zero if never).
func (s *Service) RetainedReconciledAt(ctx context.Context) (time.Time, error) {
	var at int64
	err := s.db.QueryRowContext(ctx, `SELECT retained_at FROM placement_ledger WHERE id=1`).Scan(&at)
	if err == sql.ErrNoRows || at == 0 {
		return time.Time{}, nil
	}
	return time.UnixMilli(at), err
}

// liveSessions counts unretired sessions through the partial live index. Every
// live session holds claims against a finite pool, so this is bounded by
// configured cluster capacity, never by history or by retained tombstones.
func liveSessions(ctx context.Context, q store.Querier) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM placement_sessions WHERE retired=0`).Scan(&n)
	return n, err
}

// generationFloor is the highest generation ever compacted out of the ledger.
func generationFloor(ctx context.Context, q store.Querier) (int64, error) {
	var floor int64
	err := q.QueryRowContext(ctx, `SELECT generation_floor FROM placement_ledger WHERE id=1`).Scan(&floor)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return floor, err
}

// adjustRetained maintains the display counter in the caller's transaction.
func adjustRetained(ctx context.Context, tx *sql.Tx, delta int) error {
	_, err := tx.ExecContext(ctx, `UPDATE placement_ledger SET retained=CASE WHEN retained+?<0 THEN 0 ELSE retained+? END WHERE id=1`, delta, delta)
	return err
}

func sessionList(pruned []Pruned) (string, []any) {
	args := make([]any, len(pruned))
	for i, p := range pruned {
		args[i] = p.Session
	}
	return strings.TrimSuffix(strings.Repeat("?,", len(pruned)), ","), args
}
