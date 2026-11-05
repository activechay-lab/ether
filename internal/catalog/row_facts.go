package catalog

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// LoadRowFacts is a bounded read at row assembly, never a visibility predicate.
// Warm canonical titles reuse the catalogue cache. Each cold chunk reads release,
// home-release and mandatory safety facts together with indexed key lookups.
// Missing remote summaries remain in memory until background mirroring succeeds.
func (c *Catalog) LoadRowFacts(ctx context.Context, titles []core.Title) []core.Title {
	if len(titles) == 0 {
		return titles
	}
	facts := map[core.RatingKey]core.Title{}
	var need []core.Title
	seen := map[core.RatingKey]bool{}
	gen, minute := c.writeGen.Load(), c.minuteBucket()
	for _, t := range titles {
		if t.Type != core.TypeMovie && t.Type != core.TypeShow || seen[t.Key] {
			continue
		}
		seen[t.Key] = true
		if cached, ok := c.cachedStored(t.Key, gen, minute); ok {
			facts[t.Key] = cached
		} else {
			need = append(need, t)
		}
	}
	for chunk := range store.Chunks(need, 200) {
		args := keyArgs(chunk)
		rows, err := c.db.QueryContext(ctx, `SELECT t.key,t.released_ts,t.year,t.adult,t.content_rating,COALESCE(h.checked,0),COALESCE(h.earliest_ts,0),COALESCE(h.fallback_admitted,0) FROM catalog_titles t LEFT JOIN catalog_home_release h ON h.key=t.key WHERE t.key IN (`+placeholders(len(args))+`)`, args...)
		if err != nil {
			return nil
		}
		for rows.Next() {
			var t core.Title
			var released, home int64
			var adult, checked, fallback int
			if err := rows.Scan(&t.Key, &released, &t.Year, &adult, &t.ContentRating, &checked, &home, &fallback); err != nil {
				rows.Close()
				return nil
			}
			if released != 0 {
				t.Released = time.Unix(released, 0).UTC()
			}
			if home != 0 {
				t.HomeReleased = time.Unix(home, 0).UTC()
			}
			t.Adult = adult != 0
			t.HomeReleaseChecked = checked != 0
			t.HomeReleaseFallbackAdmitted = fallback != 0
			facts[t.Key] = t
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil
		}
	}
	var missing []core.Title
	for _, t := range need {
		if _, ok := facts[t.Key]; !ok {
			missing = append(missing, t)
		}
	}
	c.RememberListSummaries(ctx, missing)
	out := make([]core.Title, 0, len(titles))
	for _, t := range titles {
		if f, ok := facts[t.Key]; ok {
			t.Released = f.Released
			t.Year = f.Year
			t.Adult = f.Adult
			t.ContentRating = f.ContentRating
			t.HomeReleased = f.HomeReleased
			t.HomeReleaseChecked = f.HomeReleaseChecked
			t.HomeReleaseFallbackAdmitted = f.HomeReleaseFallbackAdmitted
		} else if t.Type == core.TypeMovie || t.Type == core.TypeShow {
			if _, ok := c.listSummary(t.Key); !ok {
				continue
			}
		}
		out = append(out, t)
	}
	return out
}
