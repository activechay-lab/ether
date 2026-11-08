package store

import (
	"context"
	"database/sql"
	"time"
)

// PruneBatch is the row limit of one retention delete. One batch is a short
// write transaction, so a request writer waits at most one batch behind a
// prune instead of behind the whole backlog.
const PruneBatch = 1000

// pruneYield is the pause between batches, so writers queued on the write
// gate run before the next batch takes it.
const pruneYield = 5 * time.Millisecond

// PruneBatched runs a retention delete in batches until a batch removes
// fewer than PruneBatch rows or ctx ends, and reports the rows removed.
// del's last placeholder is the batch limit; the statement must pick its
// rows through an index, the same way on SQLite and PostgreSQL:
//
//	DELETE FROM t WHERE id IN (SELECT id FROM t WHERE created_at<? ORDER BY created_at LIMIT ?)
//
// Multi-column keys use a row value: (a,b) IN (SELECT a,b ...).
func PruneBatched(ctx context.Context, db *sql.DB, del string, args ...any) (int64, error) {
	args = append(args[:len(args):len(args)], PruneBatch)
	var total int64
	for {
		res, err := db.ExecContext(ctx, del, args...)
		if err != nil {
			return total, err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return total, err
		}
		total += n
		if n < PruneBatch {
			return total, nil
		}
		select {
		case <-ctx.Done():
			return total, ctx.Err()
		case <-time.After(pruneYield):
		}
	}
}
