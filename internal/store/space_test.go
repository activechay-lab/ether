package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
)

// A database created before incremental vacuum converts once, when asked, and then
// hands freed pages back to the filesystem, keeping its rows.
func TestConvertAutoVacuumThenReleaseFreePages(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "ether.db")
	old, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.ExecContext(ctx, `CREATE TABLE space_test_t(id INTEGER PRIMARY KEY, v BLOB); WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<4000) INSERT INTO space_test_t SELECT i, randomblob(4000) FROM n`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	db, err := OpenPath(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pragma := func(name string) int64 {
		var v int64
		if err := db.QueryRowContext(ctx, `PRAGMA `+name).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if pragma("auto_vacuum") != 0 {
		t.Fatal("an existing database must not convert on an ordinary open")
	}
	plan, err := Compact(ctx, db)
	if err != nil || !plan.Incremental || plan.Ready {
		t.Fatalf("compact: %+v %v", plan, err)
	}
	if _, err := Compact(ctx, db); !errors.Is(err, ErrCompactBlocked) {
		t.Fatalf("second compact: %v", err)
	}
	if pragma("auto_vacuum") != autoVacuumIncremental {
		t.Fatalf("auto_vacuum = %d after conversion", pragma("auto_vacuum"))
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM space_test_t WHERE id>1000`); err != nil {
		t.Fatal(err)
	}
	before := pragma("page_count")
	n, err := ReleaseFreePages(ctx, db)
	if err != nil || n == 0 {
		t.Fatalf("released %d pages, err %v", n, err)
	}
	if free := pragma("freelist_count"); free > vacuumSlackPages {
		t.Fatalf("freelist %d after release", free)
	}
	if after := pragma("page_count"); after > before-n { // pointer-map pages go too
		t.Fatalf("page_count %d -> %d, released %d", before, after, n)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM space_test_t`).Scan(&rows); err != nil || rows != 1000 {
		t.Fatalf("rows %d err %v", rows, err)
	}
}
