package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"reflect"
	"sync"
)

// A new SQLite database starts as a copy of one migrated in memory, once per
// process and set of migrations. Applying every migration re-reads a schema
// that grows with each one (every ALTER TABLE re-parses all of it), so a
// process that creates many databases (a test package: OpenMemory, or a
// server started on an empty data directory) paid that again for each.
// The copy is the same database: migrations are only ever applied by the
// process that registered them (from init), and the image is rebuilt
// whenever the migrations that would apply differ from the ones it holds.

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

// migratedImage returns a serialized, freshly migrated SQLite database for
// the migrations that opening with ctx applies (see WithMigrationCeiling).
// progress hears the migrations when the image has to be built.
func migratedImage(ctx context.Context, progress func(Progress)) ([]byte, error) {
	key := sqliteRegistryKey(ctx)
	memoryTemplate.Lock()
	defer memoryTemplate.Unlock()
	if memoryTemplate.image != nil && memoryTemplate.key == key {
		return memoryTemplate.image, nil
	}
	db, err := openSQLite(ctx, ":memory:", nil, progress)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var image []byte
	if err := withSQLiteConn(ctx, db, func(c sqliteImage) error {
		image, err = c.Serialize()
		return err
	}); err != nil {
		return nil, fmt.Errorf("store: serialize migrated database: %w", err)
	}
	memoryTemplate.key, memoryTemplate.image = key, image
	return image, nil
}

// sqliteRegistryKey identifies everything migrateSQLite would apply with
// ctx, in order. Tests that add or swap migrations change it.
func sqliteRegistryKey(ctx context.Context) [sha256.Size]byte {
	h := sha256.New()
	for _, m := range pendingFor(ctx) {
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
