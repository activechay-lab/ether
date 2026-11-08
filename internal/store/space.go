package store

import (
	"context"
	"database/sql"
	"errors"
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
// converted only when the owner runs Compact (the console's "Compact
// database" action), never automatically: that is a whole-file rewrite.
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
		if err := WaitWALBudget(ctx, db); err != nil {
			return released, err
		}
	}
}

// compactSecondsPerGB estimates a compaction on an idle machine: measured
// 11 s for 1.05 GB and 133 s for 2.9 GB (VACUUM plus checkpoint), rounded
// up. Writers wait for the VACUUM part, about three quarters of it.
const compactSecondsPerGB = 46

// CompactPlan is what compacting the database would take, for the owner
// to decide before starting it.
type CompactPlan struct {
	// Supported is false on PostgreSQL, which needs no compaction.
	Supported bool `json:"supported"`
	// Incremental means the database already returns free space daily;
	// compacting it again is not offered.
	Incremental bool `json:"incremental"`
	// DatabaseBytes is the file size; ReclaimableBytes its free pages now.
	DatabaseBytes    int64 `json:"databaseBytes"`
	ReclaimableBytes int64 `json:"reclaimableBytes"`
	// NeedBytes must be free on the database's volume (the WAL takes a
	// copy), TempNeedBytes in the temp folder (VACUUM's working copy).
	NeedBytes       int64 `json:"needBytes"`
	DiskFreeBytes   int64 `json:"diskFreeBytes"`
	TempNeedBytes   int64 `json:"tempNeedBytes"`
	TempFreeBytes   int64 `json:"tempFreeBytes"`
	EstimateSeconds int64 `json:"estimateSeconds"`
	// Ready is whether Compact would start; Blocked says why not:
	// "incremental", "disk" or "temp".
	Ready   bool   `json:"ready"`
	Blocked string `json:"blocked,omitempty"`
}

// ErrCompactBlocked is returned by Compact when its plan is not ready.
var ErrCompactBlocked = errors.New("store: database compaction cannot start")

// PlanCompact reports what Compact would cost on db.
func PlanCompact(ctx context.Context, db *sql.DB) (CompactPlan, error) {
	if db == nil || DialectOf(db) == Postgres {
		return CompactPlan{}, nil
	}
	p := CompactPlan{Supported: true}
	path, err := sqlitePath(ctx, db)
	if err != nil || path == "" {
		return p, err
	}
	var mode, pageSize, pages, free int64
	for q, v := range map[string]*int64{`PRAGMA auto_vacuum`: &mode, `PRAGMA page_size`: &pageSize, `PRAGMA page_count`: &pages, `PRAGMA freelist_count`: &free} {
		if err := db.QueryRowContext(ctx, q).Scan(v); err != nil {
			return p, err
		}
	}
	// The size SQLite holds, not the file's: right after a compaction the
	// file shrinks only once the WAL is checkpointed in full.
	size := pages * pageSize
	p.Incremental = mode == autoVacuumIncremental
	p.DatabaseBytes, p.ReclaimableBytes = size, free*pageSize
	p.NeedBytes = size + 256<<20
	p.TempNeedBytes = size + 256<<20
	p.EstimateSeconds = max(5, (size*compactSecondsPerGB+(1<<30)-1)>>30)
	if _, p.DiskFreeBytes, err = datafs.Usage(filepath.Dir(path)); err != nil {
		return p, err
	}
	if _, p.TempFreeBytes, err = datafs.Usage(os.TempDir()); err != nil {
		return p, err
	}
	switch {
	case p.Incremental:
		p.Blocked = "incremental"
	case p.DiskFreeBytes < p.NeedBytes:
		p.Blocked = "disk"
	case p.TempFreeBytes < p.TempNeedBytes:
		p.Blocked = "temp"
	default:
		p.Ready = true
	}
	return p, nil
}

// Compact rewrites the database once with VACUUM, switching it to
// auto_vacuum=INCREMENTAL so daily maintenance returns free space from then
// on. It runs only when the owner asks (never at startup) and only when
// PlanCompact is ready. Readers keep reading the last committed state
// throughout (WAL). Writers wait on SQLite's lock while the VACUUM runs
// (not its checkpoint), so a write that waits past busy_timeout (10 s)
// fails as contention: outboxed writes (watch progress, resume points,
// plays) are held and replayed, others report an error. Measured on the
// 1.05 GB lab copy with 4 readers and 2 writers on a heavily loaded Mac:
// reads p99 7.9 ms, max 120 ms, no errors; the VACUUM held writers 69 s
// (9.5 s on an idle machine).
func Compact(ctx context.Context, db *sql.DB) (CompactPlan, error) {
	p, err := PlanCompact(ctx, db)
	if err != nil {
		return p, err
	}
	if !p.Ready {
		return p, fmt.Errorf("%w: %s", ErrCompactBlocked, p.Blocked)
	}
	if err := vacuumIncremental(ctx, db); err != nil {
		return p, err
	}
	return PlanCompact(ctx, db)
}

func sqlitePath(ctx context.Context, db *sql.DB) (string, error) {
	var seq int
	var name, path string
	err := db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &path)
	return path, err
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
	start := time.Now()
	if _, err := conn.ExecContext(ctx, `VACUUM`); err != nil {
		return err
	}
	vacuumed := time.Now()
	// The VACUUM went through the WAL. Copy it back with a PASSIVE
	// checkpoint, which writers do not wait on (TRUNCATE holds them off
	// while it runs: 34 s for 1 GB); the background checkpointer truncates
	// the WAL at its next idle moment.
	_, _, _, err = walCheckpoint(ctx, conn, "PASSIVE")
	slog.Info("store: database compacted", "vacuum", vacuumed.Sub(start).Round(time.Millisecond), "checkpoint", time.Since(vacuumed).Round(time.Millisecond), "err", err)
	return err
}
