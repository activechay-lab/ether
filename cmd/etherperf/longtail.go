package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/braedonsaunders/ether/internal/perfharness"
	"github.com/braedonsaunders/ether/internal/store"
)

// runLongTail mirrors a synthetic long-tail TMDB into a fresh database
// through the real catalogue sync and detail paths.
//
//	etherperf longtail -out /tmp/lt/ether.db -titles 800000
//	etherperf longtail -pg postgres://… -titles 100000
func runLongTail(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("longtail", flag.ExitOnError)
	out := f.String("out", "", "SQLite database file to create")
	pgURL := f.String("pg", "", "empty PostgreSQL database to fill instead")
	titles := f.Int("titles", 800_000, "movies plus shows in the TMDB universe")
	share := f.Float64("movie-share", 0.7, "movies' share of the titles")
	seed := f.Uint64("seed", 1, "universe seed")
	hide := f.Bool("hide-obscure", false, "keep the default browsable bar (popularity 5 or 100 votes); off mirrors everything")
	openShows := f.Int("open-shows", 0, "most popular shows whose seasons a household opened (0 = 300, -1 = none)")
	limit := f.Int("detail-limit", 0, "stop after this many detail fetches (0 = all)")
	report := f.String("report", "", "write the generation report and sizes as JSON here")
	countsFile := f.String("tmdb-counts", "", "per-year TMDB discover totals (JSON); sizes the universe as a real sync would mirror it, replacing -titles")
	skipSync := f.Bool("skip-sync", false, "continue an existing -out database: skip the sync and detail what is left")
	uncapped := f.Bool("uncapped", false, "with -tmdb-counts: the whole of TMDB, not the 10,000 per year a sync reaches")
	_ = f.Parse(args)
	if (*out == "") == (*pgURL == "") {
		return fmt.Errorf("exactly one of -out or -pg is required")
	}
	var yearCounts [2]map[int]int
	if *countsFile != "" {
		raw, err := os.ReadFile(*countsFile)
		if err != nil {
			return err
		}
		if yearCounts, err = perfharness.LoadTMDBYearCounts(raw, !*uncapped); err != nil {
			return err
		}
	}
	var db *sql.DB
	var err error
	if *pgURL != "" {
		db, _, err = store.OpenWith(ctx, store.Options{URL: *pgURL})
	} else {
		if _, statErr := os.Stat(*out); statErr == nil && !*skipSync {
			return fmt.Errorf("%s exists; choose a fresh -out", *out)
		}
		if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
			return err
		}
		db, err = store.OpenPath(ctx, *out)
	}
	if err != nil {
		return err
	}
	defer db.Close()
	started := time.Now()
	logf := func(format string, a ...any) {
		fmt.Fprintf(os.Stderr, "etherperf longtail %s: "+format+"\n", append([]any{time.Since(started).Round(time.Second)}, a...)...)
	}
	last := map[string]time.Time{}
	rep, err := perfharness.GenerateLongTail(ctx, db, perfharness.LongTailConfig{
		Titles: *titles, MovieShare: *share, Seed: *seed, HideObscure: *hide, YearCounts: yearCounts, SkipSync: *skipSync,
		OpenShows: *openShows, DetailLimit: *limit,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})),
		Progress: func(phase string, n int) {
			if time.Since(last[phase]) > 30*time.Second {
				last[phase] = time.Now()
				var pages, pageSize int64
				if *pgURL == "" {
					_ = db.QueryRowContext(ctx, `PRAGMA page_count`).Scan(&pages)
					_ = db.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize)
				}
				logf("%s %d (database %.0f MB)", phase, n, float64(pages*pageSize)/1e6)
			}
		},
	})
	if err != nil {
		return err
	}
	logf("done: universe %d (%d meet the default bar), sync %s, %d past the year caps filled in %s, %d details in %s, %d seasons opened",
		rep.Universe, rep.Browsable, rep.Synced.Round(time.Second), rep.Filled, rep.FillTook.Round(time.Second), rep.Warmed, rep.WarmTook.Round(time.Second), rep.SeasonsOpened)
	sizes, err := perfharness.MeasureSizes(ctx, db, true)
	if err != nil {
		return err
	}
	var detailed int64
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles WHERE detail=1 AND type IN ('movie','show')`).Scan(&detailed)
	perfharness.WriteSizeReport(os.Stdout, sizes, detailed, 1<<20)
	if *report != "" {
		b, err := json.MarshalIndent(map[string]any{"generation": rep, "detailedTitles": detailed, "sizes": sizes}, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(*report, b, 0o644)
	}
	return nil
}

// runSizes reports a database's per-table and per-index sizes.
func runSizes(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("sizes", flag.ExitOnError)
	path := f.String("db", "", "SQLite database file (opened read-only)")
	pgURL := f.String("pg", "", "PostgreSQL database URL")
	rows := f.Bool("rows", true, "count each table's rows")
	min := f.Int64("min", 1<<20, "omit tables smaller than this many bytes")
	asJSON := f.Bool("json", false, "print JSON instead of a table")
	_ = f.Parse(args)
	var db *sql.DB
	var err error
	switch {
	case *pgURL != "":
		db, err = sql.Open(store.PostgresDriverName, *pgURL)
	case *path != "":
		db, err = sql.Open("sqlite", "file:"+*path+"?mode=ro")
	default:
		return fmt.Errorf("-db or -pg is required")
	}
	if err != nil {
		return err
	}
	defer db.Close()
	sizes, err := perfharness.MeasureSizes(ctx, db, *rows)
	if err != nil {
		return err
	}
	if *asJSON {
		return json.NewEncoder(os.Stdout).Encode(sizes)
	}
	var detailed int64
	_ = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles WHERE detail=1 AND type IN ('movie','show')`).Scan(&detailed)
	fmt.Printf("detailed movies and shows: %d\n\n", detailed)
	perfharness.WriteSizeReport(os.Stdout, sizes, detailed, *min)
	return nil
}

