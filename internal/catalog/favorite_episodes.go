package catalog

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// favoritesTitleJoin is the favourite-episode read's FROM clause: the viewer's
// favourite title rows (a few hundred at most, indexed by the favorites
// primary key) probed against the catalogue by rating key. It reads the
// viewer's own favorite set, so a page costs the favorites, never the
// catalogue's size. subject_id is the rating key as a fixed-width decimal
// string; comparing it as an integer lets the catalogue's key index serve the
// probe.
const favoritesTitleJoin = ` FROM favorites_items f JOIN catalog_titles e ON e.key=CAST(f.subject_id AS INTEGER) JOIN catalog_titles t ON t.key=e.show_key`

// favoriteEpisodesWhere is playedEpisodesWhere's filter shape without the
// watched predicate: the series and episode predicates a browse filter names.
func (c *Catalog) favoriteEpisodesWhere(ctx context.Context, show core.Filter, showKey core.RatingKey) (string, []any, error) {
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

// FavoriteEpisodesByKey pages a viewer's favorite episodes in Jellyfin's
// episode SortName order — series sort title, then season, then episode — of
// the series and episodes the browse filter show admits, restricted to one
// series when showKey is nonzero. It joins the viewer's favorite rows to the
// catalogue, so the page and its total follow the favorite set, not the
// library; no series is ever expanded.
func (c *Catalog) FavoriteEpisodesByKey(ctx context.Context, show core.Filter, userID int64, showKey core.RatingKey, offset, limit int) ([]core.Title, error) {
	where, args, err := c.favoriteEpisodesWhere(ctx, show, showKey)
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + prefixedCols("e") + favoritesTitleJoin +
		` WHERE f.viewer_id=? AND f.kind='title' AND ` + where +
		` ORDER BY t.sort_title ASC, e.season ASC, e.episode ASC, e.key ASC LIMIT ? OFFSET ?`
	a := append([]any{userID}, args...)
	a = append(a, limit, max(offset, 0))
	return c.queryPagedTitles(ctx, q, a...)
}

// CountFavoriteEpisodes counts FavoriteEpisodesByKey's whole set from the
// viewer's favorite rows.
func (c *Catalog) CountFavoriteEpisodes(ctx context.Context, show core.Filter, userID int64, showKey core.RatingKey) (int, error) {
	where, args, err := c.favoriteEpisodesWhere(ctx, show, showKey)
	if err != nil {
		return 0, err
	}
	var total int
	a := append([]any{userID}, args...)
	err = c.db.QueryRowContext(ctx, `SELECT COUNT(*)`+favoritesTitleJoin+
		` WHERE f.viewer_id=? AND f.kind='title' AND `+where, a...).Scan(&total)
	return total, err
}
