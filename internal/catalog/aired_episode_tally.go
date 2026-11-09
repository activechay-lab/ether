package catalog

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// AiredEpisodeTallies counts, for each show, the aired episodes
// CachedAiredEpisodes lists and how many of them userID has played (a
// watch row with a view), by season, without reading the episodes. Aired
// episodes released after settled, or named in judge, are not counted:
// their keys are returned for the caller to judge one by one. A zero
// settled counts every aired episode. One statement per airedManyBatch
// shows.
func (c *Catalog) AiredEpisodeTallies(ctx context.Context, userID int64, shows []core.RatingKey, settled time.Time, judge []core.RatingKey) (map[core.RatingKey]core.AiredEpisodeTally, error) {
	out := make(map[core.RatingKey]core.AiredEpisodeTally, len(shows))
	var keys []core.RatingKey
	for _, show := range shows {
		if show == 0 || show.Kind() != core.KindShow {
			return nil, errors.New("catalog: invalid aired episode query")
		}
		if _, ok := out[show]; !ok {
			out[show] = core.AiredEpisodeTally{Seasons: map[int]core.EpisodeTally{}}
			keys = append(keys, show)
		}
	}
	// Episode keys carry their show's TMDB id.
	judged := map[core.RatingKey][]any{}
	for _, key := range judge {
		if key.Kind() != core.KindEpisode {
			continue
		}
		if show := core.ShowKey(key.TMDB()); out[show].Seasons != nil {
			judged[show] = append(judged[show], int64(key))
		}
	}
	asOf := c.now().Unix()
	settle := asOf
	if !settled.IsZero() && settled.Unix() < settle {
		settle = settled.Unix()
	}
	const aired = `e.type='episode' AND e.adult=0 AND e.release_known=1 AND e.released_ts<=?`
	for part := range store.Chunks(keys, airedManyBatch) {
		showArgs := make([]any, 0, len(part))
		var named []any
		for _, show := range part {
			showArgs = append(showArgs, int64(show))
			named = append(named, judged[show]...)
		}
		in := placeholders(len(showArgs))
		counted, listed := ``, ``
		if len(named) > 0 {
			counted = ` AND e.key NOT IN (` + placeholders(len(named)) + `)`
			listed = ` OR e.key IN (` + placeholders(len(named)) + `)`
		}
		args := append([]any{userID, asOf}, showArgs...)
		args = append(append(args, settle), named...)
		args = append(append(args, asOf), showArgs...)
		args = append(append(args, settle), named...)
		rows, err := c.db.QueryContext(ctx, `SELECT e.show_key, e.season, CAST(0 AS BIGINT), COUNT(*), COUNT(w.rating_key)
FROM catalog_titles e
LEFT JOIN state_watch w ON w.user_id=? AND w.rating_key=e.key AND w.view_count>0
WHERE `+aired+` AND e.show_key IN (`+in+`) AND e.released_ts<=?`+counted+`
GROUP BY e.show_key, e.season
UNION ALL
SELECT e.show_key, e.season, e.key, 1, 0
FROM catalog_titles e
WHERE `+aired+` AND e.show_key IN (`+in+`) AND (e.released_ts>?`+listed+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var show, key int64
			var season, n, played int
			if err := rows.Scan(&show, &season, &key, &n, &played); err != nil {
				rows.Close()
				return nil, err
			}
			tally, ok := out[core.RatingKey(show)]
			if !ok {
				continue
			}
			tally.Total += n
			if key != 0 {
				tally.Judge = append(tally.Judge, core.RatingKey(key))
			} else {
				tally.Seasons[season] = core.EpisodeTally{Aired: n, Played: played}
			}
			out[core.RatingKey(show)] = tally
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
