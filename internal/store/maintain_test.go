package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSetsJournalSizeLimitAndMaintainRetainsBoundedWAL(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var limit int64
	if err := db.QueryRowContext(ctx, `PRAGMA journal_size_limit`).Scan(&limit); err != nil {
		t.Fatal(err)
	}
	if limit != journalSizeLimit {
		t.Fatalf("journal_size_limit = %d, want %d", limit, journalSizeLimit)
	}
	var synchronous int
	if err := db.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&synchronous); err != nil {
		t.Fatal(err)
	}
	if synchronous != 1 {
		t.Fatalf("synchronous = %d, want NORMAL (1)", synchronous)
	}
	var vacuum int
	if err := db.QueryRowContext(ctx, `PRAGMA auto_vacuum`).Scan(&vacuum); err != nil || vacuum != autoVacuumIncremental {
		t.Fatalf("auto_vacuum = %d (%v), want INCREMENTAL on a new database", vacuum, err)
	}
	var auto int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_autocheckpoint`).Scan(&auto); err != nil {
		t.Fatal(err)
	}
	if auto != 0 {
		t.Fatalf("wal_autocheckpoint = %d, want 0", auto)
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE store_test_t(v TEXT)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO store_test_t(v) VALUES (hex(randomblob(512)))`); err != nil {
			t.Fatal(err)
		}
	}
	wal := filepath.Join(dir, "ether.db-wal")
	if st, err := os.Stat(wal); err != nil || st.Size() == 0 {
		t.Fatalf("expected a non-empty WAL before maintenance: %v", err)
	}
	if err := Maintain(ctx, db); err != nil {
		t.Fatal(err)
	}
	// PASSIVE copies frames without taking the checkpoint writer lock. It
	// retains this small reusable file instead of demanding a zero-byte WAL.
	if st, err := os.Stat(wal); err != nil || st.Size() > journalSizeLimit {
		t.Fatalf("WAL after Maintain: stat=%v err=%v; retention limit=%d", st, err, journalSizeLimit)
	}
	var busy, frames, copied int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &frames, &copied); err != nil || busy != 0 || frames != copied {
		t.Fatalf("maintenance left uncheckpointed frames: busy=%d frames=%d copied=%d err=%v", busy, frames, copied, err)
	}
}

// journal_size_limit is a post-reuse retention bound, not a peak file-size
// cap. Exercise the same SQLite policy with a small limit, avoiding a 64MiB
// fixture. Keep the connection open so last-close cleanup cannot fake success.
func TestMaintainWALRetentionAppliesOnReuse(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	const limit = 64 << 10
	if _, err := db.ExecContext(ctx, `PRAGMA journal_size_limit=65536`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE wal_reuse(id INTEGER PRIMARY KEY,v BLOB); INSERT INTO wal_reuse VALUES(1,zeroblob(131072))`); err != nil {
		t.Fatal(err)
	}
	path, err := sqlitePath(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	wal := path + "-wal"
	before, err := os.Stat(wal)
	if err != nil || before.Size() <= limit {
		t.Fatalf("fixture must exceed scaled retention: %v %v", before, err)
	}
	if err := Maintain(ctx, db); err != nil {
		t.Fatal(err)
	}
	// Maintain may optimize after checkpointing. Drain those final frames
	// before explicitly exercising the subsequent writer's reuse boundary.
	var busy, frames, copied int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &frames, &copied); err != nil || busy != 0 || frames != copied {
		t.Fatalf("drain: %d/%d/%d %v", busy, frames, copied, err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO wal_reuse VALUES(2,x'01')`); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(wal)
	if err != nil || after.Size() <= 0 || after.Size() > limit {
		t.Fatalf("recycled WAL size: %v %v; limit=%d", after, err, limit)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM wal_reuse`).Scan(&n); err != nil || n != 2 {
		t.Fatalf("accepted writes lost: n=%d %v", n, err)
	}
}

func TestMaintainInMemory(t *testing.T) {
	db, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Maintain(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}
