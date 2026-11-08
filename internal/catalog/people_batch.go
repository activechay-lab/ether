package catalog

import (
	"context"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/libraryfilter"
	"github.com/braedonsaunders/ether/internal/store"
)

// A people search checks, for each person whose name matches, that the
// viewer can see at least one title crediting them. Per person that was a
// credit query, a full title read (every credit of every title) and a
// re-read of the portrait title: a statement set per matching person,
// growing with the library. These read a page of people in a few
// statements, and only the facts a visibility decision needs.

// peopleBatch bounds the people one credits statement covers.
const peopleBatch = 25

// PersonCreditsMany is PersonCreditKeys for several people at once: each
// person's most popular credited titles of the given types (at most limit
// each), with the person's credit rows on them, most popular first.
func (c *Catalog) PersonCreditsMany(ctx context.Context, persons []int64, types []core.MediaType, limit int) (map[int64][]core.PersonCredit, error) {
	out := map[int64][]core.PersonCredit{}
	if len(persons) == 0 || len(types) == 0 || limit <= 0 {
		return out, nil
	}
	for part := range store.Chunks(persons, peopleBatch) {
		var parts []string
		var args []any
		for _, p := range part {
			if p <= 0 {
				continue
			}
			q, a := personCreditKeysSQL(p, types, limit)
			parts = append(parts, `SELECT CAST(? AS BIGINT) AS person, key, popularity FROM (`+q+`) AS p`)
			args = append(append(args, p), a...)
		}
		if len(parts) == 0 {
			continue
		}
		rows, err := c.db.QueryContext(ctx, strings.Join(parts, "\nUNION ALL\n"), args...)
		if err != nil {
			return nil, err
		}
		order := map[int64][]core.RatingKey{}
		var keys []any
		seenKey := map[int64]bool{}
		for rows.Next() {
			var person, key int64
			var pop float64
			if err := rows.Scan(&person, &key, &pop); err != nil {
				rows.Close()
				return nil, err
			}
			order[person] = append(order[person], core.RatingKey(key))
			if !seenKey[key] {
				seenKey[key] = true
				keys = append(keys, key)
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
		if len(keys) == 0 {
			continue
		}
		credits, err := c.personCreditRows(ctx, part, keys)
		if err != nil {
			return nil, err
		}
		for person, ks := range order {
			for _, k := range ks {
				out[person] = append(out[person], credits[personKey{person, k}]...)
			}
		}
	}
	return out, nil
}

type personKey struct {
	person int64
	key    core.RatingKey
}

// personCreditRows reads the credit rows of persons on keys (the
// catalog_credits_person index: a person's own credits).
func (c *Catalog) personCreditRows(ctx context.Context, persons []int64, keys []any) (map[personKey][]core.PersonCredit, error) {
	args := make([]any, 0, len(persons)+len(keys))
	for _, p := range persons {
		args = append(args, p)
	}
	args = append(args, keys...)
	rows, err := c.db.QueryContext(ctx, `SELECT cr.person_tmdb, cr.key, cr.kind, cr.name, cr.role, cr.thumb, cr.provider_id, COALESCE(p.imdb,''), cr.ord
		FROM catalog_credits cr LEFT JOIN catalog_people p ON p.person_tmdb=cr.person_tmdb
		WHERE cr.person_tmdb IN (`+placeholders(len(persons))+`) AND cr.key IN (`+placeholders(len(keys))+`)
		ORDER BY cr.person_tmdb, cr.key, cr.kind, cr.ord`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[personKey][]core.PersonCredit{}
	for rows.Next() {
		var person, key, ord int64
		var cr core.PersonCredit
		if err := rows.Scan(&person, &key, &cr.Kind, &cr.Person.Name, &cr.Person.Role, &cr.Person.ThumbPath, &cr.Person.ID, &cr.Person.IMDB, &ord); err != nil {
			return nil, err
		}
		cr.Key, cr.Person.TMDB = core.RatingKey(key), person
		out[personKey{person, cr.Key}] = append(out[personKey{person, cr.Key}], cr)
	}
	return out, rows.Err()
}

// CachedVisibleTitles is CachedCreditTitles for a visibility decision only:
// the stored rows, plus the facts visibility rules and the release gate
// read when they read any, without credits or alternative titles.
func (c *Catalog) CachedVisibleTitles(ctx context.Context, keys []core.RatingKey, cfg config.Catalog, now time.Time) (map[core.RatingKey]core.Title, error) {
	filter, err := libraryfilter.CompilePolicy(cfg.Exclusions, cfg.VisibilityDefault)
	if err != nil {
		return nil, err
	}
	digital := cfg.ReleaseMode == config.ModeDigital
	out := make(map[core.RatingKey]core.Title, len(keys))
	for part := range store.Chunks(keys, 200) {
		args := make([]any, len(part))
		for i, k := range part {
			args[i] = int64(k)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE key IN (`+placeholders(len(part))+`)`, args...)
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
		if !filter.Passive() || digital {
			if err := c.hydrateVisibilityFacts(ctx, items); err != nil {
				return nil, err
			}
		}
		var seasons []*core.Title
		for i := range items {
			if items[i].Type == core.TypeSeason {
				seasons = append(seasons, &items[i])
			}
		}
		if len(seasons) > 0 {
			if err := applyAiredCounts(ctx, c.db, now, seasons); err != nil {
				return nil, err
			}
		}
		for i := range items {
			t := &items[i]
			upcoming := cfg.ShowUpcomingMovies && t.Type == core.TypeMovie && !t.Released.IsZero() && t.Released.After(now)
			if t.Adult || (!upcoming && !t.IsReleased(now)) || filter.Hidden(t) || (!upcoming && digital && !homeReleaseVisible(t, now)) || (t.Type == core.TypeSeason && !listableSeason(t, now)) {
				continue
			}
			out[t.Key] = *t
		}
	}
	return out, nil
}

// CachedVisibilityRows is CachedGetMany for a visibility decision: stored
// rows that pass the list gates, with the facts visibility rules read when
// they read any, without credits. The partial titles are not cached.
func (c *Catalog) CachedVisibilityRows(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
	out := make(map[core.RatingKey]core.Title, len(keys))
	needFacts := !c.visibilityFor(ctx).Passive() || c.config().ReleaseMode == config.ModeDigital
	for part := range store.Chunks(keys, 200) {
		args := make([]any, len(part))
		for i, k := range part {
			args[i] = int64(k)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE key IN (`+placeholders(len(part))+`)`, args...)
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
		if needFacts {
			if err := c.hydrateVisibilityFacts(ctx, items); err != nil {
				return nil, err
			}
		}
		for i := range items {
			if items[i].Type != core.TypeSeason && c.cachedListVisible(&items[i]) {
				out[items[i].Key] = items[i]
			}
		}
	}
	return out, nil
}
