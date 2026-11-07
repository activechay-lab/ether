package app

import (
	"context"
	"database/sql"
	"time"

	"github.com/braedonsaunders/ether/internal/diagnostics"
	"github.com/braedonsaunders/ether/internal/store"
)

// collectDatabaseStorage reads allocation only, never row values or file paths.
// A bounded read transaction keeps the page counts internally consistent and
// cannot checkpoint, vacuum, or delete data while preparing a support bundle.
func collectDatabaseStorage(db *sql.DB, fact *diagnostics.StorageFact) {
	if db == nil {
		return
	}
	fact.DatabaseEngine = store.DialectOf(db).String()
	if store.DialectOf(db) != store.SQLite {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		fact.DatabaseAllocationStatus = "unavailable"
		return
	}
	defer tx.Rollback()
	var pageSize, pageCount, freePages int64
	for _, q := range []struct {
		sql string
		out *int64
	}{
		{"PRAGMA page_size", &pageSize}, {"PRAGMA page_count", &pageCount}, {"PRAGMA freelist_count", &freePages},
	} {
		if err = tx.QueryRowContext(ctx, q.sql).Scan(q.out); err != nil {
			fact.DatabaseAllocationStatus = "unavailable"
			return
		}
	}
	fact.DatabasePageSize = pageSize
	fact.DatabasePageCount = pageCount
	fact.DatabaseFreePages = freePages
	fact.DatabaseAllocationStatus = "complete"
	// Include indexes and FTS backing tables separately: they can account for
	// most of a database's bytes. Do not inspect stored titles, queries or URLs.
	rows, err := tx.QueryContext(ctx, `SELECT name, SUM(pgsize) AS bytes FROM dbstat GROUP BY name ORDER BY bytes DESC, name LIMIT 20`)
	if err != nil {
		fact.DatabaseObjectsStatus = "unavailable"
		return
	}
	defer rows.Close()
	objects := []diagnostics.DatabaseObjectFact{}
	for rows.Next() {
		var item diagnostics.DatabaseObjectFact
		if err = rows.Scan(&item.Name, &item.Bytes); err != nil {
			fact.DatabaseObjectsStatus = "unavailable"
			return
		}
		objects = append(objects, item)
	}
	if rows.Err() != nil {
		fact.DatabaseObjectsStatus = "unavailable"
		return
	}
	fact.DatabaseObjects = objects
	fact.DatabaseObjectsStatus = "complete"
}
