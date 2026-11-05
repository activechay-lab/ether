package catalog

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// OpenableTitles prepares remote list summaries for the same admission gate as
// Get. Missing summaries are mirrored in one batch; opening the item still
// hydrates full details on demand. Known mirror facts always beat list data.
// It does not apply discovery-only preferences (saved collections retain those).
func (c *Catalog) OpenableTitles(ctx context.Context, titles []core.Title) []core.Title {
	if len(titles) == 0 {
		return titles
	}
	// Episode-order adapters own virtual seasons and episodes. Do not mirror
	// their presentation identities as catalogue records.
	input := titles
	titles = nil
	for _, title := range input {
		if title.Type == core.TypeMovie || title.Type == core.TypeShow {
			titles = append(titles, title)
		}
	}
	if len(titles) == 0 {
		return input
	}
	known := map[core.RatingKey]bool{}
	for chunk := range store.Chunks(titles, 200) {
		args := keyArgs(chunk)
		rows, err := c.db.QueryContext(ctx, `SELECT key FROM catalog_titles WHERE key IN (`+placeholders(len(args))+`)`, args...)
		if err != nil {
			return nil
		}
		for rows.Next() {
			var key core.RatingKey
			if err := rows.Scan(&key); err != nil {
				rows.Close()
				return nil
			}
			known[key] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil
		}
	}
	var missing []core.Title
	for _, title := range titles {
		if title.Key != 0 && !known[title.Key] {
			missing = append(missing, title)
			known[title.Key] = true
		}
	}
	if err := c.upsertSummaries(ctx, missing, false); err != nil {
		return nil
	}
	titles = c.reloadSummaries(ctx, titles)
	if err := c.hydrateHomeRelease(ctx, titles); err != nil {
		return nil
	}
	admitted := map[core.RatingKey]core.Title{}
	for _, title := range titles {
		if c.visibleTitle(&title) && !title.IsFutureRelease(c.now()) {
			admitted[title.Key] = title
		}
	}
	out := make([]core.Title, 0, len(input))
	for _, title := range input {
		if title.Type != core.TypeMovie && title.Type != core.TypeShow {
			out = append(out, title)
			continue
		}
		if canonical, ok := admitted[title.Key]; ok {
			out = append(out, canonical)
		}
	}
	return out
}
