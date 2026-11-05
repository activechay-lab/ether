package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

// An opt-in plan diagnostic compares covering ordered scans with evidence-led
// primary-key lookups against the same large fixture and identical predicates.
func consoleEvidencePlan(t *testing.T, c *Catalog, q core.BrowseQuery) {
	f := normalizeConsoleFilter(q.Filter)
	f.Type = core.TypeMovie
	where, args, err := c.browseWhere(f)
	if err != nil {
		t.Fatal(err)
	}
	for _, hint := range []string{"", " INDEXED BY catalog_titles_console_pop_page"} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		sql := `SELECT t.key,t.popularity FROM catalog_titles t` + hint + ` WHERE ` + where + ` ORDER BY t.popularity DESC,t.key ASC LIMIT 49`
		start := time.Now()
		rows, err := c.db.QueryContext(ctx, sql, args...)
		if err == nil {
			for rows.Next() {
				var key int64
				var pop float64
				if err = rows.Scan(&key, &pop); err != nil {
					break
				}
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
		}
		t.Logf("evidence plan hint=%q elapsed=%s error=%v", hint, time.Since(start), err)
		rows, err = c.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+sql, args...)
		if err == nil {
			for rows.Next() {
				var a, b, d int
				var detail string
				if rows.Scan(&a, &b, &d, &detail) == nil {
					t.Log(detail)
				}
			}
			rows.Close()
		}
		cancel()
	}
}
