package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/backup"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/perfharness"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestTierMigrationUpgradeBench measures the 0.3.5 → two-tier upgrade on an
// Off-preset mirror (perfharness.OffMirror800k, built from TMDB's exports).
// It is a measurement, not a gate, and runs only when asked:
//
//	ETHER_TIER_BENCH=gen ETHER_TIER_BENCH_DIR=/scratch ETHER_TIER_EXPORTS=/exports \
//	  [ETHER_TIER_BENCH_PG=postgres://.../fixture] go test ./internal/app -run TierMigrationUpgradeBench -timeout 3h
//	ETHER_TIER_BENCH=run ETHER_TIER_BENCH_DIR=/scratch [ETHER_TIER_BENCH_PG=postgres://.../run] ...
//
// gen writes the fixture as 0.3.5 left it (the arming migration not yet
// applied). run copies it (on PostgreSQL the caller creates the run
// database from the fixture as a template), opens it the way the server
// does (pre-migrate snapshot, pending migrations), and runs the tier
// migration beside readers, writers and new touches, reporting time to
// open, job time per phase, WAL and disk, request latencies, and whether
// every touch made during the run kept its title.
func TestTierMigrationUpgradeBench(t *testing.T) {
	mode, dir := os.Getenv("ETHER_TIER_BENCH"), os.Getenv("ETHER_TIER_BENCH_DIR")
	if mode == "" || dir == "" {
		t.Skip("set ETHER_TIER_BENCH=gen|run and ETHER_TIER_BENCH_DIR")
	}
	pgURL := os.Getenv("ETHER_TIER_BENCH_PG")
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	switch mode {
	case "gen":
		benchGenerate(t, ctx, dir, pgURL, log)
	case "run":
		benchRun(t, ctx, dir, pgURL, log)
	default:
		t.Fatalf("ETHER_TIER_BENCH=%q", mode)
	}
}

