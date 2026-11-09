package catalog

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Titles returns hydrated, released, non-adult mirror entries in requested order. It never
// fetches remote metadata, so collection serving stays bounded and offline.
func (m *Mirror) Titles(ctx context.Context, keys []core.RatingKey) ([]core.Title, error) {
	return m.titles(ctx, keys, false)
}

// TitlesIncludingFuture is a local metadata read for collection-scoped
// authorization. Its caller must enforce membership and viewer policy.
func (m *Mirror) TitlesIncludingFuture(ctx context.Context, keys []core.RatingKey) ([]core.Title, error) {
	return m.titles(ctx, keys, true)
}

// ValidKeys filters keys to stored, non-adult mirror entries matching Titles
// (or TitlesIncludingFuture when includeFuture is true) in requested order,
// without hydrating facet or credit join tables.
func (m *Mirror) ValidKeys(ctx context.Context, keys []core.RatingKey, includeFuture bool) ([]core.RatingKey, error) {
	if len(keys) == 0 {
		return []core.RatingKey{}, nil
	}
	now := m.now()
	valid := make(map[core.RatingKey]bool, len(keys))
	const chunk = 400
	for start := 0; start < len(keys); start += chunk {
		end := start + chunk
		if end > len(keys) {
			end = len(keys)
		}
		part := keys[start:end]
		args := make([]any, len(part))
		for i, k := range part {
			args[i] = int64(k)
		}
		ph := placeholders(len(part))
		rows, err := m.db.QueryContext(ctx, `SELECT key,type,released_ts,adult,
			COALESCE((SELECT h.checked FROM catalog_home_release h WHERE h.key=catalog_titles.key),0),
			COALESCE((SELECT h.earliest_ts FROM catalog_home_release h WHERE h.key=catalog_titles.key),0)
			FROM catalog_titles WHERE key IN (`+ph+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var key, released, home int64
			var adult, checked int
			var typ string
			if err := rows.Scan(&key, &typ, &released, &adult, &checked, &home); err != nil {
				rows.Close()
				return nil, err
			}
			var t core.Title
			t.Key = core.RatingKey(key)
			t.Type = core.MediaType(typ)
			t.Adult = adult != 0
			t.HomeReleaseChecked = checked != 0
			if home != 0 {
				t.HomeReleased = time.Unix(home, 0).UTC()
			}
			if released != 0 {
				t.Released = time.Unix(released, 0).UTC()
			}
			if !t.Adult && (t.IsReleased(now) || includeFuture && !t.Released.IsZero()) {
				valid[t.Key] = true
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out := make([]core.RatingKey, 0, len(keys))
	for _, k := range keys {
		if valid[k] {
			out = append(out, k)
		}
	}
	return out, nil
}

func (m *Mirror) titles(ctx context.Context, keys []core.RatingKey, includeFuture bool) ([]core.Title, error) {
	if len(keys) == 0 {
		return []core.Title{}, nil
	}
	byKey := make(map[core.RatingKey]*core.Title, len(keys))
	const chunk = 400
	for start := 0; start < len(keys); start += chunk {
		end := start + chunk
		if end > len(keys) {
			end = len(keys)
		}
		part := keys[start:end]
		args := make([]any, len(part))
		for i, k := range part {
			args[i] = int64(k)
		}
		ph := placeholders(len(part))
		rows, err := m.db.QueryContext(ctx, `SELECT key,type,tmdb_id,imdb,tvdb,title,original_title,sort_title,tagline,summary,year,
			released_ts,runtime_sec,status,language,content_rating,rating,vote_count,popularity,poster,backdrop,logo,
			collection_id,collection_name,child_count,leaf_count,fetched_at,adult,last_air_ts,
   COALESCE((SELECT h.checked FROM catalog_home_release h WHERE h.key=catalog_titles.key),0),
   COALESCE((SELECT h.earliest_ts FROM catalog_home_release h WHERE h.key=catalog_titles.key),0),
   COALESCE((SELECT h.fallback_admitted FROM catalog_home_release h WHERE h.key=catalog_titles.key),0)
			FROM catalog_titles WHERE key IN (`+ph+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var t core.Title
			var key, released, runtime, fetched, lastAir int64
			var adult, checked, fallback int
			var home int64
			var typ string
			if err := rows.Scan(&key, &typ, &t.IDs.TMDB, &t.IDs.IMDB, &t.IDs.TVDB, &t.Title, &t.OriginalTitle, &t.SortTitle,
				&t.Tagline, &t.Summary, &t.Year, &released, &runtime, &t.Status, &t.Language, &t.ContentRating,
				&t.Rating, &t.VoteCount, &t.Popularity, &t.PosterPath, &t.BackdropPath, &t.LogoPath,
				&t.CollectionID, &t.CollectionName, &t.ChildCount, &t.LeafCount, &fetched, &adult, &lastAir, &checked, &home, &fallback); err != nil {
				rows.Close()
				return nil, err
			}
			t.Key = core.RatingKey(key)
			t.Type = core.MediaType(typ)
			t.Adult = adult != 0
			t.HomeReleaseChecked = checked != 0
			t.HomeReleaseFallbackAdmitted = fallback != 0
			if home != 0 {
				t.HomeReleased = time.Unix(home, 0).UTC()
			}
			if released != 0 {
				t.Released = time.Unix(released, 0).UTC()
			}
			if lastAir != 0 {
				t.LastAirDate = time.Unix(lastAir, 0).UTC()
			}
			t.Runtime = time.Duration(runtime) * time.Second
			if fetched != 0 {
				t.UpdatedAt = time.Unix(fetched, 0).UTC()
			}
			tt := t
			byKey[t.Key] = &tt
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		pair := func(q string, apply func(t *core.Title, v string)) error {
			rows, err := m.db.QueryContext(ctx, q, args...)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var k int64
				var v string
				if err := rows.Scan(&k, &v); err != nil {
					return err
				}
				if t := byKey[core.RatingKey(k)]; t != nil {
					apply(t, v)
				}
			}
			return rows.Err()
		}
		if err := pair(`SELECT key,genre FROM catalog_genres WHERE key IN (`+ph+`) ORDER BY genre`, func(t *core.Title, v string) { t.Genres = append(t.Genres, v) }); err != nil {
			return nil, err
		}
		if err := pair(`SELECT key,keyword FROM catalog_keywords WHERE key IN (`+ph+`) ORDER BY keyword`, func(t *core.Title, v string) { t.Keywords = append(t.Keywords, v) }); err != nil {
			return nil, err
		}
		if err := pair(`SELECT key,studio FROM catalog_studios WHERE key IN (`+ph+`) ORDER BY studio`, func(t *core.Title, v string) { t.Studios = append(t.Studios, v) }); err != nil {
			return nil, err
		}
		if err := pair(`SELECT key,country FROM catalog_countries WHERE key IN (`+ph+`) ORDER BY country`, func(t *core.Title, v string) { t.Countries = append(t.Countries, v) }); err != nil {
			return nil, err
		}
		crows, err := m.db.QueryContext(ctx, `SELECT c.key,c.person_tmdb,c.name,c.role,c.kind,c.thumb,COALESCE(p.imdb,''),c.provider_id FROM catalog_credits c LEFT JOIN catalog_people p ON p.person_tmdb=c.person_tmdb WHERE c.key IN (`+ph+`) ORDER BY c.kind,c.ord`, args...)
		if err != nil {
			return nil, err
		}
		for crows.Next() {
			var k, pid int64
			var name, role, kind, thumb, imdb, provider string
			if err := crows.Scan(&k, &pid, &name, &role, &kind, &thumb, &imdb, &provider); err != nil {
				crows.Close()
				return nil, err
			}
			t := byKey[core.RatingKey(k)]
			if t == nil {
				continue
			}
			p := core.Person{TMDB: pid, IMDB: imdb, ID: provider, Name: name, Role: role, ThumbPath: thumb}
			switch kind {
			case "director":
				t.Directors = append(t.Directors, p)
			case "writer":
				t.Writers = append(t.Writers, p)
			case "crew":
				t.Crew = append(t.Crew, p)
			default:
				t.Cast = append(t.Cast, p)
			}
		}
		crows.Close()
		if err := crows.Err(); err != nil {
			return nil, err
		}
	}
	counted := make([]*core.Title, 0, len(byKey))
	for _, t := range byKey {
		counted = append(counted, t)
	}
	if err := applyAiredCounts(ctx, m.db, m.now(), counted); err != nil {
		return nil, err
	}
	out := make([]core.Title, 0, len(keys))
	for _, k := range keys {
		if t := byKey[k]; t != nil && !t.Adult && (t.IsReleased(m.now()) || includeFuture && !t.Released.IsZero()) {
			out = append(out, *t)
		}
	}
	if err := applyAirAddedTimes(ctx, m.db, out, m.now()); err != nil {
		return nil, err
	}
	return out, nil
}
