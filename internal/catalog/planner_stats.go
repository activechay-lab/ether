package catalog

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// Earlier upgrades sampled new indexes with analysis_limit=1000. For
	// low-cardinality prefixes such as type, that can underestimate a range
	// by two orders of magnitude and displace the selective per-show index.
	// Repair existing catalogues once; future index migrations use full stats.
	store.Register("catalog", 43, `PRAGMA analysis_limit=0; ANALYZE catalog_titles;`)
	store.RegisterDialect("catalog", 43, store.Postgres, `SELECT 1;`)
}

// analyzeFull refreshes SQLite's planner statistics for the named tables or
// indexes from every row, on one connection. A sampled ANALYZE, which
// PRAGMA optimize runs under a temporary analysis limit, estimates
// catalog_titles' type prefix at a few hundred rows instead of a quarter of
// the table and moves per-show reads onto type-leading indexes that walk
// every episode (migration 43 repaired that once).
func (c *Catalog) analyzeFull(ctx context.Context, names ...string) error {
	conn, err := c.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `PRAGMA analysis_limit=0`); err != nil {
		return err
	}
	for _, name := range names {
		if _, err := conn.ExecContext(ctx, `ANALYZE `+name); err != nil {
			return fmt.Errorf("catalog: analyze %s: %w", name, err)
		}
	}
	return nil
}
