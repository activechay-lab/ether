package catalog

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"time"
)

// ConsoleFacets never starts a library-sized count on the HTTP request. Values
// are a generation-stamped snapshot; pending tells the UI to retry progressively.
// A nil slice means no snapshot has completed yet; a non-nil empty slice is a
// valid cached result, including while its replacement is being refreshed.
func (c *Catalog) ConsoleFacets(ctx context.Context, typ core.MediaType, facet string) ([]core.FacetValue, bool, error) {
	switch facet {
	case "genre", "decade", "year", "studio", "country", "language", "keyword", "contentRating", "tag":
	default:
		return nil, false, fmt.Errorf("unknown facet")
	}
	f := c.viewerPaging(ctx, core.Filter{Type: typ})
	key := fmt.Sprintf("%+v|%s", f, facet)
	c.cacheMu.RLock()
	ent, ok := c.facetCache[key]
	c.cacheMu.RUnlock()
	pending := !ok || ent.gen != c.writeGen.Load() || ent.minute != c.minuteBucket()
	if pending {
		_ = c.hydrate(ctx, "console-facet:"+key, 0, func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			_, err := c.countFacet(ctx, f, facet)
			return err
		})
	}
	if !ok {
		return nil, pending, nil
	}
	return append([]core.FacetValue{}, ent.vals...), pending, nil
}

func init() {
	store.Register("catalog", 36, `CREATE INDEX catalog_titles_console_language_votes_page ON catalog_titles(type,browsable,adult,language,vote_count,rating DESC,key,release_known,released_ts);`)

	store.Register("catalog", 35, `
CREATE INDEX catalog_titles_console_pop_page ON catalog_titles(type,browsable,adult,popularity DESC,key,year,rating,runtime_sec,language,vote_count,release_known,released_ts);
CREATE INDEX catalog_titles_console_year_title_page ON catalog_titles(type,browsable,adult,year DESC,sort_title ASC,key,rating,runtime_sec,popularity,language,vote_count,release_known,released_ts);
`)

	// Identical B-tree/expression syntax on SQLite and PostgreSQL. These cover
	// selective numeric ranges and keyword matching without per-title probes.
	store.Register("catalog", 34, `
CREATE INDEX catalog_titles_console_votes ON catalog_titles(type,browsable,adult,vote_count,key);
CREATE INDEX catalog_titles_console_status ON catalog_titles(type,browsable,adult,status,popularity DESC,key);
CREATE INDEX catalog_keywords_console_lower ON catalog_keywords(LOWER(keyword),key);
`)
}

// ConsoleCount returns a bounded cache lookup and starts at most one background
// count per expression. Stateful counts expire after five seconds as state has
// a separate generation from catalogue metadata.
func (c *Catalog) ConsoleCount(ctx context.Context, f core.Filter) (int, bool, error) {
	f = c.viewerPaging(ctx, f)
	where, args, err := c.browseWhere(f)
	if err != nil {
		return 0, false, err
	}
	key := "console-count:" + c.consoleAvailabilityCacheKey(f) + "|" + browseFilterKey(f)
	c.cacheMu.RLock()
	ent, ok := c.countCache[key]
	c.cacheMu.RUnlock()
	gen, minute := c.writeGen.Load(), c.minuteBucket()
	pending := !ok || ent.gen != gen || ent.minute != minute || (groupHasState(f.Group) && time.Since(ent.counted) > 5*time.Second)
	if pending {
		_ = c.hydrate(ctx, key, 0, func(ctx context.Context) error {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			var total int
			if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_titles t WHERE "+where, args...).Scan(&total); err != nil {
				return err
			}
			c.cacheMu.Lock()
			defer c.cacheMu.Unlock()
			if len(c.countCache) >= 512 {
				c.countCache = map[string]cachedCount{}
			}
			c.countCache[key] = cachedCount{total: total, gen: gen, minute: minute, counted: time.Now()}
			return nil
		})
	}
	return ent.total, pending, nil
}
