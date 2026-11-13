package catalog

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// AiredShowEpisodes returns all aired, non-adult episode keys stored for the
// given show keys, using the indexed show/aired columns without network calls
// or full-library scans. A premiered regular season whose episodes are not
// stored yet contributes episodes 1..its published episode count, so a show
// is judged from its season summary before its episode list is loaded. That
// count can include announced episodes of an airing season, which only ever
// keeps a show visible, never hides one early.
func (c *Catalog) AiredShowEpisodes(ctx context.Context, shows []core.RatingKey) (map[core.RatingKey][]core.RatingKey, error) {
	out := make(map[core.RatingKey][]core.RatingKey, len(shows))
	if c == nil || c.db == nil || len(shows) == 0 {
		return out, nil
	}
	unique := make([]core.RatingKey, 0, len(shows))
	seen := make(map[core.RatingKey]struct{}, len(shows))
	for _, k := range shows {
		if k.Kind() != core.KindShow {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			unique = append(unique, k)
		}
	}
	if len(unique) == 0 {
		return out, nil
	}
	now := c.now().Unix()
	const chunk = 200
	for start := 0; start < len(unique); start += chunk {
		part := unique[start:min(start+chunk, len(unique))]
		args := make([]any, 0, len(part)+1)
		args = append(args, now)
		for _, k := range part {
			args = append(args, int64(k))
		}
		rows, err := c.db.QueryContext(ctx, `SELECT show_key, key
FROM catalog_titles
WHERE `+airedTypeColumn(c.db)+`='episode' AND adult=0 AND release_known=1 AND released_ts<=?
AND season>=0 AND episode>0 AND show_key IN (`+placeholders(len(part))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var showKey, epKey int64
			if err := rows.Scan(&showKey, &epKey); err != nil {
				rows.Close()
				return nil, err
			}
			sk := core.RatingKey(showKey)
			out[sk] = append(out[sk], core.RatingKey(epKey))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		// Seasons with no stored episodes count their summary's episode
		// total. Two show_key-indexed reads joined here: a correlated
		// NOT EXISTS let SQLite scan every season row (2 minutes on a
		// 50k-title library).
		keyArgs := args[1:]
		stored := map[[2]int64]bool{}
		rows, err = c.db.QueryContext(ctx, `SELECT DISTINCT show_key, season FROM catalog_titles
WHERE `+airedTypeColumn(c.db)+`='episode' AND show_key IN (`+placeholders(len(part))+`)`, keyArgs...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var sk, season int64
			if err := rows.Scan(&sk, &season); err != nil {
				rows.Close()
				return nil, err
			}
			stored[[2]int64{sk, season}] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		rows, err = c.db.QueryContext(ctx, `SELECT show_key, season, child_count, release_known, released_ts FROM catalog_titles
WHERE `+airedTypeColumn(c.db)+`='season' AND show_key IN (`+placeholders(len(part))+`)`, keyArgs...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var showKey, season, count, known, released int64
			if err := rows.Scan(&showKey, &season, &count, &known, &released); err != nil {
				rows.Close()
				return nil, err
			}
			if season <= 0 || count <= 0 || known != 1 || released > now || stored[[2]int64{showKey, season}] {
				continue
			}
			sk := core.RatingKey(showKey)
			for ep := 1; ep <= int(min(count, 2000)); ep++ {
				out[sk] = append(out[sk], core.EpisodeKey(sk.TMDB(), int(season), ep))
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
