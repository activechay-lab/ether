package catalog

import (
	"cmp"
	"context"
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
)

// yearMergeMax is the most years a title-sorted page reads one index walk
// each for: a decade (Plex's decade filter) and a few more.
const yearMergeMax = 20

// pageYears is the years f admits, when they are between two and
// yearMergeMax: a year range (a decade) or a list of years.
func pageYears(f core.Filter) ([]int, bool) {
	var years []int
	switch {
	case len(f.Years) > 0:
		for _, y := range f.Years {
			if y > 0 && y >= f.YearFrom && (f.YearTo == 0 || y <= f.YearTo) {
				years = append(years, y)
			}
		}
	case f.YearFrom > 0 && f.YearTo > f.YearFrom && f.YearTo-f.YearFrom < yearMergeMax:
		for y := f.YearFrom; y <= f.YearTo; y++ {
			years = append(years, y)
		}
	default:
		return nil, false
	}
	years = slices.DeleteFunc(years, func(y int) bool { return slices.Contains(f.ExcludeYears, y) })
	slices.Sort(years)
	years = slices.Compact(years)
	return years, len(years) >= 2 && len(years) <= yearMergeMax
}

// yearMergeable reports whether q is a title-sorted page of one type whose
// filter admits a few years. The planner has no index that orders a year
// range by title: it reads every title of the range and sorts them, which
// grows with the library. Each year's titles are an index range already in
// title order (catalog_titles_console_year_title), so the page is a merge
// of one short walk per year.
func yearMergeable(q core.BrowseQuery, offset, limit int) ([]int, bool) {
	if q.Sort != core.SortTitle || len(q.SecondarySorts) > 0 || q.SortLastViewed || q.Filter.LocalOnly || q.AfterKey > 0 || offset+limit > mergedPageMax {
		return nil, false
	}
	if q.Filter.Type != core.TypeMovie && q.Filter.Type != core.TypeShow {
		return nil, false
	}
	if !walkableFilter(q.Filter) {
		// A person, a collection or keywords drive the page from their
		// own index; walking each year in title order would pass every
		// title of the range.
		return nil, false
	}
	return pageYears(q.Filter)
}

// yearMergedKeys is the keys of a title-sorted page over years: each
// year's first offset+limit titles in (sort_title, key) order, merged.
func (c *Catalog) yearMergedKeys(ctx context.Context, where string, args []any, q core.BrowseQuery, years []int, offset, limit int) ([]core.RatingKey, error) {
	dir := "ASC"
	if q.Desc {
		dir = "DESC"
	}
	where = c.genreProbe(where, q.Filter)
	var all []sortedKey
	for _, y := range years {
		got, err := c.sortedKeys(ctx, `SELECT t.key, t.sort_title FROM catalog_titles t WHERE `+where+` AND t.year=? ORDER BY t.sort_title `+dir+`, t.key ASC LIMIT ?`, append(append([]any{}, args...), y, offset+limit)...)
		if err != nil {
			return nil, err
		}
		all = append(all, got...)
	}
	return mergedPage(all, q.Desc, offset, limit), nil
}

// sortedKeys reads (key, sort value) rows.
func (c *Catalog) sortedKeys(ctx context.Context, query string, args ...any) ([]sortedKey, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sortedKey
	for rows.Next() {
		var key int64
		var val any
		if err := rows.Scan(&key, &val); err != nil {
			return nil, err
		}
		out = append(out, sortedKey{key: core.RatingKey(key), val: val})
	}
	return out, rows.Err()
}

// mergedPage is the page offset..offset+limit of runs each in (value,
// key) order, merged as the database orders them: value (descending when
// desc), then key.
func mergedPage(all []sortedKey, desc bool, offset, limit int) []core.RatingKey {
	slices.SortStableFunc(all, func(a, b sortedKey) int {
		if c := compareSQL(a.val, b.val); c != 0 {
			if desc {
				return -c
			}
			return c
		}
		return cmp.Compare(a.key, b.key)
	})
	if offset >= len(all) {
		return nil
	}
	out := make([]core.RatingKey, 0, limit)
	for _, a := range all[offset:min(offset+limit, len(all))] {
		out = append(out, a.key)
	}
	return out
}
