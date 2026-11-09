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
// narrow rows, then read only the selected keys' cards. Other clients keep Browse's
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
	if empty, err := c.consoleDomainEmpty(ctx, q.Filter); err != nil {
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
	// Popularity order alone reads the rank (rank.go): the boundary row's
	// rk, and seeks on (rk, key) per type. rk is the negated popularity, so
	// the direction flips.
	useRank := c.rankReady.Load() && len(specs) == 1 && specs[0].Sort == core.SortPopularity &&
		!(c.config().ShowUpcomingMovies && q.Filter.RuleVisibility == "") && !consoleHasSparseCandidates(q.Filter)
	if useRank {
		specs = []core.SortSpec{{Sort: core.SortPopularity, Desc: !specs[0].Desc}}
	}
	var after []any
	if useRank && q.AfterKey != 0 {
		var rk float64
		if err := c.db.QueryRowContext(ctx, `SELECT rk FROM catalog_rank WHERE key=?`, int64(q.AfterKey)).Scan(&rk); err != nil {
			return core.Page{}, fmt.Errorf("catalog: cursor title unavailable; restart browsing: %w", err)
		}
		after = []any{rk}
	} else if q.AfterKey != 0 {
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
		from := "catalog_titles t"
		if useRank {
			if rankOnly(where) {
				from, columns[0] = "catalog_rank t", "t.rk"
			} else {
				from, columns[0] = "catalog_rank r CROSS JOIN catalog_titles t ON t.key=r.key", "r.rk"
				if store.DialectOf(c.db) == store.Postgres {
					from = "catalog_rank r JOIN catalog_titles t ON t.key=r.key"
				}
				where += " AND r.type='" + string(typ) + "' AND r.browsable=1 AND r.adult=0"
			}
			orders[0] = columns[0] + " ASC"
			if specs[0].Desc {
				orders[0] = columns[0] + " DESC"
			}
		}
		hint := ""
		// Availability projections can be much smaller than the title domain;
		// let the planner drive those key sets before ordering the candidates.
		selectiveEvidence := typed.Playable != "" || typed.DebridCached != "" || len(typed.Qualities) > 0 || typed.ReleaseGate != ""
		if store.DialectOf(c.db) != store.Postgres && !selectiveEvidence && !consoleHasSparseCandidates(typed) && !useRank {
			if specs[0].Sort == core.SortPopularity && specs[0].Desc && len(specs) == 1 {
				hint = c.indexHint("catalog_titles_console_pop_page")
			}
			if len(specs) == 2 && specs[0].Sort == core.SortYear && specs[0].Desc && specs[1].Sort == core.SortTitle && !specs[1].Desc {
				hint = c.indexHint("catalog_titles_console_year_title_page")
			}
		}
		for _, seek := range consoleSeekRanges(columns, specs, after, q.AfterKey) {
			if !useRank && store.DialectOf(c.db) != store.Postgres && consolePositiveEvidence(typed) && len(specs) == 1 && specs[0].Sort == core.SortPopularity && specs[0].Desc && !c.config().ShowUpcomingMovies && c.indexHint("catalog_titles_console_pop_page") != "" {
				matches, complete, err := c.consoleEvidencePilot(ctx, typ, where, args, seek, limit)
				if err != nil {
					return core.Page{}, err
				}
				if complete {
					for _, r := range matches {
						all = append(all, row{key: r.key, values: []any{r.popularity}})
					}
					continue
				}
			}
			sqlArgs := append(append([]any{}, args...), string(typ))
			sqlArgs = append(sqlArgs, seek.args...)
			sqlArgs = append(sqlArgs, limit)
			rows, err := c.db.QueryContext(ctx, `SELECT t.key,`+strings.Join(columns, ",")+` FROM `+from+hint+` WHERE `+where+` AND t.type=?`+seek.predicate+` ORDER BY `+strings.Join(orders, ",")+`,t.key ASC LIMIT ?`, sqlArgs...)
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
	items, err := c.consoleCards(ctx, keys)
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

// consoleCards reads the titles of keys, in that order, as the console's
// browse cards show them (admin.CardFor): the title row and its genres
// (core.FactGenres). Credits, keywords, studios, trailers and aired counts
// grow with how much TMDB knows about a title, and no card renders them. A
// title cached whole for this generation is served as it is; the others are
// partial and never enter the title cache. The page query already applied
// every visibility guard in SQL, so nothing is filtered here.
func (c *Catalog) consoleCards(ctx context.Context, keys []core.RatingKey) ([]core.Title, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	gen, minute := c.writeGen.Load(), c.minuteBucket()
	byKey := make(map[core.RatingKey]core.Title, len(keys))
	var missing []any
	for _, k := range keys {
		if t, ok := c.cachedStored(k, gen, minute); ok {
			byKey[k] = t
			continue
		}
		missing = append(missing, int64(k))
	}
	if len(missing) > 0 {
		// A cursor page holds at most 101 keys: one statement.
		rows, err := c.db.QueryContext(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE key IN (`+placeholders(len(missing))+`)`, missing...)
		if err != nil {
			return nil, err
		}
		var read []core.Title
		for rows.Next() {
			st, err := scanTitle(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			read = append(read, st.t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if err := c.hydrateFacts(ctx, read, core.FactGenres); err != nil {
			return nil, err
		}
		for _, t := range read {
			byKey[t.Key] = t
		}
	}
	items := make([]core.Title, 0, len(keys))
	for _, k := range keys {
		if t, ok := byKey[k]; ok {
			items = append(items, t)
		}
	}
	return items, nil
}
