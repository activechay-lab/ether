package catalog

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func admissionCatalog(t *testing.T) *Catalog {
	t.Helper()
	db, err := store.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c, err := New(db, nil, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// A slow optional detail write must roll back its entire group, admit a
// competing viewer write, and retry the primary titles with fresh budgets.
func TestDetailAdmissionRollsBackAndYields(t *testing.T) {
	c := admissionCatalog(t)
	ctx := context.Background()
	if _, err := c.db.Exec(`CREATE TABLE admission_probe (id INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	var once sync.Once
	slow := &detailExtra{after: func(ctx context.Context, tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO admission_probe VALUES (1)`); err != nil {
			return err
		}
		once.Do(func() { close(started) })
		<-ctx.Done()
		return ctx.Err()
	}}
	var batch []detailWrite
	for i := int64(1); i <= 2; i++ {
		batch = append(batch, detailWrite{ctx: ctx, item: upsertItem{t: core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, Title: "detail"}, o: upsertOpts{detail: true}}, extra: slow, done: make(chan error, 1)})
	}
	finished := make(chan struct{})
	go func() { defer close(finished); c.flushDetailsBudget(batch, 100*time.Millisecond) }()
	<-started
	viewerCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	tx, err := c.db.BeginTx(viewerCtx, nil)
	if err == nil {
		_, err = tx.ExecContext(viewerCtx, `INSERT INTO admission_probe VALUES (2)`)
		if err == nil {
			err = tx.Commit()
		} else {
			_ = tx.Rollback()
		}
	}
	<-finished
	if err != nil {
		t.Fatalf("viewer write rejected: %v", err)
	}
	for _, w := range batch {
		if err := <-w.done; err != nil {
			t.Fatalf("primary retry: %v", err)
		}
	}
	var probe, titles int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM admission_probe WHERE id=1`).Scan(&probe); err != nil {
		t.Fatal(err)
	}
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM catalog_titles`).Scan(&titles); err != nil {
		t.Fatal(err)
	}
	if probe != 0 || titles != 2 {
		t.Fatalf("rolled back extras=%d, committed primary titles=%d", probe, titles)
	}
}

// Fail after copying rows but before advancing the cursor. The original
// catalogue and prior progress must survive, and the same batch must resume.
func TestTierCopyFailureKeepsCanonicalAndResumes(t *testing.T) {
	c := admissionCatalog(t)
	ctx := context.Background()
	item := upsertItem{t: core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "touched"}, o: upsertOpts{detail: true}}
	if err := c.upsertItems(ctx, []upsertItem{item}); err != nil {
		t.Fatal(err)
	}
	if err := c.tierPrepareCopy(ctx); err != nil {
		t.Fatal(err)
	}
	tables, err := existingTables(ctx, c.db, store.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	cols, err := titleColumns(ctx, c.db, "catalog_titles")
	if err != nil {
		t.Fatal(err)
	}
	before := c.getMeta(ctx, tierMetaCursor)
	if _, err := c.db.Exec(`CREATE TRIGGER fail_copy_cursor BEFORE UPDATE ON catalog_meta WHEN NEW.k='` + tierMetaCursor + `' BEGIN SELECT RAISE(ABORT,'injected copy failure'); END`); err != nil {
		t.Fatal(err)
	}
	p := TierMigration{}
	touched := newTierTouched()
	touched.add(item.t.Key)
	if _, _, err := c.tierCopyBatch(ctx, tables, cols, 10, touched, &p); err == nil {
		t.Fatal("expected rollback")
	}
	var old, kept int
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM catalog_titles`).Scan(&old); err != nil {
		t.Fatal(err)
	}
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM catalog_titles_keep`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if old != 1 || kept != 0 || c.getMeta(ctx, tierMetaCursor) != before || p.Removed != 0 {
		t.Fatalf("rollback old=%d keep=%d cursor=%s progress=%+v", old, kept, c.getMeta(ctx, tierMetaCursor), p)
	}
	if _, err := c.db.Exec(`DROP TRIGGER fail_copy_cursor`); err != nil {
		t.Fatal(err)
	}
	if n, more, err := c.tierCopyBatch(ctx, tables, cols, 10, touched, &p); err != nil || n != 1 || !more {
		t.Fatalf("resume n=%d more=%v err=%v", n, more, err)
	}
	if err := c.db.QueryRow(`SELECT COUNT(*) FROM catalog_titles_keep WHERE key=?`, int64(item.t.Key)).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if kept != 1 {
		t.Fatal(errors.New("successful touch lost on resume"))
	}
}

// Without a transaction execution budget, a real detail transaction blocks a
// queued viewer even though the viewer only needs a single short write.
func TestDetailUnbudgetedTransactionRejectsViewer(t *testing.T) {
	c := admissionCatalog(t)
	viewer, err := c.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer viewer.Close()
	if _, err := viewer.ExecContext(context.Background(), "PRAGMA busy_timeout=50"); err != nil {
		t.Fatal(err)
	}
	started, release, finished := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		finished <- c.upsertItems(context.Background(), nil, func(ctx context.Context, tx *sql.Tx) error {
			close(started)
			<-release
			return errors.New("rollback controlled hold")
		})
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	tx, err := viewer.BeginTx(ctx, nil)
	if err == nil {
		_ = tx.Rollback()
	}
	cancel()
	close(release)
	if holdErr := <-finished; holdErr == nil {
		t.Fatal("expected held transaction rollback")
	}
	if !store.IsWriteContention(err) {
		t.Fatalf("expected viewer admission timeout, got %v", err)
	}
}
