package perfharness

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// SyncBench measures how fast the catalogue's real write paths take
// titles into an existing (large) catalogue when TMDB costs nothing: the
// database-bound ceiling of a sync. The fake TMDB serves a long-tail
// universe of new titles, with ids above IDBase, and four phases run in
// turn:
//
//   - list fill: catalog.Sync stores the universe's discover pages as new
//     browsable summaries;
//   - list refresh: the same sync again, every title's popularity and
//     votes moved by 15%;
//   - detail fill: Get on Details of the new titles (details, seasons and
//     crowd recommendations), eight at a time as WarmDetails' callers do;
//   - detail refresh: RefreshTitle on the same titles, one credit and one
//     keyword changed.
//
// The server's background WAL checkpointer runs throughout.
type SyncBenchConfig struct {
	// Titles is the universe: movies and shows the list phases store.
	Titles int
	// Details is how many of them the detail phases fetch (0 = 2,000).
	Details int
	// IDBase puts the universe's TMDB ids above every id the catalogue
	// already holds (0 = 50,000,000).
	IDBase int64
	Seed   uint64
	// Path is the SQLite file, for WAL size sampling; empty on PostgreSQL.
	Path string
	Log  *slog.Logger
	// Progress, if set, gets a line per phase as it starts.
	Progress func(string)
}

// SyncBenchPhase is one phase's measurement.
type SyncBenchPhase struct {
	Name         string        `json:"name"`
	Titles       int           `json:"titles"`
	Wall         time.Duration `json:"wall"`
	TitlesPerSec float64       `json:"titlesPerSec"`
	// TMDB is time the fake TMDB spent building payloads; DBBound is
	// titles per second with it subtracted.
	TMDB       time.Duration `json:"tmdb"`
	DBBound    float64       `json:"dbBoundTitlesPerSec"`
	DBTime     time.Duration `json:"dbTime"`
	DBPerTitle time.Duration `json:"dbPerTitle"`
	Statements int64         `json:"statements"`
	StmtsTitle float64       `json:"statementsPerTitle"`
	LockWait   time.Duration `json:"lockWait"`
	// WrittenPerTitle is bytes the process wrote to disk per title (WAL
	// and checkpoints; Linux), WALPerTitle PostgreSQL's WAL bytes.
	WrittenPerTitle float64              `json:"writtenPerTitle,omitempty"`
	WALPerTitle     float64              `json:"walPerTitle,omitempty"`
	PeakWAL         int64                `json:"peakWal,omitempty"`
	Gate            store.WriteGateHolds `json:"gate"`
	// Detail operations are counted individually. List sync is one bulk call
	// and does not expose per-title outcomes, so its Operations is absent.
	Operations *SyncBenchOperations `json:"operations,omitempty"`
	Error      string               `json:"error,omitempty"`
}

