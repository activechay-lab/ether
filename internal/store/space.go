package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/braedonsaunders/ether/internal/datafs"
)

// SQLite space policy.
//
// Deleted rows leave free pages that SQLite reuses for later writes, but the
// file never shrinks on its own (auto_vacuum NONE). Retention prunes and
// moved-out data can free hundreds of megabytes at once, so every database
// runs with auto_vacuum=INCREMENTAL and the daily Maintain returns free
// pages beyond a small reuse slack with PRAGMA incremental_vacuum, in short
// steps. New databases get INCREMENTAL from the DSN (it must be set before
// the first table exists, and it also makes VACUUM INTO copies, so backups
// and pre-migrate snapshots, incremental). An existing NONE database is
// converted once by a full VACUUM at an upgrade start, after its pre-migrate
// snapshot.
//
// Measured with the modernc driver on a laptop SSD (2026-11-07):
//   - converting VACUUM: 1.05 GB in 9.5 s + 1.5 s checkpoint, 2.9 GB in 99 s + 34 s.
//     The WAL grows to the database size and the temp copy is a second
//     database size, so conversion needs about twice the database free.
//     With temp_store=MEMORY the temp copy is in RAM (2.66 GB RSS for the
//     1.05 GB database), so the conversion switches the connection to FILE.
//   - incremental_vacuum: 512-page steps hold the write lock ≤19 ms on 2.9 GB;
//     167 MB returned in 1 s; 335 MB of freed blob pages in 2.8 s at 2048 pages.
//   - pointer-map overhead of INCREMENTAL: 307 pages (0.12%) on 1.05 GB.
//
// PostgreSQL reuses dead tuples through autovacuum. Retention deletes there
// run in PruneBatch-row transactions so autovacuum keeps up; Ether never runs
// VACUUM FULL, which locks tables.
const (
	autoVacuumIncremental = 2
	// vacuumStepPages is one incremental_vacuum write transaction.
	vacuumStepPages = 512
	// vacuumSlackPages free pages are kept for reuse instead of being
	// returned and allocated again (8 MB at the default 4 KB page).
	vacuumSlackPages = 2048
	// vacuumStepPause lets queued writers run between steps.
	vacuumStepPause = 5 * time.Millisecond
)

// ReleaseFreePages returns free pages beyond vacuumSlackPages to the
// filesystem in vacuumStepPages steps and reports how many it returned. A
// database still in auto_vacuum NONE (not converted yet) returns none.
// PostgreSQL does nothing.
func ReleaseFreePages(ctx context.Context, db *sql.DB) (int64, error) {
	if db == nil || DialectOf(db) == Postgres {
		return 0, nil
	}
	var mode int
	if err := db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode != autoVacuumIncremental {
		return 0, err
	}
	var released int64
	for {
		var free int64
		if err := db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free); err != nil {
			return released, err
		}
		if free <= vacuumSlackPages {
			return released, nil
		}
		step := min(free-vacuumSlackPages, vacuumStepPages)
		// incremental_vacuum returns no rows but must be stepped to run;
		// Exec steps it to completion in one write transaction.
		if _, err := db.ExecContext(ctx, fmt.Sprintf(`PRAGMA incremental_vacuum(%d)`, step)); err != nil {
			return released, fmt.Errorf("incremental vacuum: %w", err)
		}
		released += step
		select {
		case <-ctx.Done():
			return released, ctx.Err()
		case <-time.After(vacuumStepPause):
		}
	}
}

// convertAutoVacuum switches an existing database to auto_vacuum=INCREMENTAL
// with one full VACUUM, when the volume has room for it. It runs at an
// upgrade start, before the server serves, and never fails the open: a
// database that cannot convert keeps working and tries again next upgrade.
func convertAutoVacuum(ctx context.Context, db *sql.DB, path string, progress func(Progress)) {
	var mode int
	if err := db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&mode); err != nil || mode == autoVacuumIncremental {
		return
	}
	st, err := os.Stat(path)
	if err != nil {
		return
	}
	// The WAL takes a copy of the database and the temp file another.
	need := 2*st.Size() + 256<<20
	if _, free, err := datafs.Usage(filepath.Dir(path)); err != nil || free < need {
		slog.Info("store: not converting the database to incremental vacuum; not enough free disk", "path", path, "need", need, "free", free, "err", err)
		return
	}
	if _, free, err := datafs.Usage(os.TempDir()); err != nil || free < st.Size()+256<<20 {
		slog.Info("store: not converting the database to incremental vacuum; not enough free temp space", "dir", os.TempDir(), "free", free, "err", err)
		return
	}
	report(progress, Progress{Stage: StageCompact})
	start := time.Now()
	if err := vacuumIncremental(ctx, db); err != nil {
		slog.Warn("store: converting the database to incremental vacuum failed; it keeps working unconverted", "path", path, "err", err)
		return
	}
	slog.Info("store: database converted to incremental vacuum", "path", path, "took", time.Since(start).Round(time.Millisecond))
}

func vacuumIncremental(ctx context.Context, db *sql.DB) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	// VACUUM builds its copy in a temp database; on this connection that is
	// RAM (temp_store=MEMORY) unless switched to a file first.
	if _, err := conn.ExecContext(ctx, `PRAGMA temp_store=FILE`); err != nil {
		return err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA temp_store=MEMORY`)
	if _, err := conn.ExecContext(ctx, `PRAGMA auto_vacuum=INCREMENTAL`); err != nil {
		return err
	}
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		return err
	}
	// The VACUUM went through the WAL; copy it back and truncate it now
	// rather than leaving a database-sized WAL for the first checkpoint.
	_, _, _, err = walCheckpoint(ctx, conn, "TRUNCATE")
	return err
}
