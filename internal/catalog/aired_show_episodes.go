package catalog

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// AiredShowEpisodes returns all aired, non-adult episode keys stored for the
// given show keys, using the indexed show/aired columns without network calls
// or full-library scans.
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
WHERE type='episode' AND adult=0 AND release_known=1 AND released_ts<=?
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
	}
	return out, nil
}
