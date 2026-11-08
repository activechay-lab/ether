package catalog

import (
	"context"
	"errors"

	"github.com/braedonsaunders/ether/internal/core"
)

// Artwork for titles the catalogue has not stored.
//
// Search and list hits (TMDB results, and long-tail hits) are summaries in
// memory, not rows: a row is written when the title is opened. Their
// artwork must still be grantable and servable, or every frontend that
// signs images from stored rows shows them without a poster. ArtworkTitle
// and ArtworkTitles are CachedGet and CachedGetMany plus that memory, with
// the same visibility gates. They never fetch and never write.

// summaryTitle is a title known in memory without a row: a list or search
// summary, else what a poster lookup learned.
func (c *Catalog) summaryTitle(key core.RatingKey) (core.Title, bool) {
	if key.Kind() != core.KindMovie && key.Kind() != core.KindShow {
		return core.Title{}, false
	}
	if t, ok := c.listSummary(key); ok {
		if t.PosterPath == "" {
			if info, ok := c.posters.get(key, c.now()); ok {
				t.PosterPath = info.Path
			}
		}
		return t, true
	}
	if info, ok := c.posters.get(key, c.now()); ok && info.title.Key == key {
		return info.title, true
	}
	return core.Title{}, false
}

// ArtworkTitle is CachedGet that also admits a title held only in memory.
func (c *Catalog) ArtworkTitle(ctx context.Context, key core.RatingKey) (*core.Title, error) {
	stored, err := c.loadByKey(ctx, key)
	if err == nil {
		if !c.visibleTitle(&stored.t) {
			return nil, core.ErrNotFound
		}
		return &stored.t, nil
	}
	if !errors.Is(err, core.ErrNotFound) {
		return nil, err
	}
	t, ok := c.summaryTitle(key)
	if !ok || !c.visibleTitle(&t) {
		return nil, core.ErrNotFound
	}
	return &t, nil
}

// ArtworkTitles is CachedGetMany that also admits titles held only in
// memory.
func (c *Catalog) ArtworkTitles(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
	out, err := c.CachedGetMany(ctx, keys)
	if err != nil {
		return nil, err
	}
	for _, key := range keys {
		if _, ok := out[key]; ok {
			continue
		}
		if t, ok := c.summaryTitle(key); ok && c.cachedListVisible(&t) {
			out[key] = t
		}
	}
	return out, nil
}

// MemoryTitle tells an artwork reader how to read key without promoting it.
// stored reports a row (read it as usual); otherwise t is the title held in
// memory, or nil when the catalogue knows nothing of key. Reading a
// search hit's thumbnail must not store the hit: only opening it does.
func (c *Catalog) MemoryTitle(ctx context.Context, key core.RatingKey) (t *core.Title, stored bool, err error) {
	if _, err := c.loadByKey(ctx, key); err == nil {
		return nil, true, nil
	} else if !errors.Is(err, core.ErrNotFound) {
		return nil, false, err
	}
	if mem, ok := c.summaryTitle(key); ok {
		return &mem, false, nil
	}
	return nil, false, nil
}
