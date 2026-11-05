package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"github.com/braedonsaunders/ether/internal/dburl"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestPostgresEnv names the environment variable that points package tests
// at a PostgreSQL server instead of in-memory SQLite, e.g.
//
//	ETHER_TEST_POSTGRES='postgres://ether:ether@127.0.0.1:5432/ether?sslmode=disable' go test ./...
//
// Each test database is a fresh, randomly named schema that is dropped when
// the *sql.DB is closed.
const TestPostgresEnv = "ETHER_TEST_POSTGRES"

// testSchemaPrefix marks throwaway schemas; the name embeds the creation
// time so abandoned schemas (a test that never closed its DB, a killed run)
// can be swept later.
const testSchemaPrefix = "ether_test_"

// memoryDSN opens a private in-memory SQLite database.
const memoryDSN = "file::memory:?_pragma=foreign_keys(1)"

// OpenMemory is a migrated, empty database for tests: in-memory SQLite (a
// single connection), or a fresh PostgreSQL schema when TestPostgresEnv is
// set.
//
// The SQLite database is a copy of one migrated once per process. Applying
// every component's migrations re-reads a schema that grows with each one,
// which costs seconds under the race detector; packages that open hundreds
// of test databases spent most of their time there. The copy is the same
// database: migrations are only ever applied by the process that registered
// them (from init), and the image is rebuilt whenever the effective SQLite
// registry differs from the one it was migrated with.
func OpenMemory(ctx context.Context) (*sql.DB, error) {
	if u := os.Getenv(TestPostgresEnv); u != "" {
		return OpenTestPostgres(ctx, u)
	}
	image, err := migratedMemoryImage(ctx)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite-cached", memoryDSN)
	if err != nil {
		return nil, err
	}
	// The database lives in this one connection; a second would be a
	// different, empty database.
	db.SetMaxOpenConns(1)
	if err := withSQLiteConn(ctx, db, func(c sqliteImage) error { return c.Deserialize(image) }); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: load migrated test database: %w", err)
	}
	return db, nil
}

// SeedTestDataDir writes the migrated test database to dataDir/ether.db, for
// a test whose server has already been set up: opening it (Open, app.New)
// takes the ordinary startup path for an up-to-date database instead of
// migrating a fresh file. It never replaces an existing database.
func SeedTestDataDir(t TB, dataDir string) {
	t.Helper()
	image, err := migratedMemoryImage(context.Background())
	if err != nil {
		t.Fatalf("store: seed test data directory: %v", err)
	}
	f, err := os.OpenFile(filepath.Join(dataDir, "ether.db"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("store: seed test data directory: %v", err)
	}
	_, err = f.Write(image)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatalf("store: seed test data directory: %v", err)
	}
}

// sqliteImage is the part of the modernc.org/sqlite connection that copies
// a whole database in and out of memory.
type sqliteImage interface {
	Serialize() ([]byte, error)
	Deserialize([]byte) error
}

func withSQLiteConn(ctx context.Context, db *sql.DB, fn func(sqliteImage) error) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		sc, ok := dc.(*stmtConn)
		if !ok {
			return fmt.Errorf("unexpected driver connection %T", dc)
		}
		img, ok := sc.inner.(sqliteImage)
		if !ok {
			return fmt.Errorf("driver connection %T cannot serialize", sc.inner)
		}
		return fn(img)
	})
}

var memoryTemplate struct {
	sync.Mutex
	key   [sha256.Size]byte
	image []byte
}

// migratedMemoryImage returns a serialized, freshly migrated SQLite
// database for the migrations registered now.
func migratedMemoryImage(ctx context.Context) ([]byte, error) {
	key := sqliteRegistryKey()
	memoryTemplate.Lock()
	defer memoryTemplate.Unlock()
	if memoryTemplate.image != nil && memoryTemplate.key == key {
		return memoryTemplate.image, nil
	}
	db, err := openSQLite(ctx, ":memory:", nil, nil)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var image []byte
	if err := withSQLiteConn(ctx, db, func(c sqliteImage) error {
		image, err = c.Serialize()
		return err
	}); err != nil {
		return nil, fmt.Errorf("store: serialize migrated test database: %w", err)
	}
	memoryTemplate.key, memoryTemplate.image = key, image
	return image, nil
}

