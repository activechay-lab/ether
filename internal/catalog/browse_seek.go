package catalog

import (
	"context"
	"database/sql"
	"errors"

	"github.com/braedonsaunders/ether/internal/core"
)

// ErrSeekUnavailable reports that a seek's title is gone from the page's
// order (deleted, hidden or filtered out); the caller pages by offset.
var ErrSeekUnavailable = core.ErrSeekUnavailable

// seekableBrowse is core.BrowseSeekable.
func seekableBrowse(q core.BrowseQuery) bool { return core.BrowseSeekable(q) }

// seekColumn is the column Browse orders q by.
func seekColumn(q core.BrowseQuery) string {
	if q.Sort == core.SortAdded && q.Filter.Type == core.TypeShow {
		return "t.show_added_ts"
	}
	return sortColumn("t", q.Sort)
}

// seekPage reads the page that starts offset rows after AfterKey in the
// sort order, without visiting the rows before it: the rest of AfterKey's
// tie (same sort value, larger key) and then the rows past its value, each
// an index range of the sort column.
func (c *Catalog) seekPage(ctx context.Context, q core.BrowseQuery, where string, args []any, offset, limit int) ([]core.Title, error) {
	col := seekColumn(q)
	var value any
	err := c.db.QueryRowContext(ctx, `SELECT `+col+` FROM catalog_titles t WHERE t.key=? AND `+where, append([]any{int64(q.AfterKey)}, args...)...).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) || err == nil && value == nil {
		return nil, ErrSeekUnavailable
	}
	if err != nil {
		return nil, err
	}
	dir, past := "ASC", ">"
	if q.Desc {
		dir, past = "DESC", "<"
	}
	order := col + " " + dir + ", t.key ASC"
	want := offset + limit
	var items []core.Title
	for _, r := range []struct {
		pred string
		args []any
	}{
		{" AND " + col + "=? AND t.key>?", []any{value, int64(q.AfterKey)}},
		{" AND " + col + past + "?", []any{value}},
	} {
		if len(items) >= want {
			break
		}
		page, err := c.queryPagedTitles(ctx, `SELECT `+prefixedCols("t")+` FROM catalog_titles t WHERE `+where+r.pred+` ORDER BY `+order+` LIMIT ?`, append(append(append([]any{}, args...), r.args...), want-len(items))...)
		if err != nil {
			return nil, err
		}
		items = append(items, page...)
	}
	if offset >= len(items) {
		return []core.Title{}, nil
	}
	items = items[offset:min(want, len(items))]
	if q.Sort == core.SortAdded {
		if err := applyAirAddedTimes(ctx, c.db, items, c.now()); err != nil {
			return nil, err
		}
	}
	return items, nil
}
