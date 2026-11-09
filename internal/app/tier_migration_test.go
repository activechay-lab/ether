package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// tierTMDB answers detail fetches for any id, counting them: opening a
// title the tier migration removed stores it again through Get.
type tierTMDB struct {
	searchDown
	fetches atomic.Int32
}

func (f *tierTMDB) MovieDetails(_ context.Context, id int64) (*tmdb.MovieDetails, error) {
	f.fetches.Add(1)
	return &tmdb.MovieDetails{Movie: tmdb.Movie{ID: id, Title: fmt.Sprintf("Film %d", id), ReleaseDate: "2001-02-03", Popularity: 0.4, VoteCount: 2}}, nil
}

func (f *tierTMDB) TVDetails(_ context.Context, id int64) (*tmdb.TVDetails, error) {
	f.fetches.Add(1)
	return &tmdb.TVDetails{TVShow: tmdb.TVShow{ID: id, Name: fmt.Sprintf("Show %d", id), FirstAirDate: "2001-02-03"}}, nil
}

// TestTierMigrationKeepsEveryTouchedTitle is the tier migration's lasting
// invariant: after it, every title a touch source names (and a kept show's
// seasons and episodes) still has its row, every row the bars, detail or a
// franchise keep is still there, everything else is gone, the schema,
// search index and tallies are as before, and a removed title's old key
// (a frontend compact id, a stale client key) opens again through Get.
// Every table in the schema must be classified (a touch source, or
// declared untouched) for the test to pass.
func TestTierMigrationKeepsEveryTouchedTitle(t *testing.T) {
	ctx := context.Background()
	var db *sql.DB
	var err error
	if os.Getenv(store.TestPostgresEnv) != "" {
		db = store.OpenTest(t)
	} else {
		db, err = store.OpenPath(ctx, filepath.Join(t.TempDir(), "ether.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { db.Close() })
	}
	pg := store.DialectOf(db) == store.Postgres
	if missing, err := catalog.TierUnclassifiedTables(ctx, db); err != nil {
		t.Fatal(err)
	} else if len(missing) > 0 {
		t.Fatalf("tables neither a tier touch source nor declared untouched (classify them in catalog/tier_sources.go): %v", missing)
	}
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	fake := &tierTMDB{}
	cat, err := catalog.New(db, fake, config.Catalog{Language: "en-US", Region: "US"}, slog.New(slog.DiscardHandler), catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	// The library: 3,000 movies and 400 shows (two seasons of three
	// episodes each), most of them long tail as an Off mirror stored them.
	released := time.Date(2001, 2, 3, 0, 0, 0, 0, time.UTC)
	type want int
	const (
		gone want = iota
		bars
		detail
		franchise
		touch
	)
	fate := map[core.RatingKey]want{}
	var titles []core.Title
	for i := int64(1); i <= 3000; i++ {
		m := core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, IDs: core.IDs{TMDB: i}, Title: fmt.Sprintf("Film %d", i), Released: released, Popularity: 0.5, VoteCount: 1, Genres: []string{"Drama"}}
		switch {
		case i%10 == 0:
			m.Popularity, fate[m.Key] = 40, bars
		case i%10 == 1:
			m.VoteCount, fate[m.Key] = 300, bars
		default:
			fate[m.Key] = gone
		}
		titles = append(titles, m)
	}
	for i := int64(1); i <= 400; i++ {
		s := core.Title{Key: core.ShowKey(i), Type: core.TypeShow, IDs: core.IDs{TMDB: i}, Title: fmt.Sprintf("Series %d", i), Released: released, Popularity: 0.5, VoteCount: 1, Genres: []string{"Comedy"}}
		if i%10 == 0 {
			s.Popularity, fate[s.Key] = 40, bars
		} else {
			fate[s.Key] = gone
		}
		titles = append(titles, s)
	}
	for _, m := range titles {
		if err := cat.UpsertTitle(ctx, m, true); err != nil {
			t.Fatal(err)
		}
	}
	var episodes []core.Title
	for i := int64(1); i <= 400; i++ {
		for season := 1; season <= 2; season++ {
			episodes = append(episodes, core.Title{Key: core.SeasonKey(i, season), Type: core.TypeSeason, ShowKey: core.ShowKey(i), Season: season, Title: fmt.Sprintf("Season %d", season), Released: released})
			for ep := 1; ep <= 3; ep++ {
				episodes = append(episodes, core.Title{Key: core.EpisodeKey(i, season, ep), Type: core.TypeEpisode, ShowKey: core.ShowKey(i), Season: season, Episode: ep, Title: fmt.Sprintf("Episode %d", ep), Released: released.AddDate(0, 0, ep)})
			}
		}
	}
	for _, e := range episodes {
		if err := cat.UpsertTitle(ctx, e, false); err != nil {
			t.Fatal(err)
		}
	}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	// Opened titles and franchise parts stay on their own facts.
	for i := int64(2); i < 3000; i += 97 {
		exec(`UPDATE catalog_titles SET detail=1 WHERE key=?`, int64(core.MovieKey(i)))
		fate[core.MovieKey(i)] = detail
	}
	for i := int64(3); i < 3000; i += 89 {
		if fate[core.MovieKey(i)] == gone {
			exec(`UPDATE catalog_titles SET collection_id=? WHERE key=?`, 9000+i, int64(core.MovieKey(i)))
			fate[core.MovieKey(i)] = franchise
		}
	}

	// One touched long-tail title per touch source, named the way that
	// source names titles.
	nextMovie, nextShow := int64(4), int64(1)
	pickMovie := func() core.RatingKey {
		for ; ; nextMovie++ {
			if k := core.MovieKey(nextMovie); fate[k] == gone {
				nextMovie++
				return k
			}
		}
	}
	pickShow := func() core.RatingKey {
		for ; ; nextShow++ {
			if k := core.ShowKey(nextShow); fate[k] == gone {
				nextShow++
				return k
			}
		}
	}
	seed := newTierSeeder(t, db)
	for _, src := range catalog.TierSources() {
		col := strings.ToLower(src.Column)
		var key core.RatingKey
		var value any
		switch {
		case strings.Contains(col, "episode"):
			key = pickShow()
			value = int64(core.EpisodeKey(key.TMDB(), 2, 3))
		case strings.Contains(col, "show"):
			key = pickShow()
			value = int64(key)
			if src.BareID {
				value = key.TMDB()
			}
		default:
			key = pickMovie()
			value = int64(key)
		}
		switch {
		case src.Scan:
			value = fmt.Sprintf(`{"items":[{"ratingKey":"%d","title":"x"}]}`, value)
		case src.Text || src.BareID && strings.HasPrefix(col, "scope"):
			value = strconv.FormatInt(value.(int64), 10)
		}
		seed.row(src.Table, src.Column, value, src.Where)
		fate[key] = touch
	}
	seed.close()

	// Removed titles a client still holds a compact id for.
	var stale []core.RatingKey
	for i := int64(5); len(stale) < 3; i++ {
		if k := core.MovieKey(i); fate[k] == gone {
			stale = append(stale, k)
			exec(`INSERT INTO frontend_compact_ids(surface,kind,ref,id) VALUES('xtream','item',?,?)`, strconv.FormatInt(int64(k), 10), len(stale))
		}
	}

	beforeIndexes := schemaObjects(t, db, "index")
	beforeTriggers := schemaObjects(t, db, "trigger")
	exec(`INSERT INTO catalog_meta(k,v) VALUES('tier_migrate_armed',?)`, strconv.FormatInt(now.Add(-time.Minute).Unix(), 10))
	var backupSince time.Time
	cat.SetTierBackup(func(_ context.Context, since time.Time) (string, error) {
		backupSince = since
		return "ether-test.tar.gz", nil
	})
	if err := cat.MigrateTiers(ctx); err != nil {
		t.Fatal(err)
	}
	if backupSince.IsZero() {
		t.Fatal("removed titles without asking for a backup")
	}
	status := cat.TierMigrationStatus(ctx)
	if status.State != "done" || !cat.TierMigrationDone(ctx) || status.Removed == 0 {
		t.Fatalf("status %+v", status)
	}
	t.Logf("tier migration: %+v; %d indexes, %d triggers compared", status, len(beforeIndexes), len(beforeTriggers))

	stored := map[core.RatingKey]bool{}
	rows, err := db.QueryContext(ctx, `SELECT key FROM catalog_titles`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		stored[core.RatingKey(k)] = true
	}
	rows.Close()
	for k, f := range fate {
		if (f == gone) == stored[k] {
			t.Errorf("%d (fate %d): stored=%v", k, f, stored[k])
		}
		if k.Kind() == core.KindShow && f != gone {
			for season := 1; season <= 2; season++ {
				if !stored[core.SeasonKey(k.TMDB(), season)] || !stored[core.EpisodeKey(k.TMDB(), season, 3)] {
					t.Errorf("kept show %d lost season %d or its episodes", k, season)
				}
			}
		}
	}
	touched, err := cat.TierTouchedKeys(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range touched {
		if !stored[k] {
			t.Errorf("touched title %d has no row", k)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	// The schema is what it was, the search index and tallies agree with
	// the rows, and nothing of the job is left behind.
	if after := schemaObjects(t, db, "index"); !slices.Equal(beforeIndexes, after) {
		t.Errorf("indexes changed:\nbefore %v\nafter  %v", beforeIndexes, after)
	}
	if after := schemaObjects(t, db, "trigger"); !slices.Equal(beforeTriggers, after) {
		t.Errorf("triggers changed:\nbefore %v\nafter  %v", beforeTriggers, after)
	}
	if !pg {
		var ok string
		if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&ok); err != nil || ok != "ok" {
			t.Fatalf("integrity_check %q %v", ok, err)
		}
		exec(`INSERT INTO catalog_titles_fts(catalog_titles_fts, rank) VALUES('integrity-check', 1)`)
		var hits []int64
		r, err := db.QueryContext(ctx, `SELECT rowid FROM catalog_titles_fts WHERE title LIKE ? ORDER BY rowid`, `%Film 10%`)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			var k int64
			r.Scan(&k)
			hits = append(hits, k)
		}
		r.Close()
		for _, k := range hits {
			if !stored[core.RatingKey(k)] {
				t.Errorf("search index still holds removed title %d", k)
			}
		}
		if !slices.Contains(hits, int64(core.MovieKey(10))) {
			t.Errorf("search index lost kept Film 10: %v", hits)
		}
	}
	stats, err := cat.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var movies, browsable int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(browsable),0) FROM catalog_titles WHERE type='movie'`).Scan(&movies, &browsable); err != nil {
		t.Fatal(err)
	}
	if stats.Movies != movies || stats.BrowsableMovies != browsable {
		t.Errorf("tallies say %d movies (%d browsable), rows %d (%d)", stats.Movies, stats.BrowsableMovies, movies, browsable)
	}
	var orphans int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_genres g WHERE NOT EXISTS (SELECT 1 FROM catalog_titles t WHERE t.key=g.key)`).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("%d genre rows of removed titles left", orphans)
	}

	// A client's old key for a removed title opens it again: Get stores
	// it (one TMDB round trip), and the compact id still names it.
	for i, k := range stale {
		var ref string
		if err := db.QueryRowContext(ctx, `SELECT ref FROM frontend_compact_ids WHERE surface='xtream' AND kind='item' AND id=?`, i+1).Scan(&ref); err != nil || ref != strconv.FormatInt(int64(k), 10) {
			t.Fatalf("compact id %d: %q %v", i+1, ref, err)
		}
		before := fake.fetches.Load()
		got, err := cat.Get(ctx, k)
		if err != nil || got == nil || got.Key != k {
			t.Fatalf("Get(%d) after migration: %+v %v", k, got, err)
		}
		if fake.fetches.Load() == before {
			t.Fatalf("Get(%d) answered without fetching the removed title", k)
		}
		var n int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles WHERE key=? AND detail=1`, int64(k)).Scan(&n); err != nil || n != 1 {
			t.Fatalf("Get(%d) did not store the title again: %d %v", k, n, err)
		}
	}

	// Every export id is findable: a stored row, or the long-tail index
	// (which holds every export id, so nothing removed is lost to search).
	var movieExport, tvExport bytes.Buffer
	for _, w := range []struct {
		buf  *bytes.Buffer
		kind core.KeyKind
		n    int64
	}{{&movieExport, core.KindMovie, 3000}, {&tvExport, core.KindShow, 400}} {
		zw := gzip.NewWriter(w.buf)
		for i := int64(1); i <= w.n; i++ {
			name := "original_title"
			if w.kind == core.KindShow {
				name = "original_name"
			}
			fmt.Fprintf(zw, `{"adult":false,"id":%d,"%s":"Title %d","popularity":0.5,"video":false}`+"\n", i, name, i)
		}
		zw.Close()
	}
	if err := cat.UseTailExports(&movieExport, &tvExport); err != nil {
		t.Fatal(err)
	}
	for i := int64(1); i <= 3000; i++ {
		if ok, err := cat.Findable(ctx, core.MovieKey(i)); err != nil || !ok {
			t.Fatalf("export movie %d not findable after migration: %v", i, err)
		}
	}
	for i := int64(1); i <= 400; i++ {
		if ok, err := cat.Findable(ctx, core.ShowKey(i)); err != nil || !ok {
			t.Fatalf("export show %d not findable after migration: %v", i, err)
		}
	}
}

// schemaObjects lists the catalogue's indexes or triggers with their
// definitions, so a swap that lost or renamed one shows.
func schemaObjects(t *testing.T, db *sql.DB, typ string) []string {
	t.Helper()
	var q string
	if store.DialectOf(db) == store.Postgres {
		if typ == "index" {
			q = `SELECT indexname || ' ' || replace(indexdef, ' ONLY ', ' ') FROM pg_indexes WHERE schemaname=current_schema() AND tablename LIKE 'catalog\_%' ORDER BY 1`
		} else {
			q = `SELECT tgname || ' ' || pg_get_triggerdef(t.oid) FROM pg_trigger t JOIN pg_class c ON c.oid=t.tgrelid WHERE NOT t.tgisinternal AND c.relname LIKE 'catalog\_%' AND c.relnamespace=current_schema()::regnamespace ORDER BY 1`
		}
	} else {
		q = `SELECT name || ' ' || COALESCE(sql,'') FROM sqlite_master WHERE type='` + typ + `' AND tbl_name LIKE 'catalog\_%' ESCAPE '\' ORDER BY name`
	}
	rows, err := db.Query(q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

// tierSeeder inserts one row into any table, setting one column and giving
// every other required column a unique placeholder.
type tierSeeder struct {
	t    *testing.T
	db   *sql.DB
	conn *sql.Conn
	n    int
}

// newTierSeeder seeds through one connection with foreign keys off: a
// source row's parent (a playlist for its items) is not what is tested.
func newTierSeeder(t *testing.T, db *sql.DB) *tierSeeder {
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	off := `PRAGMA foreign_keys=OFF`
	if store.DialectOf(db) == store.Postgres {
		off = `SET session_replication_role = replica`
	}
	if _, err := conn.ExecContext(context.Background(), off); err != nil {
		t.Fatal(err)
	}
	return &tierSeeder{t: t, db: db, conn: conn}
}

func (s *tierSeeder) close() {
	on := `PRAGMA foreign_keys=ON`
	if store.DialectOf(s.db) == store.Postgres {
		on = `SET session_replication_role = DEFAULT`
	}
	s.conn.ExecContext(context.Background(), on)
	s.conn.Close()
}

type seedColumn struct {
	name, typ string
	required  bool
}

func (s *tierSeeder) columns(table string) []seedColumn {
	s.t.Helper()
	var q string
	if store.DialectOf(s.db) == store.Postgres {
		q = `SELECT column_name, data_type, (is_nullable='NO' AND column_default IS NULL AND is_generated='NEVER' AND is_identity='NO') FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=? ORDER BY ordinal_position`
	} else {
		q = `SELECT name, type, ("notnull"=1 AND dflt_value IS NULL AND hidden=0) FROM pragma_table_xinfo(?) ORDER BY cid`
	}
	rows, err := s.conn.QueryContext(context.Background(), q, table)
	if err != nil {
		s.t.Fatal(err)
	}
	defer rows.Close()
	var out []seedColumn
	for rows.Next() {
		var c seedColumn
		if err := rows.Scan(&c.name, &c.typ, &c.required); err != nil {
			s.t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

// seedFixed satisfies CHECK constraints the placeholder rule cannot.
var seedFixed = map[string]map[string]any{
	"sessions_source_offers": {"namespace": "sessions.source-offer", "kind": "detail", "revision": "r1"},
}

var seedCheckRE = regexp.MustCompile(`CHECK constraint failed: ([a-z_]+)\s*(?:=|IN\s*\()\s*'([^']*)'`)

func (s *tierSeeder) row(table, column string, value any, where string) {
	s.t.Helper()
	cols := s.columns(table)
	if len(cols) == 0 {
		return // the table is not in this build
	}
	fixed := map[string]any{}
	for k, v := range seedFixed[table] {
		fixed[k] = v
	}
	if where != "" {
		name, v, _ := strings.Cut(where, "=")
		n, _ := strconv.ParseInt(v, 10, 64)
		fixed[name] = n
	}
	for attempt := 0; ; attempt++ {
		var names []string
		var args []any
		for _, c := range cols {
			switch v, ok := fixed[c.name]; {
			case strings.EqualFold(c.name, column):
				args = append(args, value)
			case ok:
				args = append(args, v)
			case c.required:
				s.n++
				if t := strings.ToUpper(c.typ); strings.Contains(t, "INT") || strings.Contains(t, "REAL") || strings.Contains(t, "NUMERIC") || strings.Contains(t, "DOUBLE") {
					args = append(args, int64(s.n))
				} else if strings.Contains(t, "BLOB") || t == "BYTEA" {
					args = append(args, []byte(fmt.Sprintf("x%d", s.n)))
				} else {
					args = append(args, fmt.Sprintf("x%d", s.n))
				}
			default:
				continue
			}
			names = append(names, c.name)
		}
		q := `INSERT INTO ` + table + `(` + strings.Join(names, ",") + `) VALUES (` + store.Placeholders(len(names)) + `)`
		err := s.withoutTriggers(table, func() error {
			_, err := s.conn.ExecContext(context.Background(), q, args...)
			return err
		})
		if err == nil {
			return
		}
		// A CHECK that pins a column to one value: use it.
		if m := seedCheckRE.FindStringSubmatch(err.Error()); m != nil && attempt < 8 {
			fixed[m[1]] = m[2]
			continue
		}
		s.t.Fatalf("seed %s.%s: %v", table, column, err)
	}
}

// withoutTriggers runs fn with table's triggers set aside (an admission
// trigger guards some stores; the seed only needs the row). PostgreSQL's
// replica role already skips them.
func (s *tierSeeder) withoutTriggers(table string, fn func() error) error {
	if store.DialectOf(s.db) == store.Postgres {
		return fn()
	}
	ctx := context.Background()
	rows, err := s.conn.QueryContext(ctx, `SELECT name, sql FROM sqlite_master WHERE type='trigger' AND tbl_name=?`, table)
	if err != nil {
		return err
	}
	var names, defs []string
	for rows.Next() {
		var n, d string
		if err := rows.Scan(&n, &d); err != nil {
			rows.Close()
			return err
		}
		names, defs = append(names, n), append(defs, d)
	}
	rows.Close()
	for _, n := range names {
		if _, err := s.conn.ExecContext(ctx, `DROP TRIGGER "`+n+`"`); err != nil {
			return err
		}
	}
	err = fn()
	for _, d := range defs {
		if _, e := s.conn.ExecContext(ctx, d); e != nil && err == nil {
			err = e
		}
	}
	return err
}