// RunSyncBench runs the four phases against db, which holds a catalogue
// (typically a large fixture clone).
func RunSyncBench(ctx context.Context, db *sql.DB, cfg SyncBenchConfig) ([]SyncBenchPhase, error) {
	if cfg.Titles <= 0 {
		cfg.Titles = 20_000
	}
	if cfg.Details <= 0 {
		cfg.Details = 2_000
	}
	if cfg.IDBase <= 0 {
		cfg.IDBase = 50_000_000
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	ucfg := LongTailConfig{Titles: cfg.Titles, MovieShare: 0.84, Seed: cfg.Seed, Now: now}
	u := NewLongTailTMDB(ucfg)
	u.base = cfg.IDBase
	catCfg := config.Catalog{Language: "en-US", Region: "US", RefreshInterval: config.Duration(24 * time.Hour)}
	c, err := catalog.New(db, u, catCfg, log, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		return nil, err
	}
	cpCtx, stopCheckpointer := context.WithCancel(ctx)
	defer stopCheckpointer()
	go store.RunCheckpointer(cpCtx, db, func() bool { return true }, nil)

	pg := store.DialectOf(db) == store.Postgres
	measure := func(name string, titles int, tmdb *LongTailTMDB, run func(context.Context) (*SyncBenchOperations, error)) (SyncBenchPhase, error) {
		if cfg.Progress != nil {
			cfg.Progress(name)
		}
		if !pg {
			_ = store.Checkpoint(ctx, db, true)
		}
		p := SyncBenchPhase{Name: name, Titles: titles}
		var peak atomic.Int64
		sample, stopSample := context.WithCancel(ctx)
		var sampled sync.WaitGroup
		if cfg.Path != "" {
			sampled.Add(1)
			go func() {
				defer sampled.Done()
				t := time.NewTicker(200 * time.Millisecond)
				defer t.Stop()
				for {
					if st, err := os.Stat(cfg.Path + "-wal"); err == nil && st.Size() > peak.Load() {
						peak.Store(st.Size())
					}
					select {
					case <-sample.Done():
						return
					case <-t.C:
					}
				}
			}()
		}
		var lsn0 string
		if pg {
			_ = db.QueryRowContext(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&lsn0)
		}
		written0 := processBytesWritten()
		store.ResetWriteGateHolds()
		tmdb0 := tmdb.fetchNanos.Load()
		qctx, stats := store.WithQueryStats(ctx)
		start := time.Now()
		operations, err := run(qctx)
		p.Operations = operations
		c.WaitRefreshes()
		p.Wall = time.Since(start)
		stopSample()
		sampled.Wait()
		p.TMDB = time.Duration(tmdb.fetchNanos.Load() - tmdb0)
		completed := titles
		if operations != nil {
			completed = operations.Succeeded
		} else if err != nil {
			completed = 0 // A failed bulk sync does not expose its success count.
		}
		p.TitlesPerSec = float64(completed) / p.Wall.Seconds()
		if bound := p.Wall - p.TMDB; bound > 0 {
			p.DBBound = float64(completed) / bound.Seconds()
		}
		p.DBTime, p.Statements, p.LockWait = stats.DBTime(), stats.Queries(), stats.LockWait()
		p.DBPerTitle = p.DBTime / time.Duration(max(titles, 1))
		p.StmtsTitle = float64(p.Statements) / float64(max(titles, 1))
		if w := processBytesWritten(); w > 0 && written0 >= 0 {
			p.WrittenPerTitle = float64(w-written0) / float64(max(titles, 1))
		}
		if pg {
			var bytes float64
			if db.QueryRowContext(ctx, `SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), ?::pg_lsn)`, lsn0).Scan(&bytes) == nil {
				p.WALPerTitle = bytes / float64(max(titles, 1))
			}
		}
		p.PeakWAL = peak.Load()
		p.Gate = store.ReadWriteGateHolds()
		if err != nil {
			p.Error = err.Error()
			return p, fmt.Errorf("%s: %w", name, err)
		}
		return p, nil
	}

	universe := len(u.movies) + len(u.shows)
	var phases []SyncBenchPhase
	add := func(p SyncBenchPhase, err error) error {
		phases = append(phases, p)
		return err
	}
	syncList := func(ctx context.Context) (*SyncBenchOperations, error) { return nil, c.Sync(ctx) }
	if err := add(measure("list fill", universe, u, syncList)); err != nil {
		return phases, err
	}
	moved := NewLongTailTMDB(ucfg)
	moved.base, moved.jitter = cfg.IDBase, 1
	c.SetTMDB(moved)
	if err := add(measure("list refresh", universe, moved, syncList)); err != nil {
		return phases, err
	}
	c.SetTMDB(u)
	keys := make([]core.RatingKey, 0, cfg.Details)
	for kind, list := range [][]ltEntry{u.movies, u.shows} {
		// Movies and shows in the universe's proportion.
		n := cfg.Details * len(list) / max(universe, 1)
		for i := 0; i < len(list) && n > 0; i++ {
			if !u.date(list[i]).After(now) {
				keys = append(keys, ltKey(kind, int64(i+1)+u.base))
				n--
			}
		}
	}
	if err := add(measure("detail fill", len(keys), u, func(ctx context.Context) (*SyncBenchOperations, error) {
		return runSyncBenchOperations(ctx, len(keys), func(ctx context.Context, i int) error { _, err := c.Get(ctx, keys[i]); return err })
	})); err != nil {
		return phases, err
	}
	refreshed := NewLongTailTMDB(ucfg)
	refreshed.base, refreshed.jitter = cfg.IDBase, 2
	c.SetTMDB(refreshed)
	if err := add(measure("detail refresh", len(keys), refreshed, func(ctx context.Context) (*SyncBenchOperations, error) {
		return runSyncBenchOperations(ctx, len(keys), func(ctx context.Context, i int) error {
			_, err := c.RefreshTitle(ctx, keys[i], true)
			return err
		})
	})); err != nil {
		return phases, err
	}
	return phases, nil
}

// WriteSyncBench renders phases as a table.
func WriteSyncBench(w io.Writer, phases []SyncBenchPhase) {
	fmt.Fprintln(w, "| phase | titles | titles/s | DB-bound titles/s | DB ms/title | stmts/title | written KB/title | PG WAL KB/title | peak WAL MB | gate holds (<1/5/20/100/1000ms/more) | max hold | lock wait |")
	fmt.Fprintln(w, "|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|")
	for _, p := range phases {
		fmt.Fprintf(w, "| %s | %d | %.0f | %.0f | %.2f | %.1f | %.1f | %.1f | %.0f | %d %v | %s | %s |\n",
			p.Name, p.Titles, p.TitlesPerSec, p.DBBound, float64(p.DBPerTitle.Microseconds())/1000, p.StmtsTitle,
			p.WrittenPerTitle/1024, p.WALPerTitle/1024, float64(p.PeakWAL)/1e6, p.Gate.Holds, p.Gate.Buckets, p.Gate.Max.Round(time.Millisecond), p.LockWait.Round(time.Millisecond))
		if p.Operations != nil {
			fmt.Fprintf(w, "%s operations: attempted=%d succeeded=%d rejected=%d\n", p.Name, p.Operations.Attempted, p.Operations.Succeeded, p.Operations.Rejected)
		}
		if p.Error != "" {
			fmt.Fprintf(w, "%s error: %s\n", p.Name, p.Error)
		}
	}
}
