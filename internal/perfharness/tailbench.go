package perfharness

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// The tail bench measures a compact long-tail tier: every TMDB title
// outside the library as a reference row (key, title, original title,
// year, poster) and a name index, built from TMDB's daily id exports.

// TailRow is one compact long-tail title.
type TailRow struct {
	Key           int64
	Title         string
	OriginalTitle string
	Year          int
	Poster        string
	Popularity    float64
}

// LoadTailExports reads TMDB daily id exports (gzip JSON lines) for
// movies and shows, skipping adult titles. Exports carry the original
// title and popularity only; title, year and poster are drawn the way a
// /discover row would carry them (about 60% of originals are also the
// title, posters on 60% of titles), seeded by id.
func LoadTailExports(movies, shows string) ([]TailRow, error) {
	var out []TailRow
	for kind, path := range []string{movies, shows} {
		f, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		sc := bufio.NewScanner(gz)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		vocab := ltVocab(1)
		for sc.Scan() {
			var r struct {
				ID       int64   `json:"id"`
				Original string  `json:"original_title"`
				Name     string  `json:"original_name"`
				Pop      float64 `json:"popularity"`
				Adult    bool    `json:"adult"`
			}
			if json.Unmarshal(sc.Bytes(), &r) != nil || r.Adult || r.ID <= 0 {
				continue
			}
			rng := rand.New(rand.NewPCG(uint64(r.ID), uint64(kind)))
			row := TailRow{Popularity: r.Pop, OriginalTitle: r.Original}
			if kind == 0 {
				row.Key = int64(core.MovieKey(r.ID))
			} else {
				row.Key, row.OriginalTitle = int64(core.ShowKey(r.ID)), r.Name
			}
			row.Title = row.OriginalTitle
			if !isASCIIText(row.Title) || rng.Float64() < 0.4 {
				var b strings.Builder
				for i := range 1 + rng.IntN(3) {
					if i > 0 {
						b.WriteByte(' ')
					}
					w := vocab[rng.IntN(len(vocab))]
					b.WriteString(strings.ToUpper(w[:1]) + w[1:])
				}
				row.Title = b.String()
			}
			row.Year = 1930 + rng.IntN(97)
			if rng.Float64() < 0.6 {
				row.Poster = ltPath(rng, ".jpg")
			}
			out = append(out, row)
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func isASCIIText(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func ltVocab(seed uint64) []string {
	vr := rand.New(rand.NewPCG(seed, 0x766f6361))
	seen := map[string]bool{}
	var out []string
	for len(out) < 6000 {
		var b strings.Builder
		for range 1 + vr.IntN(3) {
			b.WriteString(fixtureSyllables[vr.IntN(len(fixtureSyllables))])
		}
		if w := b.String(); !seen[w] {
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

// TailVariant is one layout's measurement.
type TailVariant struct {
	Name       string        `json:"name"`
	Bytes      int64         `json:"bytes"`
	IndexBytes int64         `json:"indexBytes"`
	Load       time.Duration `json:"load"`
	LoadWAL    int64         `json:"loadWal"`
	Searches   []TailSearch  `json:"searches"`
}

// TailSearch is one query: matches read and the time to the top 20 by
// popularity (popularity held in memory, from the day's export).
type TailSearch struct {
	Query string        `json:"query"`
	Rows  int           `json:"rows"`
	Took  time.Duration `json:"took"`
}

var tailQueries = []string{"star", "star wa", "love", "the dark", "lord of the r", "amelie"}

// RunTailBench builds each compact-tier layout on SQLite in dir (a file per
// layout) and, when pg is set, on PostgreSQL, and measures it.
func RunTailBench(ctx context.Context, rows []TailRow, dir, pgURL string) ([]TailVariant, error) {
	pop := make(map[int64]float64, len(rows))
	for _, r := range rows {
		pop[r.Key] = r.Popularity
	}
	var out []TailVariant
	for _, v := range []struct{ name, table, index string }{
		{"sqlite rowid table", `CREATE TABLE tail(key INTEGER PRIMARY KEY, title TEXT NOT NULL, original_title TEXT NOT NULL, year INTEGER NOT NULL, poster TEXT NOT NULL)`, ""},
		{"sqlite + FTS5 word prefix", `CREATE TABLE tail(key INTEGER PRIMARY KEY, title TEXT NOT NULL, original_title TEXT NOT NULL, year INTEGER NOT NULL, poster TEXT NOT NULL)`,
			`CREATE VIRTUAL TABLE tail_fts USING fts5(title, original_title, content='tail', content_rowid='key', tokenize='unicode61 remove_diacritics 2', prefix='2 3', detail=none, columnsize=0)`},
		{"sqlite + FTS5 trigram", `CREATE TABLE tail(key INTEGER PRIMARY KEY, title TEXT NOT NULL, original_title TEXT NOT NULL, year INTEGER NOT NULL, poster TEXT NOT NULL)`,
			`CREATE VIRTUAL TABLE tail_fts USING fts5(title, original_title, content='tail', content_rowid='key', tokenize='trigram', detail=none, columnsize=0)`},
	} {
		path := filepath.Join(dir, strings.NewReplacer(" ", "-", "+", "").Replace(v.name)+".db")
		for _, suffix := range []string{"", "-wal", "-shm"} {
			os.Remove(path + suffix)
		}
		db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=wal_autocheckpoint(0)&_pragma=cache_size(-65536)")
		if err != nil {
			return out, err
		}
		db.SetMaxOpenConns(1)
		tv := TailVariant{Name: v.name}
		start := time.Now()
		if err := tailLoad(ctx, db, v.table, v.index, rows); err != nil {
			db.Close()
			return out, fmt.Errorf("%s: %w", v.name, err)
		}
		tv.Load = time.Since(start)
		if st, err := os.Stat(path + "-wal"); err == nil {
			tv.LoadWAL = st.Size()
		}
		if _, err := db.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
			db.Close()
			return out, err
		}
		_ = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name='tail'`).Scan(&tv.Bytes)
		_ = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(pgsize),0) FROM dbstat WHERE name LIKE 'tail\_fts%' ESCAPE '\'`).Scan(&tv.IndexBytes)
		if v.index != "" {
			for _, q := range tailQueries {
				var stmt string
				var arg string
				if strings.Contains(v.index, "trigram") {
					if len([]rune(q)) < 3 {
						continue
					}
					stmt, arg = `SELECT rowid FROM tail_fts WHERE tail_fts.title LIKE ? UNION SELECT rowid FROM tail_fts WHERE tail_fts.original_title LIKE ?`, "%"+q+"%"
				} else {
					words := strings.Fields(q)
					for i, w := range words {
						words[i] = `"` + w + `"`
					}
					words[len(words)-1] += "*"
					stmt, arg = `SELECT rowid FROM tail_fts WHERE tail_fts MATCH ?`, strings.Join(words, " ")
				}
				s, err := tailSearch(ctx, db, stmt, arg, pop, strings.Contains(stmt, "UNION"))
				if err != nil {
					db.Close()
					return out, fmt.Errorf("%s %q: %w", v.name, q, err)
				}
				s.Query = q
				tv.Searches = append(tv.Searches, s)
			}
		}
		db.Close()
		out = append(out, tv)
	}
	if pgURL != "" {
		pv, err := tailBenchPostgres(ctx, rows, pgURL, pop)
		out = append(out, pv...)
		if err != nil {
			return out, err
		}
	}
	return out, nil
}

func tailLoad(ctx context.Context, db *sql.DB, table, index string, rows []TailRow) error {
	if _, err := db.ExecContext(ctx, table); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO tail(key,title,original_title,year,poster) VALUES(?,?,?,?,?)`)
	if err != nil {
		return err
	}
	sorted := append([]TailRow(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })
	for _, r := range sorted {
		if _, err := stmt.ExecContext(ctx, r.Key, r.Title, r.OriginalTitle, r.Year, r.Poster); err != nil {
			return err
		}
	}
	stmt.Close()
	if index != "" {
		if _, err := tx.ExecContext(ctx, index); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO tail_fts(tail_fts) VALUES('rebuild')`); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// tailSearch reads every match and keeps the 20 most popular.
func tailSearch(ctx context.Context, db *sql.DB, stmt, arg string, pop map[int64]float64, twice bool) (TailSearch, error) {
	args := []any{arg}
	if twice {
		args = append(args, arg)
	}
	start := time.Now()
	rows, err := db.QueryContext(ctx, stmt, args...)
	if err != nil {
		return TailSearch{}, err
	}
	var keys []int64
	for rows.Next() {
		var k int64
		if err := rows.Scan(&k); err != nil {
			rows.Close()
			return TailSearch{}, err
		}
		keys = append(keys, k)
	}
	rows.Close()
	read := len(keys)
	sort.Slice(keys, func(i, j int) bool { return pop[keys[i]] > pop[keys[j]] })
	if len(keys) > 20 {
		keys = keys[:20]
	}
	return TailSearch{Rows: read, Took: time.Since(start)}, rows.Err()
}

func tailBenchPostgres(ctx context.Context, rows []TailRow, pgURL string, pop map[int64]float64) ([]TailVariant, error) {
	db, err := sql.Open(store.PostgresDriverName, pgURL)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var out []TailVariant
	for _, v := range []struct{ name, index string }{
		{"pg table", ""},
		{"pg + GIN tsvector (simple)", `CREATE INDEX tail_tsv ON tail USING gin (to_tsvector('simple', title || ' ' || original_title))`},
		{"pg + GIN pg_trgm", `CREATE EXTENSION IF NOT EXISTS pg_trgm; CREATE INDEX tail_trgm ON tail USING gin ((title || ' ' || original_title) gin_trgm_ops)`},
	} {
		if _, err := db.ExecContext(ctx, `DROP TABLE IF EXISTS tail`); err != nil {
			return out, err
		}
		tv := TailVariant{Name: v.name}
		var lsn0 string
		_ = db.QueryRowContext(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&lsn0)
		start := time.Now()
		if _, err := db.ExecContext(ctx, `CREATE TABLE tail(key BIGINT PRIMARY KEY, title TEXT NOT NULL, original_title TEXT NOT NULL, year INTEGER NOT NULL, poster TEXT NOT NULL)`); err != nil {
			return out, err
		}
		if err := pgCopyTail(ctx, db, rows); err != nil {
			return out, err
		}
		if v.index != "" {
			if _, err := db.ExecContext(ctx, v.index); err != nil {
				return out, err
			}
		}
		if _, err := db.ExecContext(ctx, `ANALYZE tail`); err != nil {
			return out, err
		}
		tv.Load = time.Since(start)
		var wal float64
		_ = db.QueryRowContext(ctx, `SELECT pg_wal_lsn_diff(pg_current_wal_lsn(), $1::pg_lsn)`, lsn0).Scan(&wal)
		tv.LoadWAL = int64(wal)
		_ = db.QueryRowContext(ctx, `SELECT pg_table_size('tail'), pg_indexes_size('tail')`).Scan(&tv.Bytes, &tv.IndexBytes)
		if v.index != "" {
			for _, q := range tailQueries {
				var stmt string
				var arg string
				if strings.Contains(v.index, "tsvector") {
					words := strings.Fields(q)
					for i := range words {
						words[i] = strings.ReplaceAll(words[i], "'", "")
					}
					words[len(words)-1] += ":*"
					stmt, arg = `SELECT key FROM tail WHERE to_tsvector('simple', title || ' ' || original_title) @@ to_tsquery('simple', $1)`, strings.Join(words, " & ")
				} else {
					if len([]rune(q)) < 3 {
						continue
					}
					stmt, arg = `SELECT key FROM tail WHERE (title || ' ' || original_title) ILIKE $1`, "%"+q+"%"
				}
				s, err := tailSearch(ctx, db, stmt, arg, pop, false)
				if err != nil {
					return out, fmt.Errorf("%s %q: %w", v.name, q, err)
				}
				s.Query = q
				tv.Searches = append(tv.Searches, s)
			}
		}
		out = append(out, tv)
	}
	_, err = db.ExecContext(ctx, `DROP TABLE IF EXISTS tail`)
	return out, err
}

func pgCopyTail(ctx context.Context, db *sql.DB, rows []TailRow) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	const batch = 1000
	for i := 0; i < len(rows); i += batch {
		part := rows[i:min(len(rows), i+batch)]
		var b strings.Builder
		args := make([]any, 0, len(part)*5)
		b.WriteString(`INSERT INTO tail(key,title,original_title,year,poster) VALUES `)
		for j, r := range part {
			if j > 0 {
				b.WriteByte(',')
			}
			n := len(args)
			fmt.Fprintf(&b, "($%d,$%d,$%d,$%d,$%d)", n+1, n+2, n+3, n+4, n+5)
			args = append(args, r.Key, r.Title, r.OriginalTitle, r.Year, r.Poster)
		}
		if _, err := tx.ExecContext(ctx, b.String(), args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// WriteTailBench renders the variants.
func WriteTailBench(w io.Writer, rows int, vs []TailVariant) {
	fmt.Fprintf(w, "%d titles\n\n| layout | table MB | index MB | B/title | load | load WAL MB |", rows)
	for _, q := range tailQueries {
		fmt.Fprintf(w, " %q rows / ms |", q)
	}
	fmt.Fprint(w, "\n|---|---:|---:|---:|---:|---:|")
	for range tailQueries {
		fmt.Fprint(w, "---:|")
	}
	fmt.Fprintln(w)
	for _, v := range vs {
		fmt.Fprintf(w, "| %s | %.1f | %.1f | %.0f | %s | %.0f |", v.Name, float64(v.Bytes)/1e6, float64(v.IndexBytes)/1e6, float64(v.Bytes+v.IndexBytes)/float64(max(rows, 1)), v.Load.Round(100*time.Millisecond), float64(v.LoadWAL)/1e6)
		by := map[string]TailSearch{}
		for _, s := range v.Searches {
			by[s.Query] = s
		}
		for _, q := range tailQueries {
			if s, ok := by[q]; ok {
				fmt.Fprintf(w, " %d / %.1f |", s.Rows, float64(s.Took.Microseconds())/1000)
			} else {
				fmt.Fprint(w, " — |")
			}
		}
		fmt.Fprintln(w)
	}
}
