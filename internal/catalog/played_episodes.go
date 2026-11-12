package catalog

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// PlayedEpisodesByLastViewed pages a viewer's played episodes, most recently
// played first (or least recently first), whose series and episode pass the
// browse filter show. It joins the viewer's watch rows to the catalogue so
// both the sort and the page follow the viewer's own history, never the
// library; the SQL carries the viewer's exclusions, so a page costs the
// viewer's played set, not the catalogue's size. Only episodes with a view
// (view_count>0) are returned. showKey, when nonzero, restricts to one
// series. Ties break by key, so the order is total.
func (c *Catalog) PlayedEpisodesByLastViewed(ctx context.Context, show core.Filter, userID int64, showKey core.RatingKey, desc bool, offset, limit int) ([]core.Title, error) {
	where, args, err := c.playedEpisodesWhere(ctx, show, showKey)
	if err != nil {
		return nil, err
	}
	dir := "ASC"
	if desc {
		dir = "DESC"
	}
	q := `SELECT ` + prefixedCols("e") + ` FROM state_watch w CROSS JOIN catalog_titles e JOIN catalog_titles t ON t.key=e.show_key
		WHERE w.user_id=? AND w.view_count>0 AND e.key=w.rating_key AND ` + where + `
		ORDER BY w.last_viewed_at ` + dir + `, e.key ASC LIMIT ? OFFSET ?`
	a := append([]any{userID}, args...)
	a = append(a, limit, max(offset, 0))
	return c.queryPagedTitles(ctx, q, a...)
}

// CountPlayedEpisodes counts the viewer's played episodes matching the
// series and episode predicates in show, restricted to one series when
// showKey is nonzero. Its cost follows the viewer's played set.
func (c *Catalog) CountPlayedEpisodes(ctx context.Context, show core.Filter, userID int64, showKey core.RatingKey) (int, error) {
	where, args, err := c.playedEpisodesWhere(ctx, show, showKey)
	if err != nil {
		return 0, err
	}
	var total int
	a := append([]any{userID}, args...)
	err = c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM state_watch w CROSS JOIN catalog_titles e JOIN catalog_titles t ON t.key=e.show_key
		WHERE w.user_id=? AND w.view_count>0 AND e.key=w.rating_key AND `+where, a...).Scan(&total)
	return total, err
}

// playedEpisodesWhere is storedEpisodesWhere plus the viewer's play (the
// caller adds w.view_count>0) and an optional single-series restriction.
func (c *Catalog) playedEpisodesWhere(ctx context.Context, show core.Filter, showKey core.RatingKey) (string, []any, error) {
	show = c.viewerPaging(ctx, show)
	where, args, err := c.storedEpisodesWhere(show)
	if err != nil {
		return "", nil, err
	}
	if showKey != 0 {
		where += " AND t.key=?"
		args = append(args, showKey)
	}
	return where, args, nil
}
