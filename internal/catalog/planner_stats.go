package catalog

import "github.com/braedonsaunders/ether/internal/store"

func init() {
	// Earlier upgrades sampled new indexes with analysis_limit=1000. For
	// low-cardinality prefixes such as type, that can underestimate a range
	// by two orders of magnitude and displace the selective per-show index.
	// Repair existing catalogues once; future index migrations use full stats.
	store.Register("catalog", 43, `PRAGMA analysis_limit=0; ANALYZE catalog_titles;`)
	store.RegisterDialect("catalog", 43, store.Postgres, `SELECT 1;`)
}
