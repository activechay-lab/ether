package catalog

import (
	"context"
	"slices"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// PromoteTitles marks stored, released, non-adult movie and show rows in keys
// as browsable (the library tier), setting added_at when unset.
func (c *Catalog) PromoteTitles(ctx context.Context, keys []core.RatingKey) error {
	_, err := c.promoteTitles(ctx, keys)
	return err
}

func (c *Catalog) promoteTitles(ctx context.Context, keys []core.RatingKey) (int, error) {
	if c == nil || len(keys) == 0 {
		return 0, nil
	}
	seen := make(map[core.RatingKey]struct{}, len(keys))
	uniq := make([]core.RatingKey, 0, len(keys))
	for _, k := range keys {
		if k == 0 {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		uniq = append(uniq, k)
	}
	if len(uniq) == 0 {
		return 0, nil
	}
	slices.Sort(uniq)
	now := c.now().Unix()
	updated := 0
	for start := 0; start < len(uniq); start += 500 {
		chunk := uniq[start:min(start+500, len(uniq))]
		args := make([]any, 0, len(chunk)+3)
		args = append(args, now)
		for _, k := range chunk {
			args = append(args, int64(k))
		}
		args = append(args, now, now)
		res, err := c.db.ExecContext(ctx, `UPDATE catalog_titles
SET browsable = 1,
    added_at = CASE WHEN added_at = 0 THEN ? ELSE added_at END
WHERE key IN (`+placeholders(len(chunk))+`)
  AND browsable = 0
  AND adult = 0
  AND type IN ('movie', 'show')
  AND ((released_ts <> 0 AND released_ts <= ?) OR (type = 'movie' AND EXISTS (
    SELECT 1 FROM catalog_home_release h
    WHERE h.key = catalog_titles.key AND h.checked <> 0 AND h.earliest_ts <> 0 AND h.earliest_ts <= ?
  )))`, args...)
		if err != nil {
			return updated, err
		}
		if n, err := res.RowsAffected(); err == nil && n > 0 {
			updated += int(n)
		}
	}
	if updated > 0 {
		c.writeGen.Add(1)
	}
	return updated, nil
}

// promoteCollectionTouched promotes stored collection-referenced rows that were
// kept or re-fetched in the search-only tier (browsable=0) into the library tier.
func (c *Catalog) promoteCollectionTouched(ctx context.Context) (int, error) {
	tables, err := existingTables(ctx, c.db, store.DialectOf(c.db))
	if err != nil {
		return 0, err
	}
	sources := []touchSource{
		{table: "collections_members", column: "key", tierA: true},
		{table: "collections_specs", column: "spec_json", form: touchScan},
	}
	touched := newTierTouched()
	for _, src := range sources {
		if !tables[src.table] {
			continue
		}
		if err := readTouchSource(ctx, c.db, src, touched); err != nil {
			return 0, err
		}
	}
	if len(touched.keys) == 0 {
		return 0, nil
	}
	keys := make([]core.RatingKey, 0, len(touched.keys))
	for k := range touched.keys {
		keys = append(keys, k)
	}
	return c.promoteTitles(ctx, keys)
}
