package catalog

import (
	"context"
	"errors"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// CachedAiredEpisodes returns locally stored, aired, non-adult episodes in
// descending air-date order. A zero show key selects all shows. It performs
// no network access or viewer filtering; callers must enforce viewer policy
// before applying their final limit.
func (c *Catalog) CachedAiredEpisodes(ctx context.Context, show core.RatingKey, offset, limit int) (core.Page, error) {
	if show != 0 && show.Kind() != core.KindShow || offset < 0 {
		return core.Page{}, errors.New("catalog: invalid aired episode query")
	}
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	asOf := c.now()
	where := `t.type='episode' AND t.adult=0 AND t.release_known=1 AND t.released_ts<=?`
	args := []any{asOf.Unix()}
	if show != 0 {
		where += ` AND t.show_key=?`
		args = append(args, int64(show))
	}
	var total int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles t WHERE `+where, args...).Scan(&total); err != nil {
		return core.Page{}, err
	}
	query := `SELECT ` + prefixedCols("t") + ` FROM catalog_titles t WHERE ` + where + ` ORDER BY t.released_ts DESC,t.key ASC LIMIT ? OFFSET ?`
	items, err := c.queryTitles(ctx, query, append(append([]any{}, args...), limit, offset)...)
	if err != nil {
		return core.Page{}, err
	}
	if err := applyAirAddedTimes(ctx, c.db, items, asOf); err != nil {
		return core.Page{}, err
	}
	return core.Page{Items: items, Total: total}, nil
}

// airedManyBatch bounds the shows one CachedAiredEpisodesMany statement
// covers.
const airedManyBatch = 50

// CachedAiredEpisodesMany is CachedAiredEpisodes(show, 0, limit) for several
// shows: per show the same aired episodes, newest first, and the same total.
// A page of shows costs two statements per airedManyBatch shows instead of
// a count, a list and their hydration per show.
func (c *Catalog) CachedAiredEpisodesMany(ctx context.Context, shows []core.RatingKey, limit int) (map[core.RatingKey]core.Page, error) {
	out := make(map[core.RatingKey]core.Page, len(shows))
	if limit <= 0 {
		limit = 50
	}
	limit = min(limit, 500)
	var keys []core.RatingKey
	seen := map[core.RatingKey]bool{}
	for _, show := range shows {
		if show == 0 || show.Kind() != core.KindShow {
			return nil, errors.New("catalog: invalid aired episode query")
		}
		if !seen[show] {
			seen[show] = true
			keys = append(keys, show)
			out[show] = core.Page{Items: []core.Title{}}
		}
	}
	asOf := c.now()
	const where = `t.type='episode' AND t.adult=0 AND t.release_known=1 AND t.released_ts<=?`
	for part := range store.Chunks(keys, airedManyBatch) {
		args := []any{asOf.Unix()}
		for _, show := range part {
			args = append(args, int64(show))
		}
		rows, err := c.db.QueryContext(ctx, `SELECT t.show_key, COUNT(*) FROM catalog_titles t WHERE `+where+` AND t.show_key IN (`+placeholders(len(part))+`) GROUP BY t.show_key`, args...)
		if err != nil {
			return nil, err
		}
		found := false
		for rows.Next() {
			var show int64
			var total int
			if err := rows.Scan(&show, &total); err != nil {
				rows.Close()
				return nil, err
			}
			if page, ok := out[core.RatingKey(show)]; ok {
				page.Total = total
				out[core.RatingKey(show)] = page
				found = found || total > 0
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if !found {
			continue
		}
		// One branch per show keeps each show's own newest-first limit on
		// the (show_key, released_ts) index.
		parts := make([]string, 0, len(part))
		args = args[:0]
		for _, show := range part {
			if out[show].Total == 0 {
				continue
			}
			parts = append(parts, `SELECT * FROM (SELECT `+prefixedCols("t")+` FROM catalog_titles t WHERE `+where+` AND t.show_key=? ORDER BY t.released_ts DESC,t.key ASC LIMIT ?) AS p`)
			args = append(args, asOf.Unix(), int64(show), limit)
		}
		// The outer order is the single-show read's order within each show.
		items, err := c.queryTitles(ctx, strings.Join(parts, "\nUNION ALL\n")+"\nORDER BY show_key, released_ts DESC, key ASC", args...)
		if err != nil {
			return nil, err
		}
		if err := applyAirAddedTimes(ctx, c.db, items, asOf); err != nil {
			return nil, err
		}
		for _, item := range items {
			if page, ok := out[item.ShowKey]; ok {
				page.Items = append(page.Items, item)
				out[item.ShowKey] = page
			}
		}
	}
	return out, nil
}