func benchOpen(t *testing.T, ctx context.Context, dataDir, pgURL string, log *slog.Logger) *sql.DB {
	t.Helper()
	db, _, err := store.OpenWith(ctx, store.Options{DataDir: dataDir, URL: pgURL, Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func benchGenerate(t *testing.T, ctx context.Context, dir, pgURL string, log *slog.Logger) {
	exports := os.Getenv("ETHER_TIER_EXPORTS")
	fixture := filepath.Join(dir, "fixture")
	if err := os.MkdirAll(fixture, 0o700); err != nil {
		t.Fatal(err)
	}
	db := benchOpen(t, ctx, fixture, pgURL, log)
	defer db.Close()
	o := perfharness.OffMirror800k
	o.MovieExport = filepath.Join(exports, "movie_ids_11_07.json.gz")
	o.TVExport = filepath.Join(exports, "tv_series_ids_11_07.json.gz")
	if n := os.Getenv("ETHER_TIER_SCALE"); n != "" {
		var div int
		fmt.Sscan(n, &div)
		o.Movies, o.Shows, o.DetailMovies, o.DetailShows = o.Movies/div, o.Shows/div, o.DetailMovies/div, o.DetailShows/div
		if os.Getenv("ETHER_TIER_SAMPLE") != "" {
			o.Stride = div
		}
	}
	began := time.Now()
	sum, err := perfharness.GenerateOffMirror(ctx, db, o)
	if err != nil {
		t.Fatal(err)
	}
	// Rewind to 0.3.5: the arming migration is pending, as on an upgrade.
	for _, q := range []string{
		`DELETE FROM schema_migrations WHERE component='catalog_tier_migrate'`,
		`DELETE FROM catalog_meta WHERE k LIKE 'tier_migrate%'`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if store.DialectOf(db) == store.SQLite {
		if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.MarshalIndent(sum, "", "  ")
	t.Logf("fixture in %s: %s", time.Since(began).Round(time.Second), raw)
}

type latencies struct {
	mu   sync.Mutex
	all  []time.Duration
	errs int
	last string
}

func (l *latencies) add(d time.Duration, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		l.errs++
		l.last = err.Error()
		fmt.Fprintf(os.Stderr, "%s BENCH ERROR after %s: %v\n", time.Now().Format("15:04:05.000"), d, err)
		return
	}
	l.all = append(l.all, d)
}

func (l *latencies) summary() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	s := slices.Clone(l.all)
	slices.Sort(s)
	q := func(p float64) string {
		if len(s) == 0 {
			return "-"
		}
		return s[min(len(s)-1, int(float64(len(s))*p))].Round(100 * time.Microsecond).String()
	}
	over := 0
	for _, d := range s {
		if d >= time.Second {
			over++
		}
	}
	return map[string]any{"n": len(s), "p50": q(0.5), "p99": q(0.99), "max": q(1), "over1s": over, "errors": l.errs, "lastError": l.last}
}

func benchRun(t *testing.T, ctx context.Context, dir, pgURL string, log *slog.Logger) {
	pg := pgURL != ""
	run := filepath.Join(dir, "run-"+map[bool]string{true: "pg", false: "sqlite"}[pg])
	if err := os.RemoveAll(run); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(run, 0o700); err != nil {
		t.Fatal(err)
	}
	if !pg {
		if err := copyFile(filepath.Join(dir, "fixture", "ether.db"), filepath.Join(run, "ether.db")); err != nil {
			t.Fatal(err)
		}
	}
	report := map[string]any{"engine": map[bool]string{true: "postgres", false: "sqlite"}[pg]}
	sizes := func(db *sql.DB) map[string]int64 {
		out := map[string]int64{}
		if pg {
			var dbBytes, titles int64
			db.QueryRowContext(ctx, `SELECT pg_database_size(current_database()), pg_total_relation_size('catalog_titles')`).Scan(&dbBytes, &titles)
			out["database"], out["catalog_titles_total"] = dbBytes, titles
			return out
		}
		for _, suffix := range []string{"", "-wal", store.PreMigrateSuffix} {
			if st, err := os.Stat(filepath.Join(run, "ether.db"+suffix)); err == nil {
				out["file"+suffix] = st.Size()
			}
		}
		var pages, free int64
		db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages)
		db.QueryRowContext(ctx, `PRAGMA freelist_count`).Scan(&free)
		out["pages"], out["free_pages"] = pages, free
		return out
	}

	// Open as the server does: snapshot, pending migrations. The server
	// serves as soon as this returns; the job runs beside it.
	began := time.Now()
	db := benchOpen(t, ctx, run, pgURL, log)
	defer db.Close()
	report["open"] = time.Since(began).Round(time.Millisecond).String()
	report["before"] = sizes(db)
	var titles int
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles`).Scan(&titles)
	report["rows_before"] = titles

	// TMDB answers detail fetches, so the final sweep can store again a
	// title touched in the instant between a batch's re-check and its
	// delete (PostgreSQL).
	cat, err := catalog.New(db, &tierTMDB{}, config.Catalog{Language: "en-US", Region: "US"}, log)
	if err != nil {
		t.Fatal(err)
	}
	backups := backup.New(db, filepath.Join(run, "config.json"), filepath.Join(run, "backups"), 7)
	sqlitePath := ""
	if !pg {
		sqlitePath = filepath.Join(run, "ether.db")
	}
	var backupTook time.Duration
	cat.SetTierBackup(func(ctx context.Context, since time.Time) (string, error) {
		b := time.Now()
		defer func() { backupTook = time.Since(b) }()
		return tierBackup(ctx, backups, sqlitePath, since)
	})
	if err := os.WriteFile(filepath.Join(run, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	// Library keys for readers and writers; long-tail keys for touches.
	var lib, tail []core.RatingKey
	rows, err := db.QueryContext(ctx, `SELECT key, (popularity>=2.5 OR vote_count>=10 OR detail=1) FROM catalog_titles WHERE type IN ('movie','show')`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k int64
		var keep bool
		rows.Scan(&k, &keep)
		if keep {
			lib = append(lib, core.RatingKey(k))
		} else {
			tail = append(tail, core.RatingKey(k))
		}
	}
	rows.Close()

	loadCtx, stopLoad := context.WithCancel(ctx)
	var wg sync.WaitGroup
	var browse, get, write, touch latencies
	var touchedMu sync.Mutex
	var touched []core.RatingKey
	var owner int64
	db.QueryRowContext(ctx, `SELECT MIN(id) FROM auth_users`).Scan(&owner)
	readers := 4
	if os.Getenv("ETHER_TIER_BENCH_NOREAD") != "" {
		readers = 0
	}
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(seed, 1))
			for loadCtx.Err() == nil {
				b := time.Now()
				typ := core.TypeMovie
				if rng.IntN(3) == 0 {
					typ = core.TypeShow
				}
				var err error
				if os.Getenv("ETHER_TIER_BENCH_NOBROWSE") == "" {
					_, err = cat.Browse(loadCtx, core.BrowseQuery{Filter: core.Filter{Type: typ}, Sort: core.SortPopularity, Desc: true, Offset: rng.IntN(5000), Limit: 50})
					if loadCtx.Err() == nil {
						browse.add(time.Since(b), err)
					}
				}
				if os.Getenv("ETHER_TIER_BENCH_NOGET") == "" {
					b = time.Now()
					_, err = cat.Get(loadCtx, lib[rng.IntN(len(lib))])
					if loadCtx.Err() == nil {
						get.add(time.Since(b), err)
					}
				}
				time.Sleep(20 * time.Millisecond)
			}
		}(uint64(r))
	}
	wg.Add(1)
	go func() { // a catalogue writer: popularity updates on library rows
		defer wg.Done()
		rng := rand.New(rand.NewPCG(99, 1))
		for loadCtx.Err() == nil {
			k := lib[rng.IntN(len(lib))]
			b := time.Now()
			_, err := db.ExecContext(loadCtx, `UPDATE catalog_titles SET popularity=popularity+0.01 WHERE key=?`, int64(k))
			if err != nil {
				var phase string
				db.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='tier_migrate_phase'`).Scan(&phase)
				err = fmt.Errorf("phase %q key %d: %w", phase, k, err)
			}
			if loadCtx.Err() == nil {
				write.add(time.Since(b), err)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}()
	wg.Add(1)
	go func() { // a viewer starts long-tail titles while the job runs
		defer wg.Done()
		rng := rand.New(rand.NewPCG(7, 1))
		for loadCtx.Err() == nil {
			k := tail[rng.IntN(len(tail))]
			b := time.Now()
			now := time.Now().Unix()
			// Only a touch of a title that still has its row counts: one
			// the job already removed would go through Get first.
			tx, err := db.BeginTx(loadCtx, nil)
			var had int
			if err == nil {
				err = tx.QueryRowContext(loadCtx, `SELECT COUNT(*) FROM catalog_titles WHERE key=?`, int64(k)).Scan(&had)
			}
			if err == nil && had == 1 {
				_, err = tx.ExecContext(loadCtx, `INSERT INTO state_watch(user_id,rating_key,view_offset_ns,duration_ns,view_count,last_viewed_at,updated_at,watch_at) VALUES(?,?,?,?,0,?,?,?) ON CONFLICT DO NOTHING`, owner, int64(k), int64(time.Minute), int64(time.Hour), now, now, now)
			}
			if err == nil {
				err = tx.Commit()
			} else if tx != nil {
				tx.Rollback()
			}
			if loadCtx.Err() == nil {
				touch.add(time.Since(b), err)
				if err == nil && had == 1 {
					touchedMu.Lock()
					touched = append(touched, k)
					touchedMu.Unlock()
				}
			}
			time.Sleep(250 * time.Millisecond)
		}
	}()

	// WAL and disk, sampled.
	var peakWAL, walBytes atomic.Int64
	var startLSN string
	if pg {
		db.QueryRowContext(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&startLSN)
	}
	sampleDone := make(chan struct{})
	phases := map[string]string{}
	go func() {
		defer close(sampleDone)
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		last := ""
		for {
			select {
			case <-loadCtx.Done():
				return
			case <-tick.C:
			}
			if !pg {
				if st, err := os.Stat(filepath.Join(run, "ether.db-wal")); err == nil && st.Size() > peakWAL.Load() {
					peakWAL.Store(st.Size())
				}
			}
			s := cat.TierMigrationStatus(ctx)
			var phase string
			db.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='tier_migrate_phase'`).Scan(&phase)
			now := s.State + "/" + phase
			if now != last {
				walNow := int64(0)
				if st, err := os.Stat(filepath.Join(run, "ether.db-wal")); err == nil {
					walNow = st.Size()
				}
				phases[time.Since(began).Round(100*time.Millisecond).String()] = fmt.Sprintf("%s (wal %d MB)", now, walNow>>20)
				last = now
			}
		}
	}()

	jobStart := time.Now()
	if err := cat.MigrateTiers(ctx); err != nil {
		t.Fatal(err)
	}
	report["job"] = time.Since(jobStart).Round(time.Millisecond).String()
	report["backup_gate"] = backupTook.Round(time.Millisecond).String()
	time.Sleep(time.Second)
	stopLoad()
	wg.Wait()
	<-sampleDone
	if pg {
		var lsnBytes int64
		db.QueryRowContext(ctx, `SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), ?::pg_lsn)::bigint`, startLSN).Scan(&lsnBytes)
		walBytes.Store(lsnBytes)
		report["wal_written"] = lsnBytes
	} else {
		report["wal_peak_file"] = peakWAL.Load()
	}
	report["phases"] = phases
	report["status"] = cat.TierMigrationStatus(ctx)
	db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles`).Scan(&titles)
	report["rows_after"] = titles
	report["after"] = sizes(db)
	report["browse"], report["get"], report["write"], report["touch"] = browse.summary(), get.summary(), write.summary(), touch.summary()

	// Every title touched while the job ran kept its row (or was stored
	// again by the sweep).
	lost := 0
	for _, k := range touched {
		var n int
		db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles WHERE key=?`, int64(k)).Scan(&n)
		if n == 0 {
			lost++
		}
	}
	report["touched_during_run"], report["touched_lost"] = len(touched), lost
	raw, _ := json.MarshalIndent(report, "", "  ")
	name := filepath.Join(dir, "report-"+report["engine"].(string)+".json")
	os.WriteFile(name, raw, 0o644)
	t.Logf("%s", raw)
	if lost > 0 {
		t.Errorf("%d titles touched during the run lost their row", lost)
	}
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
