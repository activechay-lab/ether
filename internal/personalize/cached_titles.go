package personalize

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

// mirroredTitles opportunistically loads local history metadata in batches.
// Missing entries still follow Catalog.Get; failures retain the ordinary path.
func mirroredTitles(ctx context.Context, cat core.Catalog, keys []core.RatingKey) map[core.RatingKey]core.Title {
	out := map[core.RatingKey]core.Title{}
	seen := map[core.RatingKey]bool{}
	unique := make([]core.RatingKey, 0, len(keys))
	for _, k := range keys {
		if !seen[k] {
			seen[k] = true
			unique = append(unique, k)
		}
	}
	keys = unique
	if c, ok := cat.(core.CachedTitleSource); ok {
		if ts, err := c.CachedTitles(ctx, keys); err == nil {
			for _, t := range ts {
				out[t.Key] = t
			}
		}
	}
	return out
}
func historyTitle(ctx context.Context, cat core.Catalog, cached map[core.RatingKey]core.Title, key core.RatingKey) (*core.Title, error) {
	if t, ok := cached[key]; ok {
		return &t, nil
	}
	return cat.Get(ctx, key)
}

// preloadTitles reads keys' catalogue titles in batches before a loop that
// Gets them one at a time: a few statements for the whole set instead of
// several per title. Each Get then answers from the catalogue's cache and
// keeps its own freshness and visibility rules. Catalogues without a
// batched read (test fakes) are left to Get.
func preloadTitles(ctx context.Context, cat core.Catalog, keys []core.RatingKey) {
	if len(keys) < 2 {
		return
	}
	if p, ok := cat.(interface {
		PreloadStored(context.Context, []core.RatingKey)
	}); ok {
		p.PreloadStored(ctx, keys)
	}
}
