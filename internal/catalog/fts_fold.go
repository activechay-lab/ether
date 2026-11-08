package catalog

import (
	"context"
	"database/sql"
	"strings"

	"github.com/braedonsaunders/ether/internal/store"
)

// Bulk title writes fold their new rows into the title search index in one
// statement per transaction instead of one FTS5 insert per row from the
// trigger: on a 54k catalogue the per-row FTS upkeep was about half of a new
// title's insert cost, and FTS5 indexes a batch several times cheaper than
// the same rows one call at a time.
//
// catalog_fts_bulk holds a row only inside a bulk transaction, which the
// FTS insert trigger checks: other writers never see it (it is gone before
// commit), so every other insert path keeps the trigger. Search reads the
// folded rows as soon as the transaction commits. Updates and deletes keep
// their triggers; only rows a bulk transaction inserts are folded.
func init() {
	store.RegisterAfter("catalog_bulk", "catalog")
	store.Register("catalog_bulk", 1, "SELECT 1;")
	store.RegisterSQLiteApply("catalog_bulk", 1, func(ctx context.Context, tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE name='catalog_titles_fts'`).Scan(&n); err != nil || n == 0 {
			return err
		}
		_, err := tx.ExecContext(ctx, `CREATE TABLE catalog_fts_bulk(id INTEGER PRIMARY KEY CHECK(id=1));
DROP TRIGGER IF EXISTS catalog_titles_fts_insert;
CREATE TRIGGER catalog_titles_fts_insert AFTER INSERT ON catalog_titles WHEN NOT EXISTS (SELECT 1 FROM catalog_fts_bulk) BEGIN
 INSERT INTO catalog_titles_fts(rowid, title, original_title) VALUES (new.key, new.title, new.original_title);
END;`)
		return err
	})
	// PostgreSQL's title search is a pg_trgm index on catalog_titles itself.
	store.RegisterDialect("catalog_bulk", 1, store.Postgres, "SELECT 1;")
}

// ftsFoldMin is the fewest new rows a transaction folds; below it the
// trigger is as cheap.
const ftsFoldMin = 2

// ftsFoldAvailable reports whether the FTS insert trigger honours the bulk
// marker (a database migrated by this binary; never assume it).
func ftsFoldAvailable(ctx context.Context, db *sql.DB) bool {
	if store.DialectOf(db) == store.Postgres {
		return false
	}
	var def string
	if err := db.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='trigger' AND name='catalog_titles_fts_insert'`).Scan(&def); err != nil {
		return false
	}
	return strings.Contains(def, "catalog_fts_bulk")
}

func beginFTSFold(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO catalog_fts_bulk(id) VALUES(1)`)
	return err
}

// endFTSFold indexes the rows the transaction inserted and lifts the marker.
func endFTSFold(ctx context.Context, tx *sql.Tx, keys []int64) error {
	for start := 0; start < len(keys); start += 500 {
		part := keys[start:min(start+500, len(keys))]
		args := make([]any, len(part))
		for i, k := range part {
			args[i] = k
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_titles_fts(rowid, title, original_title) SELECT key, title, original_title FROM catalog_titles WHERE key IN (`+placeholders(len(part))+`)`, args...); err != nil {
			return err
		}
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM catalog_fts_bulk`)
	return err
}
