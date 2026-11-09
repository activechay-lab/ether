package catalog

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// RecentAir returns browsable titles ordered by date added: a movie's release,
// or a show's latest episode already aired. It reads the released and aired
// indexes and stops after a bounded window. It does not refresh metadata.
// offset skips that many titles in the same order. A show with no aired
// episode is omitted. done is true when this window reached the end.
func (c *Catalog) RecentAir(ctx context.Context, typ core.MediaType, offset, limit int) ([]core.Title, bool, error) {
	return c.RecentAirExcluding(ctx, typ, nil, offset, limit)
}

// RecentAirExcluding is RecentAir without the titles in exclude: the
// evidence window's hidden titles, which a viewer never sees, so a page of
// them does not use up the bounded window.
func (c *Catalog) RecentAirExcluding(ctx context.Context, typ core.MediaType, exclude []core.RatingKey, offset, limit int) ([]core.Title, bool, error) {
	return c.recentAir(ctx, typ, exclude, core.Filter{}, offset, limit)
}

// RecentAirFiltered applies an availability decision before selecting the window.
func (c *Catalog) RecentAirFiltered(ctx context.Context, typ core.MediaType, f core.Filter, offset, limit int) ([]core.Title, bool, error) {
	return c.recentAir(ctx, typ, nil, f, offset, limit)
}

func (c *Catalog) recentAir(ctx context.Context, typ core.MediaType, exclude []core.RatingKey, f core.Filter, offset, limit int) ([]core.Title, bool, error) {
	if c == nil || c.db == nil || limit <= 0 {
		return nil, true, nil
	}
	if limit > 64 {
		limit = 64
	}
	if offset < 0 {
		offset = 0
	}
	if offset > 256 {
		return nil, true, nil
	}
	now := c.now().Unix()
	var keys []core.RatingKey
	var added []int64
	var err error
	switch typ {
	case core.TypeMovie:
		keys, added, err = c.recentMovieKeys(ctx, now, exclude, f, offset, limit)
	case core.TypeShow:
		keys, added, err = c.recentShowKeys(ctx, now, exclude, f, offset, limit)
	default:
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(keys) == 0 {
		return nil, true, nil
	}
	done := len(keys) < limit
	found, err := c.CachedGetMany(ctx, keys)
	if err != nil {
		return nil, false, err
	}
	out := make([]core.Title, 0, len(keys))
	for i, key := range keys {
		title, ok := found[key]
		if !ok || title.Key != key {
			continue
		}
		if added[i] != 0 {
			title.AddedAt = time.Unix(added[i], 0).UTC()
		}
		out = append(out, title)
	}
	return out, done, nil
}

func (c *Catalog) recentMovieKeys(ctx context.Context, now int64, exclude []core.RatingKey, f core.Filter, offset, limit int) ([]core.RatingKey, []int64, error) {
	args := []any{now}
	notIn := ""
	if f.HiddenMovieGeneration != "" {
		notIn += ` AND NOT EXISTS (SELECT 1 FROM availability_hidden ah WHERE ah.kind='movie' AND ah.generation=? AND ah.rating_key=catalog_titles.key)`
		args = append(args, f.HiddenMovieGeneration)
	}
	if f.DigitalDiscovery && f.HiddenMovieGeneration == "" {
		if f.DigitalDateEvidence {
			notIn += ` AND ((key>=? AND key<?) OR EXISTS (SELECT 1 FROM catalog_home_release dh WHERE dh.key=catalog_titles.key AND dh.checked=1 AND dh.earliest_ts<>0 AND dh.earliest_ts<=?))`
			args = append(args, int64(core.LocalKey(0)), int64(core.LocalKey(0))+1000000000000000, homeReleaseDeadline(time.Unix(now, 0).UTC(), f.HomeReleaseOffsetDays))
		} else {
			notIn += ` AND key>=? AND key<?`
			args = append(args, int64(core.LocalKey(0)), int64(core.LocalKey(0))+1000000000000000)
		}
	}
	// A very long list would pass the statement's variable limit; the
	// caller still filters, so the window only fills slower.
	if len(exclude) > 0 && len(exclude) <= 4000 {
		notIn += ` AND key NOT IN (` + placeholders(len(exclude)) + `)`
		for _, k := range exclude {
			args = append(args, int64(k))
		}
	}
	rows, err := c.db.QueryContext(ctx, `SELECT key, released_ts FROM catalog_titles
WHERE type='movie' AND browsable=1 AND adult=0 AND release_known=1 AND released_ts<=?`+notIn+`
ORDER BY released_ts DESC, key ASC
LIMIT ? OFFSET ?`, append(args, limit, offset)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	return scanAirKeys(rows)
}

func (c *Catalog) recentShowKeys(ctx context.Context, now int64, exclude []core.RatingKey, f core.Filter, offset, limit int) ([]core.RatingKey, []int64, error) {
	skip := make(map[int64]bool, len(exclude))
	for _, k := range exclude {
		skip[int64(k)] = true
	}
	// Newest aired episodes, then the first time each show appears. The
	// partial index is (released_ts DESC, key). The row cap keeps a show
	// with a burst of new episodes from turning this into a catalogue walk.
	bound := (offset + limit) * 40
	if bound > 4000 {
		bound = 4000
	}
	hidden := ""
	args := []any{now}
	if f.HiddenShowGeneration != "" {
		hidden = ` AND NOT EXISTS (SELECT 1 FROM availability_hidden ah WHERE ah.kind='show' AND ah.generation=? AND ah.rating_key=e.show_key)`
		args = append(args, f.HiddenShowGeneration)
	}
	rows, err := c.db.QueryContext(ctx, `SELECT e.show_key, e.released_ts FROM catalog_titles e
WHERE e.type='episode' AND e.adult=0 AND e.release_known=1 AND e.released_ts<=?
AND EXISTS (SELECT 1 FROM catalog_titles s WHERE s.key=e.show_key AND s.browsable=1 AND s.adult=0)
`+hidden+` ORDER BY e.released_ts DESC, e.key ASC
LIMIT ?`, append(args, bound)...)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	seen := map[int64]bool{}
	var keys []core.RatingKey
	var added []int64
	skipped := 0
	for rows.Next() {
		var show, aired int64
		if err := rows.Scan(&show, &aired); err != nil {
			return nil, nil, err
		}
		if show == 0 || seen[show] || skip[show] {
			continue
		}
		seen[show] = true
		if skipped < offset {
			skipped++
			continue
		}
		keys = append(keys, core.RatingKey(show))
		added = append(added, aired)
		if len(keys) == limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return keys, added, nil
}

func scanAirKeys(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]core.RatingKey, []int64, error) {
	var keys []core.RatingKey
	var added []int64
	for rows.Next() {
		var key, aired int64
		if err := rows.Scan(&key, &aired); err != nil {
			return nil, nil, err
		}
		keys = append(keys, core.RatingKey(key))
		added = append(added, aired)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	return keys, added, nil
}
