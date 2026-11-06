package app

import (
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
