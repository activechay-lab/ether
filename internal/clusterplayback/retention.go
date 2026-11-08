package clusterplayback

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

// Retention. Every clusterplayback table except the bounded recovery-event
// log is keyed by a playback session, and every row except an unreserved
// (generation zero) route belongs to a placement generation. Rows therefore
// leave with their session's placement tombstone: PruneRetained hands
// placement.PruneTombstones a Dependents hook that deletes them in the same
// ledger transaction, so a session is either fully retained or fully gone and
// the ledger's generation floor fences anything that referred to it.
//
// Two row kinds have no placement generation and age on their own:
//   - generation-zero routes published by BindClientSession and
//     BindLocalClientSession before (or instead of) any encoder reservation;
//   - preparation receipts, closure caches keyed by source/controller boot.
//     A controller re-reads its own receipt within a Tick of publication; a
//     missing receipt fails closed and is re-derived from the live source
//     boot (or from that boot's authenticated replacement), never inferred.
func init() {
	store.Register("clusterplayback", 9, `
ALTER TABLE clusterplayback_routes ADD COLUMN touched_at BIGINT NOT NULL DEFAULT 0;
CREATE INDEX clusterplayback_routes_unreserved ON clusterplayback_routes(touched_at) WHERE generation=0;
ALTER TABLE clusterplayback_preparation_receipts ADD COLUMN created_at BIGINT NOT NULL DEFAULT 0;
CREATE INDEX clusterplayback_preparation_receipts_age ON clusterplayback_preparation_receipts(created_at);
`)
}

// Migration 9 builds its indexes inside the migration transaction (no
// CONCURRENTLY). Both tables were capped at HistoryCapacity rows per cluster
// or source boot before this change, so the SHARE lock lasts milliseconds.

const (
	// DefaultSessionRetention keeps a terminal session's rows for a day after
	// its authority and retirement both ended: far beyond the 10-minute lease,
	// the coordinator lease and every reconciliation interval.
	DefaultSessionRetention = 24 * time.Hour
	// DefaultReceiptRetention keeps preparation receipts and unreserved routes
	// for an hour past their last write. Remote preparations idle out after
	// minutes (paused sessions after 30), and a route for an active session is
	// never pruned while plex_sessions still lists it as active.
	DefaultReceiptRetention = time.Hour
	retentionBatch          = 256
	retainedReconcile       = time.Hour
)

// RetentionOptions bounds one maintenance pass. Zero values take defaults.
type RetentionOptions struct {
	SessionAge, ReceiptAge time.Duration
	Batch                  int
}

// RetentionReport counts rows removed by one pass.
type RetentionReport struct {
	Sessions, Routes, Receipts int
	// More reports that a category stopped at the pass's context deadline
	// with work remaining; the next pass continues.
	More bool
}

// sessionTables are deleted by session, all through a primary-key prefix.
// Clustered tables take the configured cluster as their leading key column.
var sessionTables = []struct {
	name      string
	clustered bool
}{
	{"clusterplayback_admissions", true},
	{"clusterplayback_operation_gates", true},
	{"clusterplayback_recovery_decisions", true},
	{"clusterplayback_source_recovery", false},
	{"clusterplayback_source_moves", false},
	{"clusterplayback_plex_bindings", false},
	{"clusterplayback_neutral_bindings", false},
	{"clusterplayback_neutral_stops", false},
	{"clusterplayback_coordinators", false},
	{"clusterplayback_routes", false},
}