// runPGCopy copies a SQLite database into an empty PostgreSQL database the
// way ether cluster init does (a logical dump, triggers suspended while it
// loads), so one generated catalogue can be measured on both engines.
func runPGCopy(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("pgcopy", flag.ExitOnError)
	path := f.String("db", "", "SQLite database file")
	pgURL := f.String("pg", "", "empty PostgreSQL database URL")
	_ = f.Parse(args)
	if *path == "" || *pgURL == "" {
		return fmt.Errorf("-db and -pg are required")
	}
	src, err := store.OpenPath(ctx, *path)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, _, err := store.OpenWith(ctx, store.Options{URL: *pgURL})
	if err != nil {
		return err
	}
	defer dst.Close()
	started := time.Now()
	r, w := io.Pipe()
	go func() { w.CloseWithError(store.ExportLogical(ctx, src, w)) }()
	if err := store.ImportLogical(ctx, dst, r); err != nil {
		return err
	}
	if _, err := dst.ExecContext(ctx, `ANALYZE`); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "etherperf pgcopy: copied in %s\n", time.Since(started).Round(time.Second))
	return nil
}

// runSyncBench measures the catalogue's database-bound sync and detail
// rates on a copy of a catalogue: see perfharness.RunSyncBench.
//
//	etherperf syncbench -db /ssd/clone.db
//	etherperf syncbench -pg postgres://…
func runSyncBench(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("syncbench", flag.ExitOnError)
	path := f.String("db", "", "SQLite catalogue to write into (a clone: it is modified)")
	pgURL := f.String("pg", "", "PostgreSQL catalogue to write into instead (modified)")
	titles := f.Int("titles", 20_000, "new titles the list phases store")
	details := f.Int("details", 2_000, "titles the detail phases fetch")
	base := f.Int64("id-base", 50_000_000, "TMDB ids of the new titles start above this")
	seed := f.Uint64("seed", 7, "universe seed")
	report := f.String("report", "", "write the phases as JSON here")
	_ = f.Parse(args)
	if (*path == "") == (*pgURL == "") {
		return fmt.Errorf("exactly one of -db or -pg is required")
	}
	var db *sql.DB
	var err error
	if *pgURL != "" {
		db, _, err = store.OpenWith(ctx, store.Options{URL: *pgURL})
	} else {
		db, err = store.OpenPath(ctx, *path)
	}
	if err != nil {
		return err
	}
	defer db.Close()
	started := time.Now()
	phases, err := perfharness.RunSyncBench(ctx, db, perfharness.SyncBenchConfig{
		Titles: *titles, Details: *details, IDBase: *base, Seed: *seed, Path: *path,
		Log: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		Progress: func(p string) {
			fmt.Fprintf(os.Stderr, "etherperf syncbench %s: %s\n", time.Since(started).Round(time.Second), p)
		},
	})
	perfharness.WriteSyncBench(os.Stdout, phases)
	if err != nil {
		return err
	}
	if *report != "" {
		b, err := json.MarshalIndent(phases, "", "  ")
		if err != nil {
			return err
		}
		return os.WriteFile(*report, b, 0o644)
	}
	return nil
}

// runTailBench measures compact long-tail layouts built from TMDB's daily
// id exports: see perfharness.RunTailBench.
func runTailBench(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("tailbench", flag.ExitOnError)
	movies := f.String("movies", "", "movie_ids_MM_DD_YYYY.json.gz")
	shows := f.String("shows", "", "tv_series_ids_MM_DD_YYYY.json.gz")
	dir := f.String("out", "", "directory for the SQLite layouts")
	pgURL := f.String("pg", "", "also measure PostgreSQL layouts in this (scratch) database")
	_ = f.Parse(args)
	if *movies == "" || *shows == "" || *dir == "" {
		return fmt.Errorf("-movies, -shows and -out are required")
	}
	if err := os.MkdirAll(*dir, 0o755); err != nil {
		return err
	}
	rows, err := perfharness.LoadTailExports(*movies, *shows)
	if err != nil {
		return err
	}
	vs, err := perfharness.RunTailBench(ctx, rows, *dir, *pgURL)
	perfharness.WriteTailBench(os.Stdout, len(rows), vs)
	return err
}

// runRankBench measures the popularity rank on a catalogue copy: see
// perfharness.RunRankBench.
func runRankBench(ctx context.Context, args []string) error {
	f := flag.NewFlagSet("rankbench", flag.ExitOnError)
	path := f.String("db", "", "SQLite catalogue copy (modified)")
	pgURL := f.String("pg", "", "PostgreSQL catalogue copy instead (modified)")
	_ = f.Parse(args)
	if (*path == "") == (*pgURL == "") {
		return fmt.Errorf("exactly one of -db or -pg is required")
	}
	var db *sql.DB
	var err error
	if *pgURL != "" {
		db, _, err = store.OpenWith(ctx, store.Options{URL: *pgURL})
	} else {
		db, err = store.OpenPath(ctx, *path)
	}
	if err != nil {
		return err
	}
	defer db.Close()
	steps, pages, err := perfharness.RunRankBench(ctx, db, *path)
	perfharness.WriteRankBench(os.Stdout, steps, pages)
	return err
}
