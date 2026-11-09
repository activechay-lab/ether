package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/schedule"
)

// walCheckpointInterval is how often a background PASSIVE checkpoint copies
// WAL frames into the database. Short enough that one checkpoint stays a
// small fsync, long enough that an idle server is not rewriting the file
// continuously.
const walCheckpointInterval = time.Second

// Checkpoint copies WAL frames back into the database using PASSIVE only.
// The truncate argument is retained for source compatibility, but no longer
// permits a strong checkpoint on a live pool. An idle InUse snapshot cannot
// fence the next writer, and TRUNCATE may copy/sync for longer than admission
// deadlines even after a successful PASSIVE. Physical reclamation requires the
// separate database-generation lifecycle, not a busy-timeout or idle hint.
// The existing journal_size_limit still bounds retained WAL upon full
// checkpoint plus subsequent writer reuse; it is not a peak-size/I/O bound.
// PostgreSQL owns its own checkpoints; no server CHECKPOINT is issued here.
func Checkpoint(ctx context.Context, db *sql.DB, _ bool) error {
	if db == nil || DialectOf(db) == Postgres {
		return nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var mode string
	if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil {
		return err
	}
	if !strings.EqualFold(mode, "wal") {
		return nil
	}
	_, _, _, err = walCheckpoint(ctx, conn, "PASSIVE")
	return err
}

func walCheckpoint(ctx context.Context, conn *sql.Conn, mode string) (busy, logFrames, checkpointed int, err error) {
	var query string
	switch mode {
	case "PASSIVE":
		query = `PRAGMA wal_checkpoint(PASSIVE)`
	case "TRUNCATE":
		// This canonical statement is writer-admitted by writes(). The gate
		// stays held until QueryRow.Scan closes its rows, including busy=1.
		// Admission does not make SQLite checkpoint I/O preemptible.
		query = `PRAGMA wal_checkpoint(TRUNCATE)`
	default:
		return 0, 0, 0, fmt.Errorf("wal checkpoint %s: unknown mode", mode)
	}
	err = conn.QueryRowContext(ctx, query).Scan(&busy, &logFrames, &checkpointed)
	if err != nil {
		err = fmt.Errorf("wal checkpoint %s: %w", mode, err)
	}
	return busy, logFrames, checkpointed, err
}

// RunCheckpointer PASSIVE-checkpoints until ctx ends. The former truncate
// callback is retained for compatibility but is not consulted: sampled idle
// state cannot authorize exclusive maintenance. onErr may be nil.
func RunCheckpointer(ctx context.Context, db *sql.DB, _ func() bool, onErr func(error)) {
	if db == nil || DialectOf(db) == Postgres {
		return
	}
	for schedule.Wait(ctx, time.Now, walCheckpointInterval) {
		err := Checkpoint(ctx, db, false)
		if err != nil && ctx.Err() == nil && onErr != nil {
			onErr(err)
		}
	}
}

// walOverBudget reports whether the WAL holds more than limit bytes of
// frames a PASSIVE checkpoint could not copy (a reader holds them back).
// journal_size_limit only bounds recycled WAL, not pinned frames.
func walOverBudget(ctx context.Context, db *sql.DB, limit int64) (bool, int, error) {
	var seq int
	var name, path string
	if err := db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		return false, 0, err
	}
	if path == "" {
		return false, 0, nil
	}
	st, err := os.Stat(path + "-wal")
	if os.IsNotExist(err) {
		return false, 0, nil
	}
	if err != nil {
		return false, 0, err
	}
	if st.Size() <= limit {
		return false, 0, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, 0, err
	}
	defer conn.Close()
	_, frames, copied, err := walCheckpoint(ctx, conn, "PASSIVE")
	if err != nil {
		return false, 0, err
	}
	var pageSize int64
	if err := conn.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		return false, 0, err
	}
	pending := frames - copied
	return int64(pending)*(pageSize+24) > limit, pending, nil
}

// TruncateWAL runs a TRUNCATE checkpoint when the WAL file is larger than
// journalSizeLimit. Readers that never pause keep SQLite from restarting
// the WAL, so its file grows by everything written; a bulk job calls this
// between batches to bound it. It waits at most wait for readers on older
// snapshots (writers wait behind it meanwhile) and reports whether the WAL
// was truncated. PostgreSQL returns at once.
func TruncateWAL(ctx context.Context, db *sql.DB, wait time.Duration) (bool, error) {
	if db == nil || DialectOf(db) == Postgres {
		return false, nil
	}
	var seq int
	var name, path string
	if err := db.QueryRowContext(ctx, `PRAGMA database_list`).Scan(&seq, &name, &path); err != nil || path == "" {
		return false, err
	}
	st, err := os.Stat(path + "-wal")
	if err != nil || st.Size() <= journalSizeLimit {
		return false, nil
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA busy_timeout=%d`, wait.Milliseconds())); err != nil {
		return false, err
	}
	defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA busy_timeout=10000`)
	busy, _, _, err := walCheckpoint(ctx, conn, "TRUNCATE")
	if err != nil && strings.Contains(err.Error(), "SQLITE_BUSY") {
		return false, nil
	}
	return err == nil && busy == 0, err
}
