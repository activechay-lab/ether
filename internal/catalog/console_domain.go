package catalog

import (
	"context"
	"database/sql"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// consoleYearEmpty proves empty year ranges with a B-tree seek before a
// popularity-ordered page can walk every candidate. Only mandatory AND bounds
// are used; OR alternatives are deliberately left to the ordinary query.
func (c *Catalog) consoleYearEmpty(ctx context.Context, f core.Filter) (bool, error) {
	from, to := 0, 0
	var filter func(core.Filter)
	var group func(*core.FilterGroup)
	filter = func(v core.Filter) {
		if v.YearFrom > from {
			from = v.YearFrom
		}
		if v.YearTo > 0 && (to == 0 || v.YearTo < to) {
			to = v.YearTo
		}
		group(v.Group)
	}
	group = func(g *core.FilterGroup) {
		if g == nil {
			return
		}
		if g.Filter != nil {
			filter(*g.Filter)
			return
		}
		if g.Operator == "and" {
			for i := range g.Children {
				group(&g.Children[i])
			}
		}
	}
	filter(f)
	if from == 0 && to == 0 {
		return false, nil
	}
	types := []core.MediaType{f.Type}
	if f.Type == "" {
		types = []core.MediaType{core.TypeMovie, core.TypeShow}
	}
	for _, typ := range types {
		hint := ""
		if store.DialectOf(c.db) != store.Postgres {
			hint = " INDEXED BY catalog_titles_type_browse_year"
		}
		query := `SELECT key FROM catalog_titles` + hint + ` WHERE type=? AND browsable=1 AND adult=0 AND year>=?`
		args := []any{string(typ), max(1, from)}
		if to > 0 {
			query += " AND year<=?"
			args = append(args, to)
		}
		var key int64
		err := c.db.QueryRowContext(ctx, query+" LIMIT 1", args...).Scan(&key)
		if err == nil {
			return false, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	return true, nil
}