// PruneRetained runs one bounded retention pass. Run it on exactly one node
// (the cluster leader), off request paths; every statement is a batched,
// index-driven delete and each batch is its own short transaction. It returns
// when every category is caught up or ctx expires.
func (r *Runtime) PruneRetained(ctx context.Context, o RetentionOptions) (RetentionReport, error) {
	var report RetentionReport
	if o.SessionAge == 0 {
		o.SessionAge = DefaultSessionRetention
	}
	if o.ReceiptAge == 0 {
		o.ReceiptAge = DefaultReceiptRetention
	}
	if o.Batch == 0 {
		o.Batch = retentionBatch
	}
	if o.ReceiptAge < 10*time.Minute || o.Batch < 1 || o.Batch > placement.MaxPruneBatch {
		return report, ErrDenied
	}
	run := func(count *int, batch func() (int, error)) error {
		for {
			if ctx.Err() != nil {
				report.More = true
				return nil
			}
			n, err := batch()
			*count += n
			if err != nil {
				if ctx.Err() != nil {
					report.More = true
					return nil
				}
				return err
			}
			if n < o.Batch {
				return nil
			}
		}
	}
	err := run(&report.Sessions, func() (int, error) {
		return r.o.Placement.PruneTombstones(ctx, placement.PruneOptions{Age: o.SessionAge, Limit: o.Batch, Dependents: r.deleteSessionRows})
	})
	if err == nil {
		err = run(&report.Routes, func() (int, error) { return r.pruneUnreservedRoutes(ctx, time.Now().Add(-o.ReceiptAge), o.Batch) })
	}
	if err == nil {
		err = run(&report.Receipts, func() (int, error) { return r.prunePreparationReceipts(ctx, time.Now().Add(-o.ReceiptAge), o.Batch) })
	}
	if err == nil && ctx.Err() == nil {
		// Correct the ledger's display counter at most hourly (one count of
		// retained tombstones, bounded by the retention window).
		at, e := r.o.Placement.RetainedReconciledAt(ctx)
		if e == nil && time.Since(at) > retainedReconcile {
			e = r.o.Placement.ReconcileRetained(ctx)
		}
		if ctx.Err() == nil {
			err = e
		}
	}
	return report, err
}

// deleteSessionRows runs inside the placement ledger transaction (lock order
// placement:all then clusterplayback:admissions, as in encoder recovery).
func (r *Runtime) deleteSessionRows(ctx context.Context, tx *sql.Tx, pruned []placement.Pruned) error {
	if err := r.lockAdmissionHistory(ctx, tx); err != nil {
		return err
	}
	in := strings.TrimSuffix(strings.Repeat("?,", len(pruned)), ",")
	sessions := make([]any, len(pruned))
	for i, p := range pruned {
		sessions[i] = p.Session
	}
	for _, t := range sessionTables {
		args := sessions
		where := `session IN (` + in + `)`
		if t.clustered {
			args = append([]any{r.o.Self.Cluster}, sessions...)
			where = `cluster=? AND ` + where
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+t.name+` WHERE `+where, args...); err != nil {
			return err
		}
	}
	return nil
}

// pruneUnreservedRoutes removes generation-zero routes that never gained a
// placement row, are older than cutoff and whose public session is no longer
// active. A reserved session's route leaves only with its tombstone.
func (r *Runtime) pruneUnreservedRoutes(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	result, err := r.o.DB.ExecContext(ctx, `DELETE FROM clusterplayback_routes WHERE generation=0 AND session IN (SELECT c.session FROM clusterplayback_routes c WHERE c.generation=0 AND c.touched_at<? AND NOT EXISTS (SELECT 1 FROM placement_sessions p WHERE p.session=c.session) AND NOT EXISTS (SELECT 1 FROM plex_sessions s WHERE s.id=c.session AND s.active=1) ORDER BY c.touched_at LIMIT ?)`, cutoff.UnixMilli(), limit)
	return affected(result, err)
}

// prunePreparationReceipts removes the oldest receipts older than cutoff as
// one range of the created_at index: everything up to the limit-th oldest
// timestamp (rows sharing that millisecond go together, so a batch can exceed
// limit by its ties).
func (r *Runtime) prunePreparationReceipts(ctx context.Context, cutoff time.Time, limit int) (int, error) {
	c := cutoff.UnixMilli()
	result, err := r.o.DB.ExecContext(ctx, `DELETE FROM clusterplayback_preparation_receipts WHERE created_at<? AND created_at<=COALESCE((SELECT created_at FROM clusterplayback_preparation_receipts WHERE created_at<? ORDER BY created_at LIMIT 1 OFFSET ?),?)`, c, c, limit-1, c)
	return affected(result, err)
}

func affected(result sql.Result, err error) (int, error) {
	if err != nil {
		return 0, err
	}
	n, err := result.RowsAffected()
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return int(n), err
}
