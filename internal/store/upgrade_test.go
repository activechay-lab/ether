package store

import (
	"context"
	"testing"
)

// A cancelled tail must not lose committed batches, and a retry must not
// overwrite rows that another writer already converted.
func TestUpgradeBatchResume(t *testing.T) {
	ctx := context.Background()
	db := OpenTest(t)
	if _, err := db.Exec(`CREATE TABLE upgrade_batch_probe(id INTEGER PRIMARY KEY,value TEXT)`); err != nil {
		t.Fatal(err)
	}
	b := NewUpgradeBatch(db)
	for i := 0; i < 1200; i++ {
		if _, err := b.ExecContext(ctx, `INSERT INTO upgrade_batch_probe VALUES(?,'old')`, i); err != nil {
			t.Fatal(err)
		}
	}
	b.Close()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM upgrade_batch_probe`).Scan(&n); err != nil || n != 1000 {
		t.Fatal(n, err)
	}
	b = NewUpgradeBatch(db)
	defer b.Close()
	for i := 0; i < 1200; i++ {
		if _, err := b.ExecContext(ctx, `INSERT INTO upgrade_batch_probe VALUES(?,'new') ON CONFLICT(id) DO NOTHING`, i); err != nil {
			t.Fatal(err)
		}
	}
	if err := b.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM upgrade_batch_probe WHERE value='old'`).Scan(&n); err != nil || n != 1000 {
		t.Fatal(n, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM upgrade_batch_probe`).Scan(&n); err != nil || n != 1200 {
		t.Fatal(n, err)
	}
}
