package catalog

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// consoleCursorPage never visits the preceding pages: resolve one boundary row
// by primary key, seek each media type's ordered index, merge at most 2*(sorts+1)*limit
// narrow rows, then hydrate only the selected keys. Other clients keep Browse's
// existing date-added, paging and cache contracts.
func (c *Catalog) consoleCursorPage(ctx context.Context, q core.BrowseQuery) (core.Page, error) {
	if q.Offset != 0 || q.SortLastViewed {
		return core.Page{}, fmt.Errorf("catalog: cursor paging requires zero offset and a scalar sort")
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 101 {
		limit = 101
	}
	if empty, err := c.consoleYearEmpty(ctx, q.Filter); err != nil {
		return core.Page{}, err
	} else if empty {
		return core.Page{Items: []core.Title{}}, nil
	}
	specs := append([]core.SortSpec{{Sort: q.Sort, Desc: q.Desc}}, q.SecondarySorts...)
	if specs[0].Sort == "" {
		specs[0] = core.SortSpec{Sort: core.SortPopularity, Desc: true}
	}
	if len(specs) > 3 {
		return core.Page{}, fmt.Errorf("catalog: too many cursor sorts")
	}
	var after []any
	if q.AfterKey != 0 {
		columns := make([]string, len(specs))
		for i, s := range specs {
			columns[i] = sortColumn("t", s.Sort)
			if i == 0 && s.Sort == core.SortAdded {
				columns[i] = "CASE WHEN t.type='show' THEN t.show_added_ts ELSE t.released_ts END"
			}
		}
		after = make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range after {
			dest[i] = &after[i]
		}
		if err := c.db.QueryRowContext(ctx, `SELECT `+strings.Join(columns, ",")+` FROM catalog_titles t WHERE t.key=?`, int64(q.AfterKey)).Scan(dest...); err != nil {
			return core.Page{}, fmt.Errorf("catalog: cursor title unavailable; restart browsing: %w", err)
		}
	}
	types := []core.MediaType{q.Filter.Type}
	if q.Filter.Type == "" {
		types = []core.MediaType{core.TypeMovie, core.TypeShow}
	}
	type row struct {
		key    core.RatingKey
		values []any
	}
	all := make([]row, 0, limit*len(types))
	for _, typ := range types {
		typed := q.Filter
		typed.Type = typ
		where, args, err := c.browseWhere(typed)
		if err != nil {
			return core.Page{}, err
		}

		columns := make([]string, len(specs))
		orders := make([]string, len(specs))
		for i, s := range specs {
			columns[i] = sortColumn("t", s.Sort)
			if i == 0 && s.Sort == core.SortAdded && typ == core.TypeShow {
				columns[i] = "t.show_added_ts"
			}
			dir := "ASC"
			if s.Desc {
				dir = "DESC"
			}
			orders[i] = columns[i] + " " + dir
		}
		hint := ""
		if store.DialectOf(c.db) != store.Postgres {
			if specs[0].Sort == core.SortPopularity && specs[0].Desc && len(specs) == 1 {
				hint = " INDEXED BY catalog_titles_console_pop_page"
			}
			if len(specs) == 2 && specs[0].Sort == core.SortYear && specs[0].Desc && specs[1].Sort == core.SortTitle && !specs[1].Desc {
				hint = " INDEXED BY catalog_titles_console_year_title_page"
			}
		}
		for _, seek := range consoleSeekRanges(columns, specs, after, q.AfterKey) {
			sqlArgs := append(append([]any{}, args...), string(typ))
			sqlArgs = append(sqlArgs, seek.args...)
			sqlArgs = append(sqlArgs, limit)
			rows, err := c.db.QueryContext(ctx, `SELECT t.key,`+strings.Join(columns, ",")+` FROM catalog_titles t`+hint+` WHERE `+where+` AND t.type=?`+seek.predicate+` ORDER BY `+strings.Join(orders, ",")+`,t.key ASC LIMIT ?`, sqlArgs...)
			if err != nil {
				return core.Page{}, err
			}
			for rows.Next() {
				r := row{values: make([]any, len(specs))}
				dest := []any{&r.key}
				for i := range r.values {
					dest = append(dest, &r.values[i])
				}
				if err := rows.Scan(dest...); err != nil {
					rows.Close()
					return core.Page{}, err
				}
				all = append(all, r)
			}
			err = rows.Err()
			rows.Close()
			if err != nil {
				return core.Page{}, err
			}
		}
	}
	slices.SortFunc(all, func(a, b row) int {
		for i, s := range specs {
			if n := compareSQL(a.values[i], b.values[i]); n != 0 {
				if s.Desc {
					return -n
				}
				return n
			}
		}
		return cmp.Compare(a.key, b.key)
	})
	keys := make([]core.RatingKey, 0, min(limit, len(all)))
	for _, r := range all[:min(limit, len(all))] {
		keys = append(keys, r.key)
	}
	items, err := c.titlesInKeyOrder(ctx, keys)
	if err != nil {
		return core.Page{}, err
	}
	if items == nil {
		items = []core.Title{}
	}
	if q.Sort == core.SortAdded {
		if err := applyAirAddedTimes(ctx, c.db, items, c.now()); err != nil {
			return core.Page{}, err
		}
	}
	return core.Page{Items: items, Total: len(items)}, nil
}

type consoleSeekRange struct {
	predicate string
	args      []any
}

// Disjoint ranges let both engines seek every equality prefix and the next
// strict boundary. A single OR expression can still walk an entire tie block
// (e.g. 600k titles with the same release date) before reaching the key boundary.
// Each range contributes at most one page to the bounded merge.
func consoleSeekRanges(columns []string, specs []core.SortSpec, values []any, key core.RatingKey) []consoleSeekRange {
	if key == 0 {
		return []consoleSeekRange{{}}
	}
	ranges := make([]consoleSeekRange, 0, len(columns)+1)
	for i := 0; i <= len(columns); i++ {
		parts := make([]string, 0, i+1)
		args := make([]any, 0, i+1)
		for j := 0; j < i; j++ {
			parts = append(parts, columns[j]+"=?")
			args = append(args, values[j])
		}
		if i == len(columns) {
			parts = append(parts, "t.key>?")
			args = append(args, int64(key))
		} else {
			op := ">"
			if specs[i].Desc {
				op = "<"
			}
			parts = append(parts, columns[i]+op+"?")
			args = append(args, values[i])
		}
		ranges = append(ranges, consoleSeekRange{" AND " + strings.Join(parts, " AND "), args})
	}
	return ranges
}
