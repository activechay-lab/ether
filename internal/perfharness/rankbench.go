package perfharness

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// RankBench measures the popularity rank (catalog/rank.go) on a catalogue
// copy: the first build and the index swap with a concurrent writer, a
// day's popularity refresh (every movie and show, 250 per transaction, as
// the sync writes them) and the rebuild after it, and popularity browse
// pages before and after.
type RankBenchStep struct {
	Name       string        `json:"name"`
	Took       time.Duration `json:"took"`
	WAL        int64         `json:"wal"`
	WriterWait time.Duration `json:"writerWait,omitempty"`
}

type RankBenchPage struct {
	Name       string        `json:"name"`
	Rank       bool          `json:"rank"`
	Statements int64         `json:"statements"`
	Rows       int64         `json:"rows"`
	DB         time.Duration `json:"db"`
}

func RunRankBench(ctx context.Context, db *sql.DB, path string) ([]RankBenchStep, []RankBenchPage, error) {
	c, err := catalog.New(db, nil, config.Catalog{Language: "en-US", Region: "US"}, slog.New(slog.DiscardHandler))
	if err != nil {
		return nil, nil, err
	}
	pg := store.DialectOf(db) == store.Postgres
	wal := func() int64 {
		if pg {
			var lsn string
			_ = db.QueryRowContext(ctx, `SELECT pg_current_wal_lsn()::text`).Scan(&lsn)
			var b float64
			_ = db.QueryRowContext(ctx, `SELECT pg_wal_lsn_diff(?::pg_lsn, '0/0')`, lsn).Scan(&b)
			return int64(b)
		}
		st, err := os.Stat(path + "-wal")
		if err != nil {
			return 0
		}
		return st.Size()
	}
	var keys []int64
	rows, err := db.QueryContext(ctx, `SELECT key FROM catalog_titles WHERE type IN ('movie','show') ORDER BY key`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var k int64
		rows.Scan(&k)
		keys = append(keys, k)
	}
	rows.Close()
	if len(keys) == 0 {
		return nil, nil, fmt.Errorf("rankbench: no movies or shows")
	}
	// step runs fn with a writer touching one title every 20 ms and
	// reports the longest that writer waited.
	step := func(name string, fn func() error) (RankBenchStep, error) {
		if !pg {
			_ = store.Checkpoint(ctx, db, true)
		}
		s := RankBenchStep{Name: name}
		w0 := wal()
		stop := make(chan struct{})
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				case <-time.After(20 * time.Millisecond):
				}
				start := time.Now()
				_, _ = db.ExecContext(ctx, `UPDATE catalog_titles SET year=year WHERE key=?`, keys[i%len(keys)])
				s.WriterWait = max(s.WriterWait, time.Since(start))
			}
		}()
		start := time.Now()
		err := fn()
		s.Took = time.Since(start)
		close(stop)
		wg.Wait()
		s.WAL = wal() - w0
		return s, err
	}
	pages := func(rank bool) ([]RankBenchPage, error) {
		cases := []struct {
			name string
			f    core.Filter
			desc bool
			off  int
		}{
			{"movies p1", core.Filter{Type: core.TypeMovie}, true, 0},
			{"movies offset 10000", core.Filter{Type: core.TypeMovie}, true, 10000},
			{"shows p1", core.Filter{Type: core.TypeShow}, true, 0},
			{"least popular movies p1", core.Filter{Type: core.TypeMovie}, false, 0},
			{"genre Western", core.Filter{Type: core.TypeMovie, Genres: []string{"Western"}}, true, 0},
			{"year 1985", core.Filter{Type: core.TypeMovie, YearFrom: 1985, YearTo: 1985}, true, 0},
			{"language fr", core.Filter{Type: core.TypeMovie, Languages: []string{"fr"}}, true, 0},
			{"country Japan", core.Filter{Type: core.TypeMovie, Countries: []string{"Japan"}}, true, 0},
			{"keyword", core.Filter{Type: core.TypeMovie, Keywords: []string{"love"}}, true, 0},
		}
		var studio string
		_ = db.QueryRowContext(ctx, `SELECT studio FROM catalog_studios GROUP BY studio ORDER BY COUNT(*) DESC LIMIT 1 OFFSET 50`).Scan(&studio)
		var person, collection int64
		_ = db.QueryRowContext(ctx, `SELECT person_tmdb FROM catalog_person_names ORDER BY credits DESC LIMIT 1 OFFSET 20`).Scan(&person)
		_ = db.QueryRowContext(ctx, `SELECT collection_id FROM catalog_titles WHERE collection_id<>0 LIMIT 1`).Scan(&collection)
		cases = append(cases, struct {
			name string
			f    core.Filter
			desc bool
			off  int
		}{"studio " + studio, core.Filter{Type: core.TypeMovie, Studio: studio}, true, 0},
			struct {
				name string
				f    core.Filter
				desc bool
				off  int
			}{"person", core.Filter{Type: core.TypeMovie, Person: person}, true, 0},
			struct {
				name string
				f    core.Filter
				desc bool
				off  int
			}{"collection", core.Filter{Type: core.TypeMovie, Collection: collection}, true, 0})
		var out []RankBenchPage
		for _, cs := range cases {
			c.SetRankReady(rank)
			sctx, st := store.WithQueryStats(ctx)
			if _, err := c.Browse(sctx, core.BrowseQuery{Filter: cs.f, Sort: core.SortPopularity, Desc: cs.desc, Offset: cs.off, Limit: 50, NoTotal: true}); err != nil {
				return out, fmt.Errorf("%s: %w", cs.name, err)
			}
			out = append(out, RankBenchPage{Name: cs.name, Rank: rank, Statements: st.Queries(), Rows: st.Rows(), DB: st.DBTime()})
		}
		return out, nil
	}
	var steps []RankBenchStep
	before, err := pages(false)
	if err != nil {
		return nil, nil, err
	}
	s, err := step("first rank build", func() error { _, err := c.RebuildRank(ctx); return err })
	steps = append(steps, s)
	if err != nil {
		return steps, before, err
	}
	s, err = step("replace popularity indexes", func() error { return c.ReplacePopularityIndexes(ctx) })
	steps = append(steps, s)
	if err != nil {
		return steps, before, err
	}
	s, err = step("daily popularity refresh (250/tx)", func() error {
		for i := 0; i < len(keys); i += 250 {
			tx, err := db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			for _, k := range keys[i:min(len(keys), i+250)] {
				if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET popularity=popularity*? WHERE key=?`, 0.8+0.4*float64(k%1000)/1000, k); err != nil {
					tx.Rollback()
					return err
				}
			}
			if err := tx.Commit(); err != nil {
				return err
			}
		}
		return nil
	})
	steps = append(steps, s)
	if err != nil {
		return steps, before, err
	}
	s, err = step("daily rank rebuild", func() error { _, err := c.RebuildRank(ctx); return err })
	steps = append(steps, s)
	if err != nil {
		return steps, before, err
	}
	after, err := pages(true)
	return steps, append(before, after...), err
}

func WriteRankBench(w io.Writer, steps []RankBenchStep, pages []RankBenchPage) {
	fmt.Fprintln(w, "| step | took | WAL MB | longest 1-row write during it |\n|---|---:|---:|---:|")
	for _, s := range steps {
		fmt.Fprintf(w, "| %s | %s | %.1f | %s |\n", s.Name, s.Took.Round(time.Millisecond), float64(s.WAL)/1e6, s.WriterWait.Round(time.Millisecond))
	}
	fmt.Fprintln(w, "\n| popularity page | before: stmts / rows / DB ms | after (rank, indexes swapped) |\n|---|---:|---:|")
	half := len(pages) / 2
	for i := 0; i < half; i++ {
		b, a := pages[i], pages[i+half]
		fmt.Fprintf(w, "| %s | %d / %d / %.1f | %d / %d / %.1f |\n", strings.TrimSpace(b.Name), b.Statements, b.Rows, float64(b.DB.Microseconds())/1000, a.Statements, a.Rows, float64(a.DB.Microseconds())/1000)
	}
}
