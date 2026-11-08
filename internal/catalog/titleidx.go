package catalog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Popularity leaves catalog_titles' indexes (rank.go). Six indexes that
// carried it as an order or covered column are replaced by ones without
// it, and catalog_titles_console_pop_page is dropped outright (its paths
// read the rank). catalog_titles_type_browse_pop becomes a partial index
// over seasons and episodes (no TMDB popularity, so no churn). One index
// keeps popularity: the partial catalog_titles_movie_year_pending, whose
// entries are only the movies whose year is unchecked.
//
// The swap runs after start, once the rank is built: each new index is
// built on its own (PostgreSQL: CREATE INDEX CONCURRENTLY, which takes no
// write lock), queries move to it, and the old one is dropped
// (PostgreSQL: DROP INDEX CONCURRENTLY). A migration cannot do this: it
// runs in one transaction, and on PostgreSQL a 1.3M-row index build there
// blocks every catalogue write until the whole upgrade commits.

type titleIndexSwap struct{ old, replacement, def string }

var titleIndexSwaps = []titleIndexSwap{
	{"catalog_titles_type_browse_year", "catalog_titles_type_year", "(type, browsable, adult, year DESC, key)"},
	{"catalog_titles_type_browse_lang", "catalog_titles_type_lang", "(type, browsable, adult, language, key)"},
	{"catalog_titles_type_browse_runtime", "catalog_titles_type_runtime", "(type, browsable, adult, runtime_sec, key)"},
	{"catalog_titles_console_status", "catalog_titles_type_status", "(type, browsable, adult, status, key)"},
	{"catalog_titles_console_year_title_page", "catalog_titles_console_year_title", "(type, browsable, adult, year DESC, sort_title ASC, key, rating, runtime_sec, language, vote_count, release_known, released_ts)"},
	// The warm-up queue holds only undetailed titles, so a popularity
	// change of a detailed title writes nothing to it.
	{"catalog_titles_browse_warm", "catalog_titles_warm_queue", "(popularity DESC, vote_count DESC) WHERE detail=0 AND browsable=1 AND adult=0"},
	// Seasons and episodes have no TMDB popularity, so their index never
	// churns; movies and shows are ordered by the rank.
	{"catalog_titles_type_browse_pop", "catalog_titles_child_pop", "(type, browsable, popularity DESC) WHERE type IN ('season','episode')"},
	{"catalog_titles_console_pop_page", "", ""},
}

// indexHint is " INDEXED BY name" for the index that serves name's role
// now: name itself until the swap, then its replacement, or nothing when
// the role moved to the rank. PostgreSQL never takes hints.
func (c *Catalog) indexHint(name string) string {
	if store.DialectOf(c.db) == store.Postgres {
		return ""
	}
	if c.titleIndexesSwapped.Load() {
		for _, s := range titleIndexSwaps {
			if s.old == name {
				name = s.replacement
				break
			}
		}
	}
	if name == "" {
		return ""
	}
	return " INDEXED BY " + name
}

func (c *Catalog) loadTitleIndexState(ctx context.Context) {
	var v string
	if c.db.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k='title_indexes_swapped'`).Scan(&v) == nil {
		c.titleIndexesSwapped.Store(true)
	}
}

// swapTitleIndexes builds the replacements and drops the old indexes. It
// is idempotent: each step is IF [NOT] EXISTS, and a PostgreSQL build a
// crash left INVALID is dropped and built again.
func (c *Catalog) swapTitleIndexes(ctx context.Context) error {
	if c.titleIndexesSwapped.Load() || !c.rankReady.Load() {
		return nil
	}
	pg := store.DialectOf(c.db) == store.Postgres
	started := time.Now()
	for _, s := range titleIndexSwaps {
		if s.replacement == "" {
			continue
		}
		if pg {
			var valid bool
			err := c.db.QueryRowContext(ctx, `SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE c.relname=? AND n.nspname=current_schema()`, s.replacement).Scan(&valid)
			if err == nil && !valid {
				if _, err := c.db.ExecContext(ctx, `DROP INDEX CONCURRENTLY IF EXISTS `+s.replacement); err != nil {
					return err
				}
			}
			if _, err := c.db.ExecContext(ctx, `CREATE INDEX CONCURRENTLY IF NOT EXISTS `+s.replacement+` ON catalog_titles`+s.def); err != nil {
				return fmt.Errorf("catalog: build %s: %w", s.replacement, err)
			}
		} else if _, err := c.db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS `+s.replacement+` ON catalog_titles`+s.def); err != nil {
			return fmt.Errorf("catalog: build %s: %w", s.replacement, err)
		}
	}
	// Queries name the replacements from here on.
	c.titleIndexesSwapped.Store(true)
	c.writeGen.Add(1)
	drop := "DROP INDEX IF EXISTS "
	if pg {
		drop = "DROP INDEX CONCURRENTLY IF EXISTS "
	}
	for _, s := range titleIndexSwaps {
		if _, err := c.db.ExecContext(ctx, drop+s.old); err != nil {
			return fmt.Errorf("catalog: drop %s: %w", s.old, err)
		}
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES('title_indexes_swapped',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, fmt.Sprint(c.now().Unix())); err != nil {
		return err
	}
	if !pg {
		_, _ = c.db.ExecContext(ctx, `PRAGMA optimize`)
	}
	c.log.Info("catalog: popularity left the title indexes", "took", time.Since(started).Round(time.Millisecond), "replaced", strings.Join(swapNames(), ","))
	return nil
}

func swapNames() []string {
	var out []string
	for _, s := range titleIndexSwaps {
		out = append(out, s.old)
	}
	return out
}

// ReplacePopularityIndexes runs the post-start index swap now (benchmarks
// and tools; Run does it once the rank is built).
func (c *Catalog) ReplacePopularityIndexes(ctx context.Context) error { return c.swapTitleIndexes(ctx) }

// SetRankReady turns the rank's read paths on or off, so a benchmark can
// compare the same pages both ways. Production sets it by building the
// rank.
func (c *Catalog) SetRankReady(on bool) {
	c.rankReady.Store(on)
	c.writeGen.Add(1)
}
