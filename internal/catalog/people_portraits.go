package catalog

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/store"
)

// CachedPortraitTitles reads fresh visibility facts and the requested credits,
// never the minute cache. Movie/show portraits need no episode counts, trailers,
// alternate names or other people's credits. Child titles keep the full reader
// because their visibility can inherit parent facts. The caller owns the policy
// lease and must repeat this read and fence changes before publishing results.
func (c *Catalog) CachedPortraitTitles(ctx context.Context, subjects map[core.RatingKey][]int64, cfg config.Catalog, now time.Time) (map[core.RatingKey]core.Title, error) {
	filter, err := libraryfilter.CompilePolicy(cfg.Exclusions, cfg.VisibilityDefault)
	if err != nil {
		return nil, err
	}
	var keys, children []core.RatingKey
	for key := range subjects {
		if key.Kind() == core.KindMovie || key.Kind() == core.KindShow {
			keys = append(keys, key)
		} else {
			children = append(children, key)
		}
	}
	slices.Sort(keys)
	out := make(map[core.RatingKey]core.Title, len(subjects))
	if len(children) > 0 {
		out, err = c.CachedCreditTitles(ctx, children, cfg, now)
		if err != nil {
			return nil, err
		}
	}
	for part := range store.Chunks(keys, 100) {
		args := make([]any, len(part))
		for i, k := range part {
			args[i] = int64(k)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE key IN (`+placeholders(len(args))+`)`, args...)
		if err != nil {
			return nil, err
		}
		var items []core.Title
		for rows.Next() {
			st, err := scanTitle(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			items = append(items, st.t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		// Always include policy facets, even when the catalogue's own filter is
		// passive: the viewer's owned safety policy can still depend on them.
		if err := c.hydrateVisibilityFacts(ctx, items); err != nil {
			return nil, err
		}
		for _, title := range items {
			upcoming := cfg.ShowUpcomingMovies && title.Type == core.TypeMovie && !title.Released.IsZero() && title.Released.After(now)
			if !c.cachedListVisible(&title) || title.Adult || (!upcoming && !title.IsReleased(now)) || (!c.isTitleOrShowOwnerIncluded(&title) && filter.Hidden(&title)) || (!upcoming && cfg.ReleaseMode == config.ModeDigital && !homeReleaseVisible(&title, now)) {
				continue
			}
			out[title.Key] = title
		}
	}
	// Exact pairs keep work independent of unrelated credits on the same title.
	var pairs []personKey
	for _, key := range keys {
		if _, ok := out[key]; !ok {
			continue
		}
		seen := map[int64]bool{}
		for _, id := range subjects[key] {
			if id > 0 && !seen[id] {
				pairs = append(pairs, personKey{id, key})
				seen[id] = true
			}
		}
	}
	for part := range store.Chunks(pairs, 100) {
		var queries []string
		var args []any
		for _, pair := range part {
			queries = append(queries, `SELECT cr.key, cr.person_tmdb, cr.kind, cr.name, cr.role, cr.thumb, cr.provider_id, COALESCE(p.imdb,'') AS imdb, cr.ord FROM catalog_credits cr LEFT JOIN catalog_people p ON p.person_tmdb=cr.person_tmdb WHERE cr.key=? AND cr.person_tmdb=?`)
			args = append(args, int64(pair.key), pair.person)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT * FROM (`+strings.Join(queries, ` UNION ALL `)+`) AS credits ORDER BY key,kind,ord`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key core.RatingKey
			var person core.Person
			var kind string
			var ord int
			if err := rows.Scan(&key, &person.TMDB, &kind, &person.Name, &person.Role, &person.ThumbPath, &person.ID, &person.IMDB, &ord); err != nil {
				rows.Close()
				return nil, err
			}
			title := out[key]
			switch kind {
			case "director":
				title.Directors = append(title.Directors, person)
			case "writer":
				title.Writers = append(title.Writers, person)
			case "crew":
				title.Crew = append(title.Crew, person)
			default:
				title.Cast = append(title.Cast, person)
			}
			out[key] = title
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
