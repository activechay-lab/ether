package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/diagnostics"
)

func TestSupportDatabaseAllocationSnapshot(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE support_fixture(id INTEGER PRIMARY KEY, secret TEXT, data BLOB)`,
		`INSERT INTO support_fixture VALUES(1,'secret-provider-token',zeroblob(131072)),(2,'private-title',zeroblob(65536))`,
		`DELETE FROM support_fixture WHERE id=1`,
	} {
		if _, err = db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	var beforePages, beforeFree int64
	if err = db.QueryRow(`PRAGMA page_count`).Scan(&beforePages); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`PRAGMA freelist_count`).Scan(&beforeFree); err != nil {
		t.Fatal(err)
	}
	var fact diagnostics.StorageFact
	collectDatabaseStorage(db, &fact)
	if fact.DatabaseAllocationStatus != "complete" || fact.DatabaseObjectsStatus != "complete" || fact.DatabasePageCount != beforePages || fact.DatabaseFreePages != beforeFree || beforeFree == 0 {
		t.Fatalf("allocation: %+v", fact)
	}
	found := false
	for _, object := range fact.DatabaseObjects {
		if object.Name == "support_fixture" && object.Bytes >= 65536 {
			found = true
		}
	}
	if !found {
		t.Fatalf("fixture allocation missing: %+v", fact)
	}
	raw, _ := json.Marshal(fact)
	if strings.Contains(string(raw), "secret-provider-token") || strings.Contains(string(raw), "private-title") {
		t.Fatal("row contents leaked")
	}
	var afterFree, count int64
	if err = db.QueryRow(`PRAGMA freelist_count`).Scan(&afterFree); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM support_fixture`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if afterFree != beforeFree || count != 1 {
		t.Fatal("support capture changed the database")
	}
	db.Close()
	var closed diagnostics.StorageFact
	collectDatabaseStorage(db, &closed)
	if closed.DatabaseAllocationStatus != "unavailable" {
		t.Fatalf("closed database: %+v", closed)
	}
}

// The interior-page walk must agree with dbstat page for page: a deep
// narrow table and index (whose leaves are never read), and a table of
// large values whose overflow pages are counted from its cells.
func TestSupportBTreePagesMatchDBStat(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	for _, stmt := range []string{
		`CREATE TABLE narrow(id INTEGER PRIMARY KEY, name TEXT NOT NULL, n INTEGER NOT NULL)`,
		`CREATE INDEX narrow_name ON narrow(name, n)`,
		`CREATE TABLE pairs(a TEXT NOT NULL, b INTEGER NOT NULL, PRIMARY KEY(a, b)) WITHOUT ROWID`,
		`CREATE TABLE blobs(id TEXT PRIMARY KEY, kind TEXT NOT NULL, size INTEGER NOT NULL, content BLOB NOT NULL)`,
		`WITH RECURSIVE s(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM s WHERE i<60000) INSERT INTO narrow SELECT i, printf('name-%08d-%s', i*7919 % 60000, hex(randomblob(6))), i FROM s`,
		`WITH RECURSIVE s(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM s WHERE i<40000) INSERT INTO pairs SELECT printf('k%06d', i % 997), i FROM s`,
		`WITH RECURSIVE s(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM s WHERE i<60) INSERT INTO blobs SELECT printf('d%03d', i), 'reel', i*9000, randomblob(i*9000) FROM s`,
		`INSERT INTO blobs VALUES('small','image',40,randomblob(40)),('edge','image',4061,randomblob(4061))`,
		`ANALYZE`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	objects, _, status := sqliteObjects(context.Background(), tx)
	if status != "complete" {
		t.Fatalf("status %s", status)
	}
	for _, o := range objects {
		var want int64
		if err := tx.QueryRow(`SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name=?`, o.Name).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if o.Bytes != want {
			t.Errorf("%s: walk %d bytes, dbstat %d", o.Name, o.Bytes, want)
		}
	}
	if len(objects) < 5 {
		t.Fatalf("objects: %+v", objects)
	}
}
