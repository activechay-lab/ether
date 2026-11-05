//go:build upgradebench

package app

// Opt-in measurement harness: never runs in CI. See docs/acceptance/upgrade-performance.md.
import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cardigann"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/notify"
	"github.com/braedonsaunders/ether/internal/ratings"
	"github.com/braedonsaunders/ether/internal/startup"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trakt"
	"modernc.org/sqlite"
)

type upgradeSlowDriver struct{ driver.Driver }
type upgradeSlowConn struct {
	driver.Conn
	active bool
}
type upgradeSlowTx struct {
	driver.Tx
	conn *upgradeSlowConn
}

func (d upgradeSlowDriver) Open(n string) (driver.Conn, error) {
	c, e := d.Driver.Open(n)
	if e != nil {
		return nil, e
	}
	return &upgradeSlowConn{Conn: c}, nil
}
func (c *upgradeSlowConn) Begin() (driver.Tx, error) {
	tx, e := c.Conn.Begin()
	c.active = e == nil
	return upgradeSlowTx{tx, c}, e
}
func (tx upgradeSlowTx) Commit() error {
	time.Sleep(10 * time.Millisecond)
	tx.conn.active = false
	return tx.Tx.Commit()
}
func (tx upgradeSlowTx) Rollback() error { tx.conn.active = false; return tx.Tx.Rollback() }
func (c *upgradeSlowConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if !c.active {
		time.Sleep(10 * time.Millisecond)
	}
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, q, args)
}
func (c *upgradeSlowConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, q, args)
}
func TestUpgradeManual(t *testing.T) {
	dir := os.Getenv("ETHER_UPGRADE_MEASURE")
	if dir == "" {
		t.Skip("opt-in large fixture")
	}
	ctx := context.Background()
	workName := ""
	workAt := time.Now()
	work := func(name string, done, total int) {
		if name == workName {
			return
		}
		if workName != "" {
			t.Logf("WORK %s %s", workName, time.Since(workAt))
		}
		workName, workAt = name, time.Now()
	}
	ctx = store.WithWorkProgress(ctx, work)
	defer func() { work("", 0, 0) }()
	start := time.Now()
	last := start
	label := "open"
	progress := func(p store.Progress) {
		work("", 0, 0)
		t.Logf("STEP %s %s", label, time.Since(last))
		last = time.Now()
		label = fmt.Sprintf("%s %s %d/%d", p.Stage, p.Name, p.Done+1, p.Total)
	}
	db, _, err := store.OpenWith(ctx, store.Options{DataDir: dir, Progress: progress})
	if err != nil {
		t.Fatal(err)
	}
	work("", 0, 0)
	t.Logf("STEP %s %s; schema total %s", label, time.Since(last), time.Since(start))
	db.Close()
	sql.Register("upgrade-slow", upgradeSlowDriver{&sqlite.Driver{}})
	db, err = sql.Open("upgrade-slow", filepath.Join(dir, "ether.db")+"?_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	settingsAt := time.Now()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("STEP settings %s", time.Since(settingsAt))
	a := &App{db: db, cfg: cfg, log: quietLog(), tracker: startup.NewTracker("measure", nil)}
	a.trakt = trakt.New(trakt.Options{DB: db, Sealer: cfg, Logger: quietLog()})
	a.livetv, err = livetv.New(livetv.Options{DB: db, Sealer: cfg, Logger: quietLog()})
	if err != nil {
		t.Fatal(err)
	}
	defer a.livetv.Close()
	a.notifications = notify.New(db, notify.Options{Sealer: cfg, Logger: quietLog()})
	a.ratings = ratings.New(db, ratings.Options{Sealer: cfg})
	a.sites = cardigann.New(cardigann.Options{DB: db, Sealer: cfg, Logger: quietLog()})
	indexes := []config.Indexer{}
	if os.Getenv("ETHER_UPGRADE_ACCESSORIES") != "" {
		_, err := a.sites.Install(ctx, []byte("id: bench\nname: Bench\nlinks: [https://indexer.example/]\nsearch: {path: search, rows: {selector: li}, fields: {title: {selector: a}, magnet: {selector: a}}}\n"), "upload", "")
		if err != nil {
			t.Fatal(err)
		}
		for _, query := range []string{
			`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<42000) INSERT INTO cardigann_sessions(indexer_id,cookies,updated_at) SELECT 'ix'||i,'[{"Name":"session","Value":"fake-cookie"}]',0 FROM n WHERE true ON CONFLICT(indexer_id) DO UPDATE SET cookies=excluded.cookies`,
			`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<42000) INSERT INTO notify_channels(id,config) SELECT 'n'||i,json_object('id','n'||i,'kind','webhook','secrets',json_object('endpoint','https://notify.example/hook?token=fake-'||i)) FROM n WHERE true ON CONFLICT(id) DO UPDATE SET config=excluded.config`,
			`INSERT INTO ratings_settings(id,value) VALUES(1,'{"provider":"omdb","apiKey":"fake-key"}') ON CONFLICT(id) DO UPDATE SET value=excluded.value`,
		} {
			if _, err := db.Exec(query); err != nil {
				t.Fatal(err)
			}
		}

		for i := 1; i <= 42000; i++ {
			indexes = append(indexes, config.Indexer{ID: fmt.Sprintf("ix%d", i), Kind: config.IndexerKindCardigann, Definition: "bench", URL: "https://indexer.example/"})
		}
	}

	for _, step := range []struct {
		name string
		fn   func() error
	}{
		{"catalogue sort titles", func() error {
			_, e := catalog.New(db, nil, config.Catalog{}, quietLog(), catalog.WithStartupContext(ctx))
			return e
		}},
		{"credential blobs", func() error { return a.migrateCredentialBlobs(ctx) }},
		{"IPTV sources", func() error { _, e := a.livetv.MigrateSecrets(ctx); return e }},
		{"Trakt tokens", func() error { _, e := a.trakt.MigrateSecrets(ctx); return e }},
		{"indexer cookies", func() error { _, e := a.sites.MigrateSecrets(ctx, indexes); return e }},
		{"notifications", func() error { _, e := a.notifications.MigrateSecrets(ctx); return e }},
		{"ratings", func() error { _, e := a.ratings.MigrateSecrets(ctx); return e }},
		{"checkpoint", func() error { _, e := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); return e }},
	} {
		if only := os.Getenv("ETHER_UPGRADE_ONLY"); only != "" && only != step.name {
			continue
		}
		work("", 0, 0)
		s := time.Now()
		if err := step.fn(); err != nil {
			t.Fatal(step.name, err)
		}
		work("", 0, 0)
		t.Logf("STEP %s %s", step.name, time.Since(s))
	}
	t.Logf("TOTAL %s", time.Since(start))
}
