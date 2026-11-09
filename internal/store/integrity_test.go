package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestQuickCheckHealthyDatabase(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := QuickCheck(ctx, db)
	if err != nil || !res.OK || len(res.Findings) != 0 || res.Checked.IsZero() {
		t.Fatalf("QuickCheck = %+v, %v; want ok", res, err)
	}
	if err := Maintain(ctx, db); err != nil {
		t.Fatalf("Maintain on a healthy database: %v", err)
	}
}

// corruptDatabase writes a database with enough pages that overwriting the
// middle of the file damages b-tree pages rather than the header.
func corruptDatabase(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE store_damage(id INTEGER PRIMARY KEY, v TEXT); CREATE INDEX store_damage_v ON store_damage(v)`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 400; i++ {
		if _, err := db.ExecContext(ctx, `INSERT INTO store_damage(v) VALUES (hex(randomblob(400)))`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	path := filepath.Join(dir, "ether.db")
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := f.Stat()
	junk := make([]byte, 4096*6)
	for i := range junk {
		junk[i] = 0xA5
	}
	if _, err := f.WriteAt(junk, st.Size()/2); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return path
}

func TestQuickCheckReportsDamageWithoutFailing(t *testing.T) {
	ctx := context.Background()
	path := corruptDatabase(t)
	db, err := OpenPath(ctx, path)
	if err != nil {
		t.Fatalf("open damaged database: %v", err)
	}
	defer db.Close()
	res, err := QuickCheck(ctx, db)
	if err != nil {
		t.Fatalf("QuickCheck errored instead of reporting findings: %v", err)
	}
	if res.OK || len(res.Findings) == 0 || len(res.Findings) > maxIntegrityFindings {
		t.Fatalf("QuickCheck = %+v; want bounded findings", res)
	}
	err = Maintain(ctx, db)
	var ce *CorruptionError
	if !errors.As(err, &ce) && !IsCorrupt(err) {
		t.Fatalf("Maintain = %v; want a corruption error", err)
	}
}

func TestQuickCheckSkippedOnClosedDatabaseIsAnError(t *testing.T) {
	db, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := QuickCheck(context.Background(), db); err == nil {
		t.Fatal("QuickCheck on a closed database: want an error, not a finding")
	}
}

func TestIsCorrupt(t *testing.T) {
	for msg, want := range map[string]bool{
		"database disk image is malformed (11)": true,
		"file is not a database (26)":           true,
		"connection refused":                    false,
	} {
		if got := IsCorrupt(errors.New(msg)); got != want {
			t.Errorf("IsCorrupt(%q) = %v", msg, got)
		}
	}
	if IsCorrupt(nil) {
		t.Error("IsCorrupt(nil)")
	}
}

func TestOpenReportsMigrationProgress(t *testing.T) {
	ctx := context.Background()
	// A new database copies the process's migrated image; drop it so this
	// open migrates, as a server's first start does.
	memoryTemplate.Lock()
	memoryTemplate.image = nil
	memoryTemplate.Unlock()
	var seen []Progress
	db, _, err := OpenWith(ctx, Options{DataDir: t.TempDir(), Progress: func(p Progress) { seen = append(seen, p) }})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if len(seen) == 0 {
		t.Fatal("no progress reported while migrating a new database")
	}
	total := seen[0].Total
	for i, p := range seen {
		if p.Stage != StageMigrate || p.Total != total || p.Done != i {
			t.Fatalf("progress %d = %+v; want migrate %d/%d", i, p, i, total)
		}
	}
	if len(seen) != total {
		t.Fatalf("reported %d steps for %d migrations", len(seen), total)
	}
}

// The pinned SQLite xIntegrity does not invalidate its cached FTS structure
// when another connection merges segments. This reproduces the lab alarm
// without corrupting any bytes, and also checks genuine FTS damage afterward.
func TestQuickCheckFreshFTSReader(t *testing.T) {
	ctx := context.Background()
	// The name exercises URI escaping. '?' is reserved in Windows file
	// names (CreateFile fails, SQLite reports CANTOPEN), so only '#' and '%'
	// are exercised there.
	name := "fts #%? alarm.db"
	if runtime.GOOS == "windows" {
		name = "fts #% alarm.db"
	}
	path := filepath.Join(t.TempDir(), name)
	db, err := sql.Open("sqlite", sqliteFileURI(path)+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	writer, err := sql.Open("sqlite", sqliteFileURI(path))
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	exec := func(d *sql.DB, q string) {
		t.Helper()
		if _, err := d.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	exec(db, `CREATE VIRTUAL TABLE people USING fts5(name, prefix='1 2 3'); INSERT INTO people VALUES('Alice Smith')`)
	var before string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&before); err != nil || before != "ok" {
		t.Fatalf("initial: %s %v", before, err)
	}
	for i := 0; i < 20; i++ {
		exec(writer, fmt.Sprintf("INSERT INTO people VALUES('Bob Jones %d')", i))
	}
	exec(writer, `INSERT INTO people(people) VALUES('optimize')`)
	var stale string
	if err := db.QueryRowContext(ctx, `PRAGMA quick_check`).Scan(&stale); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stale, "corruption found reading blob") {
		t.Fatalf("expected pinned SQLite stale FTS regression, got %q", stale)
	}
	res, err := QuickCheck(ctx, db)
	if err != nil || !res.OK {
		t.Fatalf("fresh check: %+v %v", res, err)
	}
	exec(writer, `DELETE FROM people_data WHERE id>10`)
	res, err = QuickCheck(ctx, db)
	if err != nil || res.OK || len(res.Findings) == 0 {
		t.Fatalf("real FTS damage: %+v %v", res, err)
	}
}

// A Windows data directory path must reach SQLite as a local path, not as
// a URI authority ("file://C:%5C..."), or the fresh integrity reader cannot
// open; other paths keep their escaping.
func TestIntegrityReaderDSNKeepsWindowsPathsLocal(t *testing.T) {
	for path, want := range map[string]string{
		`C:\Users\runner\AppData\Local\Temp\x\ether.db`: "file:///C:/Users/runner/AppData/Local/Temp/x/ether.db?",
		"/var/lib/ether/fts #? alarm.db":                "file:///var/lib/ether/fts%20%23%3F%20alarm.db?",
	} {
		dsn := integrityReaderDSN(path)
		if !strings.HasPrefix(dsn, want) || !strings.Contains(dsn, "mode=ro") {
			t.Fatalf("integrityReaderDSN(%q) = %q, want prefix %q", path, dsn, want)
		}
	}
}
