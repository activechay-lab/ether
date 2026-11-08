package store

import (
	"context"
	"database/sql"
	"fmt"
)

// journalSizeLimit bounds retained WAL after a full checkpoint and subsequent
// writer reuse. It is not a peak-size bound: pinned readers and a large active
// transaction can exceed it. Bulk writers separately observe WaitWALBudget.
const journalSizeLimit = 64 << 20

// Maintain is the periodic (daily) upkeep for a long-running database: it
// returns free pages beyond a small reuse slack to the filesystem in short
// incremental_vacuum steps (ReleaseFreePages), PASSIVE-checkpoints the WAL, then refreshes
// query-planner statistics. It never runs a full VACUUM, which rewrites the
// whole file under the write lock; legacy conversion requires a separately coordinated generation transition.
//
// On PostgreSQL the server manages its own WAL and autovacuum; Maintain only
// refreshes planner statistics with ANALYZE.
func Maintain(ctx context.Context, db *sql.DB) error {
	if DialectOf(db) == Postgres {
		if _, err := db.ExecContext(ctx, `ANALYZE`); err != nil {
			return fmt.Errorf("analyze: %w", err)
		}
		return nil
	}
	if _, err := ReleaseFreePages(ctx, db); err != nil {
		return err
	}
	// PASSIVE never takes the strong checkpoint writer lock. Idle observations
	// cannot safely authorize TRUNCATE on a live pool.
	if err := Checkpoint(ctx, db, false); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `PRAGMA optimize`); err != nil {
		return fmt.Errorf("optimize: %w", err)
	}
	res, err := QuickCheck(ctx, db)
	if err != nil {
		return fmt.Errorf("integrity check: %w", err)
	}
	if !res.OK {
		return &CorruptionError{Integrity: res}
	}
	return nil
}