// sqliteRegistryKey identifies everything migrateSQLite would apply, in
// order. Tests that add or swap migrations change it.
func sqliteRegistryKey() [sha256.Size]byte {
	h := sha256.New()
	for _, m := range registered() {
		var apply uintptr
		if m.sqliteApply != nil {
			apply = reflect.ValueOf(m.sqliteApply).Pointer()
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00%x\x00%s\x00", m.component, m.version, len(m.sqlFor(SQLite)), apply, m.sqlFor(SQLite))
	}
	var key [sha256.Size]byte
	h.Sum(key[:0])
	return key
}

// TB is the part of testing.TB that OpenTest uses.
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// OpenTest is OpenMemory for a test: it fails the test on error and closes
// (on PostgreSQL: drops) the database when the test ends.
func OpenTest(t TB) *sql.DB {
	t.Helper()
	db, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatalf("store: open test database: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

var sweepOnce sync.Once

// OpenTestPostgres creates a uniquely named schema on the server at rawURL,
// migrates it and returns a *sql.DB whose connections use it as their
// search_path. Closing the DB drops the schema.
func OpenTestPostgres(ctx context.Context, rawURL string) (*sql.DB, error) {
	cfg, err := pgx.ParseConfig(rawURL)
	if err != nil {
		return nil, fmt.Errorf("store: invalid %s URL %s", TestPostgresEnv, dburl.Redact(rawURL))
	}
	sweepOnce.Do(func() { sweepTestSchemas(ctx, cfg) })
	var rnd [6]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return nil, err
	}
	schema := testSchemaPrefix + strconv.FormatInt(time.Now().Unix(), 10) + "_" + hex.EncodeToString(rnd[:])
	if err := adminExec(ctx, cfg, `CREATE SCHEMA `+schema); err != nil {
		return nil, fmt.Errorf("store: create test schema: %w", err)
	}
	conf := cfg.Copy()
	if conf.RuntimeParams == nil {
		conf.RuntimeParams = map[string]string{}
	}
	conf.RuntimeParams["search_path"] = schema
	conf.RuntimeParams["application_name"] = "ether-test"
	drop := func() error {
		dctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return adminExec(dctx, cfg, `SET lock_timeout = '5s'; DROP SCHEMA IF EXISTS `+schema+` CASCADE`)
	}
	db := sql.OpenDB(newPGConnector(conf, drop))
	// Keep the footprint small: tests that never Close their DB must not
	// exhaust the server's connections, so idle ones are reaped quickly.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(2 * time.Second)
	if err := preparePostgres(ctx, db, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func adminExec(ctx context.Context, cfg *pgx.ConnConfig, sql string) error {
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	_, err = conn.Exec(ctx, sql)
	return err
}

// sweepTestSchemas drops throwaway schemas older than two hours.
func sweepTestSchemas(ctx context.Context, cfg *pgx.ConnConfig) {
	conn, err := pgx.ConnectConfig(ctx, cfg.Copy())
	if err != nil {
		return
	}
	defer conn.Close(context.Background())
	rows, err := conn.Query(ctx, `SELECT nspname FROM pg_namespace WHERE nspname LIKE 'ether\_test\_%'`)
	if err != nil {
		return
	}
	var stale []string
	cutoff := time.Now().Add(-2 * time.Hour).Unix()
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			continue
		}
		ts, _, _ := strings.Cut(strings.TrimPrefix(name, testSchemaPrefix), "_")
		if n, err := strconv.ParseInt(ts, 10, 64); err == nil && n < cutoff {
			stale = append(stale, name)
		}
	}
	if err := rows.Err(); err != nil {
		slog.Default().Warn("sweep test schemas", "err", err)
	}
	rows.Close()
	for _, name := range stale {
		_, _ = conn.Exec(ctx, `SET lock_timeout = '2s'; DROP SCHEMA IF EXISTS `+pgx.Identifier{name}.Sanitize()+` CASCADE`)
	}
}
