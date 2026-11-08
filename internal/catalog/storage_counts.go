package catalog

import (
	"context"
	"database/sql"
)

// Queryer runs read queries: a *sql.DB, *sql.Conn or *sql.Tx.
type Queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// StorageCount counts catalogue rows: by type and browsable state (Detail
// nil), or by browsable and detail state (Type empty).
type StorageCount struct {
	Type      string
	Browsable bool
	Detail    *bool
	Rows      int64
}

// StorageCounts is what the mirror holds, for support bundles. Each
// grouping reads one narrow covering index (type,browsable,… and
// browsable,detail,…), never the table.
func StorageCounts(ctx context.Context, q Queryer) ([]StorageCount, error) {
	var out []StorageCount
	for _, stmt := range []string{
		`SELECT type, browsable, -1, COUNT(*) FROM catalog_titles GROUP BY type, browsable`,
		`SELECT '', browsable, detail, COUNT(*) FROM catalog_titles GROUP BY browsable, detail`,
	} {
		rows, err := q.QueryContext(ctx, stmt)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var c StorageCount
			var browsable, detail int
			if err := rows.Scan(&c.Type, &browsable, &detail, &c.Rows); err != nil {
				rows.Close()
				return nil, err
			}
			c.Browsable = browsable != 0
			if detail >= 0 {
				d := detail != 0
				c.Detail = &d
			}
			out = append(out, c)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}
