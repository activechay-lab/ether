package catalog

import (
	"context"
	"errors"

	"github.com/braedonsaunders/ether/internal/core"
)

// Artwork for titles the catalogue has not stored.
//
// Search and list hits (TMDB results and long-tail hits) are summaries in
// memory, not rows: a row is written when the title is opened. Their
// artwork must still be grantable and servable, or every frontend that
// signs images from stored rows shows them without a poster. ArtworkTitle
// and ArtworkTitles are CachedGet and CachedGetMany plus that memory, with
// the same visibility gates. They never fetch and never write.

// heldTitle is a movie or show known without a row: a list or search
// summary, else the long-tail entry, with what a poster lookup learned
// (poster, date) laid over it, else the lookup's own summary. dateUnknown
// reports a tail hit whose release date nobody has read yet: search lets
// those through, so their artwork must be served too.
func (c *Catalog) heldTitle(key core.RatingKey) (t core.Title, dateUnknown, ok bool) {
	if key.Kind() != core.KindMovie && key.Kind() != core.KindShow {
		return core.Title{}, false, false
	}
	info, looked := c.posters.get(key, c.now())
	if t, ok = c.listSummary(key); !ok {
		var e tailEntry
		if e, ok = c.tailEntry(key); ok {
			t, dateUnknown = e.Title, e.DateUnknown
		}
	}
	if !ok {
		if looked && info.title.Key == key {
			return info.title, false, true
		}
		return core.Title{}, false, false
	}
	if looked {
		info.applyTo(&t)
		dateUnknown = dateUnknown && t.Released.IsZero()
	}
	return t, dateUnknown, true
}

// summaryTitle is heldTitle without the date state.
func (c *Catalog) summaryTitle(key core.RatingKey) (core.Title, bool) {
	t, _, ok := c.heldTitle(key)
	return t, ok
}

// UndatedSearchHit reports a long-tail search hit with no row whose release
// date is not known yet. Search shows those, so artwork that requires a
// released title admits them.
func (c *Catalog) UndatedSearchHit(ctx context.Context, key core.RatingKey) bool {
	if _, err := c.loadByKey(ctx, key); !errors.Is(err, core.ErrNotFound) {
		return false
	}
	t, dateUnknown, ok := c.heldTitle(key)
	return ok && dateUnknown && !t.Adult
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
	t, dateUnknown, ok := c.heldTitle(key)
	if !ok || !(c.visibleTitle(&t) || (dateUnknown && !t.Adult)) {
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
		// A row CachedGetMany left out is hidden: memory must not bring
		// it back. Only keys with no row fall through (one key seek each,
		// for the page's search hits).
		if _, err := c.loadByKey(ctx, key); !errors.Is(err, core.ErrNotFound) {
			continue
		}
		if t, dateUnknown, ok := c.heldTitle(key); ok && (c.cachedListVisible(&t) || (dateUnknown && !t.Adult)) {
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

// PersistsArtwork reports whether facts derived from key's artwork
// (blurhashes, UltraBlur colours) may be written: yes for a stored title
// and for every key that is not a title; no for a movie, show, season or
// episode with no row. Search and list hits are read-only: their artwork
// facts stay in memory, or every search typed would add rows.
func (c *Catalog) PersistsArtwork(ctx context.Context, key core.RatingKey) bool {
	switch key.Kind() {
	case core.KindMovie, core.KindShow, core.KindSeason, core.KindEpisode:
	default:
		return true
	}
	_, err := c.loadByKey(ctx, key)
	return err == nil
}
