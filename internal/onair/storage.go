package onair

import (
	"context"
	"database/sql"
)

// Queryer runs read queries: a *sql.DB, *sql.Conn or *sql.Tx.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// AssetKindTotal totals the stored assets of one kind.
type AssetKindTotal struct {
	Kind  string
	Count int64
	Bytes int64
}

// AssetKindTotals is the stored assets' bytes by kind, largest first, for
// support bundles. size precedes the content column, so no blob page is
// read.
func AssetKindTotals(ctx context.Context, q Queryer) ([]AssetKindTotal, error) {
	rows, err := q.QueryContext(ctx, `SELECT kind, COUNT(*), COALESCE(SUM(size),0) FROM onair_assets GROUP BY kind ORDER BY 3 DESC LIMIT 20`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AssetKindTotal
	for rows.Next() {
		var a AssetKindTotal
		if err := rows.Scan(&a.Kind, &a.Count, &a.Bytes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
