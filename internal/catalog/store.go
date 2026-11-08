package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

const titleCols = `key,type,tmdb_id,imdb,tvdb,show_key,show_tmdb,show_title,season_title,season,episode,
title,original_title,sort_title,tagline,summary,year,released_ts,runtime_sec,status,language,
content_rating,rating,vote_count,popularity,poster,backdrop,still,logo,collection_id,collection_name,
child_count,leaf_count,browsable,added_at,fetched_at,detail,adult,last_air_ts,content_rating_source`

type storedTitle struct {
	t         core.Title
	browsable bool
	fetchedAt time.Time
	detail    bool
	// detailAt is when the detail payload was last fetched (zero if never).
	detailAt time.Time
}

// extraScan appends trailing columns to a titleCols scan.
type extraScan struct {
	s     scanner
	extra []any
}

func (e extraScan) Scan(dest ...any) error { return e.s.Scan(append(dest, e.extra...)...) }

func cloneTitle(t core.Title) core.Title {
	t.Genres = append([]string(nil), t.Genres...)
	t.Keywords = append([]string(nil), t.Keywords...)
	t.AlternativeTitles = append([]string(nil), t.AlternativeTitles...)
	t.ShowAlternativeTitles = append([]string(nil), t.ShowAlternativeTitles...)
	t.Studios = append([]string(nil), t.Studios...)
	t.Countries = append([]string(nil), t.Countries...)
	t.Cast = append([]core.Person(nil), t.Cast...)
	t.Directors = append([]core.Person(nil), t.Directors...)
	t.Writers = append([]core.Person(nil), t.Writers...)
	t.Crew = append([]core.Person(nil), t.Crew...)
	return t
}

func cacheStoredTitle(kind core.KeyKind) bool {
	switch kind {
	case core.KindMovie, core.KindShow, core.KindSeason, core.KindEpisode:
		return true
	default:
		return false
	}
}

func (c *Catalog) minuteBucket() int64 {
	return c.now().Unix() / 60
}

func (c *Catalog) loadByKey(ctx context.Context, key core.RatingKey) (*storedTitle, error) {
	gen := c.writeGen.Load()
	minBucket := c.minuteBucket()
	// Movies, shows, seasons and episodes are read again while a list builds
	// image grants and versions. The cache is generation-scoped, same as movies.
	if cacheStoredTitle(key.Kind()) {
		c.cacheMu.RLock()
		ent, ok := c.titleCache[key]
		c.cacheMu.RUnlock()
		if ok && ent.gen == gen && ent.minute == minBucket {
			cp := ent.st
			cp.t = cloneTitle(ent.st.t)
			return &cp, nil
		}
	}
	row := c.db.QueryRowContext(ctx, `SELECT `+titleCols+`,detail_at FROM catalog_titles WHERE key=?`, int64(key))
	var detailAt int64
	st, err := scanTitle(extraScan{row, []any{&detailAt}})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, core.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if detailAt != 0 {
		st.detailAt = time.Unix(detailAt, 0).UTC()
	}
	items := []core.Title{st.t}
	if err := c.hydrateSlice(ctx, items); err != nil {
		return nil, err
	}
	st.t = items[0]
	if cacheStoredTitle(key.Kind()) && c.writeGen.Load() == gen {
		c.cacheMu.Lock()
		cp := *st
		cp.t = cloneTitle(st.t)
		c.putSnapshotLocked(key, gen, minBucket, cp)
		c.cacheMu.Unlock()
	}
	return st, nil
}

// PreloadStored reads in batches the mirrored titles loadByKey would read one
// at a time, and caches each exactly as loadByKey does (detail time
// included) for this generation and minute. A list of episodes checks
// every episode's series; later Get and CachedGet calls then hit the cache.
// Titles already cached, missing or unreadable are left to loadByKey.
func (c *Catalog) PreloadStored(ctx context.Context, keys []core.RatingKey) {
	gen, minute := c.writeGen.Load(), c.minuteBucket()
	seen := make(map[core.RatingKey]bool, len(keys))
	var missing []any
	c.cacheMu.RLock()
	for _, k := range keys {
		if seen[k] || !cacheStoredTitle(k.Kind()) {
			continue
		}
		seen[k] = true
		if ent, ok := c.titleCache[k]; ok && ent.gen == gen && ent.minute == minute {
			continue
		}
		missing = append(missing, int64(k))
	}
	c.cacheMu.RUnlock()
	for start := 0; start < len(missing); start += 200 {
		part := missing[start:min(start+200, len(missing))]
		rows, err := c.db.QueryContext(ctx, `SELECT `+titleCols+`,detail_at FROM catalog_titles WHERE key IN (`+placeholders(len(part))+`)`, part...)
		if err != nil {
			return
		}
		var stored []*storedTitle
		for rows.Next() {
			var detailAt int64
			st, err := scanTitle(extraScan{rows, []any{&detailAt}})
			if err != nil {
				rows.Close()
				return
			}
			if detailAt != 0 {
				st.detailAt = time.Unix(detailAt, 0).UTC()
			}
			stored = append(stored, st)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return
		}
		items := make([]core.Title, len(stored))
		for i, st := range stored {
			items[i] = st.t
		}
		if err := c.hydrateSlice(ctx, items); err != nil || c.writeGen.Load() != gen {
			return
		}
		c.cacheMu.Lock()
		for i, st := range stored {
			cp := *st
			cp.t = cloneTitle(items[i])
			c.putSnapshotLocked(cp.t.Key, gen, minute, cp)
		}
		c.cacheMu.Unlock()
	}
}

type scanner interface{ Scan(dest ...any) error }

func scanTitle(s scanner) (*storedTitle, error) {
	var st storedTitle
	var key, showKey int64
	var typ, imdb, showTitle, seasonTitle, title, orig, sortT, tagline, summary, status, lang, crating, csource string
	var tmdbID, tvdb, showTMDB int64
	var season, episode, year, voteCount, collID, child, leaf, browsable, detail, adult int
	var releasedTS, runtimeSec, addedAt, fetchedAt, lastAirTS int64
	var rating, pop float64
	var collName, poster, backdrop, still, logo string
	err := s.Scan(&key, &typ, &tmdbID, &imdb, &tvdb, &showKey, &showTMDB, &showTitle, &seasonTitle, &season, &episode,
		&title, &orig, &sortT, &tagline, &summary, &year, &releasedTS, &runtimeSec, &status, &lang,
		&crating, &rating, &voteCount, &pop, &poster, &backdrop, &still, &logo, &collID, &collName,
		&child, &leaf, &browsable, &addedAt, &fetchedAt, &detail, &adult, &lastAirTS, &csource)
	if err != nil {
		return nil, err
	}
	t := core.Title{
		Key: core.RatingKey(key), Type: core.MediaType(typ),
		IDs:     core.IDs{TMDB: tmdbID, IMDB: imdb, TVDB: tvdb},
		ShowKey: core.RatingKey(showKey), ShowTitle: showTitle, SeasonTitle: seasonTitle,
		Season: season, Episode: episode, Title: title, OriginalTitle: orig, SortTitle: sortT,
		Tagline: tagline, Summary: summary, Year: year, Runtime: time.Duration(runtimeSec) * time.Second,
		Status: status, Language: lang, ContentRating: crating, ContentRatingSource: csource, Adult: adult != 0, Rating: rating, VoteCount: voteCount,
		Popularity: pop, PosterPath: poster, BackdropPath: backdrop, StillPath: still, LogoPath: logo,
		CollectionID: int64(collID), CollectionName: collName, ChildCount: child, LeafCount: leaf,
	}
	if showTMDB != 0 {
		t.ShowIDs.TMDB = showTMDB
	}
	if releasedTS != 0 {
		t.Released = time.Unix(releasedTS, 0).UTC()
	}
	if lastAirTS != 0 {
		t.LastAirDate = time.Unix(lastAirTS, 0).UTC()
	}
	if fetchedAt != 0 {
		t.UpdatedAt = time.Unix(fetchedAt, 0).UTC()
	}
	st.t = t
	st.browsable = browsable != 0
	st.detail = detail != 0
	if fetchedAt != 0 {
		st.fetchedAt = time.Unix(fetchedAt, 0).UTC()
	}
	// Fill ShowIDs imdb/tvdb lazily from the show row when needed by caller.
	return &st, nil
}

// hydrateSlice fills Genres/Studios/Countries/Cast/Directors/Writers/ShowIDs in place.
func (c *Catalog) hydrateSlice(ctx context.Context, items []core.Title) error {
	if len(items) == 0 {
		return nil
	}
	if err := c.airedCounts(ctx, items); err != nil {
		return err
	}
	// Same shape as hydrateHomeRelease: query once per distinct key, but
	// apply each row to EVERY occurrence of that key in the slice. A title
	// can appear on several shelves (in different chunks, too); mapping a
	// key to a single index would leave the other copies unhydrated, and
	// querying per occurrence would hydrate a copy once per chunk it
	// appears in.
	idx := map[core.RatingKey][]int{}
	keys := make([]any, 0, len(items))
	for i := range items {
		if _, ok := idx[items[i].Key]; !ok {
			keys = append(keys, int64(items[i].Key))
		}
		idx[items[i].Key] = append(idx[items[i].Key], i)
	}
	for i := range items {
		items[i].HomeReleaseChecked = false
		items[i].HomeReleased = time.Time{}
		items[i].HomeReleaseFallbackAdmitted = false
	}
	// Bound SQLite parameters independently of the caller's page size
	// (same 200 as titlesByKeys). One statement covers genres, keywords,
	// studios, countries, credits and home-release dates.
	const batch = 200
	for chunk := range store.Chunks(keys, batch) {
		if err := c.hydrateFactBatch(ctx, items, idx, chunk); err != nil {
			return err
		}
	}
	if err := c.hydrateShows(ctx, items); err != nil {
		return err
	}
	if err := c.hydrateTrailers(ctx, items, idx, keys); err != nil {
		return err
	}
	return c.applyHydratedAdded(ctx, items)
}

func (c *Catalog) hydrateTrailers(ctx context.Context, items []core.Title, idx map[core.RatingKey][]int, keys []any) error {
	const batch = 200
	for chunk := range store.Chunks(keys, batch) {
		args := chunk
		rows, err := c.db.QueryContext(ctx, `SELECT key, name, url FROM catalog_remote_trailers WHERE key IN (`+placeholders(len(args))+`)`, args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key int64
			var name, url string
			if err := rows.Scan(&key, &name, &url); err != nil {
				rows.Close()
				return err
			}
			for _, i := range idx[core.RatingKey(key)] {
				items[i].RemoteTrailerName = name
				items[i].RemoteTrailerURL = url
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	return nil
}

type seriesIdentity struct {
	title, contentRating string
	imdb, original       string
	tvdb                 int64
	year                 int
	keywords             []string
	altTitles            []string
}

type creditFact struct {
	person core.Person
	kind   string
	ord    int64
}

func (c *Catalog) hydrateFactBatch(ctx context.Context, items []core.Title, idx map[core.RatingKey][]int, args []any) error {
	ph := placeholders(len(args))
	q := `SELECT key, tag, a, b, c, d, e, f, g, extra FROM (
SELECT key, 'genre' AS tag, genre AS a, '' AS b, '' AS c, '' AS d, '' AS e, '' AS f, '' AS g, '' AS extra FROM catalog_genres WHERE key IN (` + ph + `)
UNION ALL
SELECT key, 'keyword', keyword, '', '', '', '', '', '', '' FROM catalog_keywords WHERE key IN (` + ph + `)
UNION ALL
SELECT key, 'alt', title, '', '', '', '', '', '', '' FROM catalog_alt_titles WHERE key IN (` + ph + `)
UNION ALL
SELECT key, 'studio', studio, '', '', '', '', '', '', '' FROM catalog_studios WHERE key IN (` + ph + `)
UNION ALL
SELECT key, 'country', country, '', '', '', '', '', '', '' FROM catalog_countries WHERE key IN (` + ph + `)
UNION ALL
SELECT c.key, 'credit', c.kind, c.name, c.role, c.thumb, CAST(c.person_tmdb AS TEXT), COALESCE(p.imdb,''), CAST(c.ord AS TEXT), c.provider_id FROM catalog_credits c LEFT JOIN catalog_people p ON p.person_tmdb=c.person_tmdb WHERE c.key IN (` + ph + `)
UNION ALL
SELECT h.key, 'home', CAST(h.checked AS TEXT), CAST(h.earliest_ts AS TEXT), CAST(h.fallback_admitted AS TEXT), CAST(t.released_ts AS TEXT), CAST(t.year AS TEXT), '', '', '' FROM catalog_home_release h JOIN catalog_titles t ON t.key=h.key WHERE h.key IN (` + ph + `)
) AS facts`
	bound := make([]any, 0, len(args)*7)
	for i := 0; i < 7; i++ {
		bound = append(bound, args...)
	}
	rows, err := c.db.QueryContext(ctx, q, bound...)
	if err != nil {
		return err
	}
	defer rows.Close()
	credits := map[core.RatingKey][]creditFact{}
	touched := map[core.RatingKey]bool{}
	for rows.Next() {
		var key int64
		var tag, a, b, ccol, d, e, f, g, extra string
		if err := rows.Scan(&key, &tag, &a, &b, &ccol, &d, &e, &f, &g, &extra); err != nil {
			return err
		}
		k := core.RatingKey(key)
		touched[k] = true
		switch tag {
		case "genre":
			for _, i := range idx[k] {
				items[i].Genres = append(items[i].Genres, a)
			}
		case "keyword":
			for _, i := range idx[k] {
				items[i].Keywords = append(items[i].Keywords, a)
			}
		case "alt":
			for _, i := range idx[k] {
				items[i].AlternativeTitles = append(items[i].AlternativeTitles, a)
			}
		case "studio":
			for _, i := range idx[k] {
				items[i].Studios = append(items[i].Studios, a)
			}
		case "country":
			for _, i := range idx[k] {
				items[i].Countries = append(items[i].Countries, a)
			}
		case "credit":
			pid, _ := strconv.ParseInt(e, 10, 64)
			ord, _ := strconv.ParseInt(g, 10, 64)
			credits[k] = append(credits[k], creditFact{kind: a, ord: ord, person: core.Person{TMDB: pid, IMDB: f, ID: extra, Name: b, Role: ccol, ThumbPath: d}})
		case "home":
			checked, _ := strconv.Atoi(a)
			earliest, _ := strconv.ParseInt(b, 10, 64)
			admitted, _ := strconv.Atoi(ccol)
			released, _ := strconv.ParseInt(d, 10, 64)
			year, _ := strconv.Atoi(e)
			for _, i := range idx[k] {
				items[i].Released = time.Time{}
				if released != 0 {
					items[i].Released = time.Unix(released, 0).UTC()
				}
				items[i].Year = year
				items[i].HomeReleaseChecked = checked != 0
				items[i].HomeReleaseFallbackAdmitted = admitted != 0
				if earliest != 0 {
					items[i].HomeReleased = time.Unix(earliest, 0).UTC()
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for key := range touched {
		for _, i := range idx[key] {
			items[i].Genres = uniqueStrings(items[i].Genres)
			items[i].Keywords = uniqueStrings(items[i].Keywords)
			sort.Strings(items[i].Genres)
			sort.Strings(items[i].Keywords)
			sort.Strings(items[i].AlternativeTitles)
			sort.Strings(items[i].Studios)
			sort.Strings(items[i].Countries)
		}
	}
	for key, list := range credits {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].kind != list[j].kind {
				return list[i].kind < list[j].kind
			}
			return list[i].ord < list[j].ord
		})
		for _, fact := range list {
			for _, i := range idx[key] {
				switch fact.kind {
				case "director":
					items[i].Directors = append(items[i].Directors, fact.person)
				case "writer":
					items[i].Writers = append(items[i].Writers, fact.person)
				case "crew":
					items[i].Crew = append(items[i].Crew, fact.person)
				default:
					items[i].Cast = append(items[i].Cast, fact.person)
				}
			}
		}
	}
	return nil
}

func (c *Catalog) hydrateShows(ctx context.Context, items []core.Title) error {
	shows := map[core.RatingKey]*seriesIdentity{}
	var showArgs []any
	for i := range items {
		if k := items[i].ShowKey; k != 0 && shows[k] == nil {
			shows[k] = &seriesIdentity{}
			showArgs = append(showArgs, int64(k))
		}
		if items[i].Type == core.TypeEpisode && items[i].Season == 1 && items[i].Episode > 0 {
			items[i].AbsoluteEpisode = items[i].Episode
		}
	}
	if len(shows) == 0 {
		return nil
	}
	counts := map[core.RatingKey]map[int]int{}
	const batch = 200
	for chunk := range store.Chunks(showArgs, batch) {
		part := chunk
		ph := placeholders(len(part))
		q := `SELECT tag, key, a, b, c, d, e, f FROM (
SELECT 'id' AS tag, key, imdb AS a, CAST(tvdb AS TEXT) AS b, CAST(year AS TEXT) AS c, original_title AS d, title AS e, content_rating AS f FROM catalog_titles WHERE key IN (` + ph + `)
UNION ALL
SELECT 'kw', key, keyword, '', '', '', '', '' FROM catalog_keywords WHERE key IN (` + ph + `)
UNION ALL
SELECT 'alt', key, title, '', '', '', '', '' FROM catalog_alt_titles WHERE key IN (` + ph + `)
UNION ALL
SELECT 'season', show_key, CAST(season AS TEXT), CAST(child_count AS TEXT), '', '', '', '' FROM catalog_titles WHERE type='season' AND season>0 AND show_key IN (` + ph + `)
) AS show_facts`
		bound := make([]any, 0, len(part)*4)
		for i := 0; i < 4; i++ {
			bound = append(bound, part...)
		}
		rows, err := c.db.QueryContext(ctx, q, bound...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var key int64
			var tag, a, b, ccol, d, e, f string
			if err := rows.Scan(&tag, &key, &a, &b, &ccol, &d, &e, &f); err != nil {
				rows.Close()
				return err
			}
			k := core.RatingKey(key)
			switch tag {
			case "id":
				if show := shows[k]; show != nil {
					tvdb, _ := strconv.ParseInt(b, 10, 64)
					year, _ := strconv.Atoi(ccol)
					show.imdb, show.tvdb, show.year, show.original, show.title, show.contentRating = a, tvdb, year, d, e, f
				}
			case "kw":
				if show := shows[k]; show != nil {
					show.keywords = append(show.keywords, a)
				}
			case "alt":
				if show := shows[k]; show != nil {
					show.altTitles = append(show.altTitles, a)
				}
			case "season":
				season, _ := strconv.Atoi(a)
				n, _ := strconv.Atoi(b)
				if counts[k] == nil {
					counts[k] = map[int]int{}
				}
				counts[k][season] = n
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	for show := range shows {
		sort.Strings(shows[show].keywords)
		sort.Strings(shows[show].altTitles)
	}
	for i := range items {
		show := shows[items[i].ShowKey]
		if show == nil {
			continue
		}
		if len(items[i].Keywords) == 0 && len(show.keywords) > 0 && (items[i].Type == core.TypeSeason || items[i].Type == core.TypeEpisode) {
			items[i].Keywords = uniqueStrings(append([]string(nil), show.keywords...))
		}
		if items[i].ShowTitle == "" && show.title != "" {
			items[i].ShowTitle = show.title
		}
		// Seasons and episodes take their series' rating, so a rating cap
		// (content_rating_rank) admits them as it admits the series.
		if items[i].ContentRating == "" && show.contentRating != "" && (items[i].Type == core.TypeEpisode || items[i].Type == core.TypeSeason) {
			items[i].ContentRating = show.contentRating
		}
		if items[i].ShowYear == 0 {
			items[i].ShowYear = show.year
		}
		if len(show.altTitles) > 0 {
			items[i].ShowAlternativeTitles = append([]string(nil), show.altTitles...)
		}
		if show.original != "" {
			items[i].ShowOriginalTitle = show.original
		}
		if items[i].ShowIDs.IMDB == "" {
			items[i].ShowIDs.IMDB = show.imdb
		}
		if items[i].ShowIDs.TVDB == 0 {
			items[i].ShowIDs.TVDB = show.tvdb
		}
		// Number episodes from the series start. An episode stays unnumbered
		// when an earlier season is missing or has no known count.
		seasons := counts[items[i].ShowKey]
		t := &items[i]
		if t.Type != core.TypeEpisode || t.Season <= 1 || t.Episode <= 0 || seasons == nil {
			continue
		}
		total := 0
		for s := 1; s < t.Season; s++ {
			if seasons[s] <= 0 {
				total = -1
				break
			}
			total += seasons[s]
		}
		if total >= 0 {
			t.AbsoluteEpisode = total + t.Episode
		}
	}
	return nil
}

func (c *Catalog) applyHydratedAdded(ctx context.Context, items []core.Title) error {
	now := c.now()
	needsAir := false
	for i := range items {
		switch items[i].Type {
		case core.TypeShow, core.TypeSeason:
			needsAir = true
		default:
			if !items[i].Released.IsZero() && !items[i].Released.After(now) {
				items[i].AddedAt = items[i].Released
			}
		}
	}
	if !needsAir {
		return nil
	}
	return applyAirAddedTimes(ctx, c.db, items, now)
}

// scanPairs applies each (key, value) row to every slice index registered
// for that key in idx, so duplicated titles are hydrated exactly once per
// occurrence.
func (c *Catalog) scanPairs(ctx context.Context, q string, args []any, apply func(i int, v string), idx map[core.RatingKey][]int) error {
	rows, err := c.db.QueryContext(ctx, q, args...)
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
		for _, i := range idx[core.RatingKey(k)] {
			apply(i, v)
		}
	}
	return rows.Err()
}

func keyArgs(items []core.Title) []any {
	args := make([]any, len(items))
	for i := range items {
		args[i] = int64(items[i].Key)
	}
	return args
}
func placeholders(n int) string {
	return store.Placeholders(n)
}

type upsertOpts struct {
	// preserveDetailed keeps a delayed source snapshot from overwriting details
	// that completed after the snapshot was queued. Enforced by the UPSERT.
	preserveDetailed bool

	browsable bool // request browsable (added_at set if newly browsable)
	detail    bool
	// airDates: the payload is authoritative for the title's air date even
	// though it is not a detail fetch. Season and episode air dates come
	// only from TMDB's show and season payloads, so a date that is added,
	// moved or withdrawn must replace the stored one, including on rows an
	// earlier detail upsert marked (which otherwise keep their date).
	airDates     bool
	releaseDates *tmdb.ReleaseDates
	// ratingCuts, set by upsertMany, limits rating, vote and popularity
	// wobbles to the writes rating_drift.go keeps.
	ratingCuts *ratingCuts
	// stored, set by upsertMany, holds the batch's keys that already had a
	// row; a key it lacks is inserted by this upsert, so the probes for
	// what an existing row holds (ratings, release names) are skipped.
	stored map[int64]bool
	// region is the owner's catalogue region; upsertMany sets it. A TMDB
	// rating from the region or the US outranks a provider rating.
	region string
}

// UpsertCollectionTitles mirrors one source refresh in a single transaction.
// Call only from the collection build worker, never a serving path.
func (c *Catalog) UpsertCollectionTitles(ctx context.Context, titles []core.Title) error {
	if len(titles) == 0 {
		return nil
	}
	titles = append([]core.Title(nil), titles...)
	sort.Slice(titles, func(i, j int) bool { return titles[i].Key < titles[j].Key })
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, t := range titles {
		if err := upsertOne(ctx, tx, t, upsertOpts{browsable: t.IsReleased(c.now()), preserveDetailed: true}, c.now().Unix()); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.writeGen.Add(1)
	return nil
}

// UpsertTitle stores a title (exported for tests/seeders and other components that
// need to insert known titles). Browsable/added_at are preserved once set.
// The row is not a fresh detail record, so a later Get may refresh it.
func (c *Catalog) UpsertTitle(ctx context.Context, t core.Title, browsable bool) error {
	return c.upsert(ctx, t, upsertOpts{browsable: browsable})
}

// UpsertDetailedTitle stores a title as a fresh detail record. A later Get
// serves the row without a metadata refresh until it is stale. The scorecard
// fixture uses this: its seed is the source of truth, and a failed refresh
// must not be repeated for every library row.
func (c *Catalog) UpsertDetailedTitle(ctx context.Context, t core.Title) error {
	return c.upsert(ctx, t, upsertOpts{browsable: true, detail: true})
}

func (c *Catalog) upsert(ctx context.Context, t core.Title, o upsertOpts) error {
	return c.upsertMany(ctx, []core.Title{t}, o)
}

func (c *Catalog) upsertMany(ctx context.Context, titles []core.Title, o upsertOpts) error {
	items := make([]upsertItem, len(titles))
	for i, t := range titles {
		items[i] = upsertItem{t: t, o: o}
	}
	return c.upsertItems(ctx, items)
}

// upsertItem is one title and its write options.
type upsertItem struct {
	t core.Title
	o upsertOpts
}

// upsertItems stores items in one transaction, each with its own options,
// then runs after (more writes of the same transaction).
func (c *Catalog) upsertItems(ctx context.Context, items []upsertItem, after ...func(context.Context, *sql.Tx) error) error {
	if len(items) == 0 && len(after) == 0 {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := c.now().Unix()
	region := c.config().Region
	cuts := c.ratingCuts()
	titles := make([]core.Title, len(items))
	for i := range items {
		titles[i] = items[i].t
	}
	stored, err := storedKeys(ctx, tx, titles)
	if err != nil {
		return err
	}
	for _, it := range items {
		o := it.o
		o.region, o.ratingCuts, o.stored = region, &cuts, stored
		if err := upsertOne(ctx, tx, it.t, o, now); err != nil {
			return err
		}
	}
	for _, f := range after {
		if err := f(ctx, tx); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.writeGen.Add(1)
	c.noteSearchWrites(titles)
	return nil
}

// materialTitleChangeSQL is true when an upsert changes a user-visible column on
// catalog_titles (excluding TMDB popularity/vote jitter), so fetched_at
// (UpdatedAt) stays stable across no-op background refreshes.
const materialTitleChangeSQL = `(
 catalog_titles.fetched_at=0 OR
 catalog_titles.type<>excluded.type OR
 catalog_titles.tmdb_id<>excluded.tmdb_id OR
 catalog_titles.adult<>excluded.adult OR
 (excluded.imdb<>'' AND catalog_titles.imdb<>excluded.imdb) OR
 (excluded.tvdb<>0 AND catalog_titles.tvdb<>excluded.tvdb) OR
 catalog_titles.show_key<>excluded.show_key OR
 catalog_titles.show_tmdb<>excluded.show_tmdb OR
 catalog_titles.show_title<>excluded.show_title OR
 catalog_titles.season_title<>excluded.season_title OR
 catalog_titles.season<>excluded.season OR
 catalog_titles.episode<>excluded.episode OR
 (excluded.title<>'' AND catalog_titles.title<>excluded.title) OR
 (excluded.original_title<>'' AND catalog_titles.original_title<>excluded.original_title) OR
 (excluded.sort_title<>'' AND catalog_titles.sort_title<>excluded.sort_title) OR
 (excluded.tagline<>'' AND catalog_titles.tagline<>excluded.tagline) OR
 (excluded.summary<>'' AND catalog_titles.summary<>excluded.summary) OR
 ((excluded.detail=1 OR (catalog_titles.detail=0 AND excluded.year<>0)) AND catalog_titles.year<>excluded.year) OR
 ((excluded.detail=1 OR (catalog_titles.detail=0 AND excluded.released_ts<>0)) AND catalog_titles.released_ts<>excluded.released_ts) OR
 (excluded.runtime_sec<>0 AND catalog_titles.runtime_sec<>excluded.runtime_sec) OR
 (excluded.status<>'' AND catalog_titles.status<>excluded.status) OR
 (excluded.last_air_ts<>0 AND catalog_titles.last_air_ts<>excluded.last_air_ts) OR
 (excluded.language<>'' AND catalog_titles.language<>excluded.language) OR
 (excluded.poster<>'' AND catalog_titles.poster<>excluded.poster) OR
 (excluded.backdrop<>'' AND catalog_titles.backdrop<>excluded.backdrop) OR
 (excluded.still<>'' AND catalog_titles.still<>excluded.still) OR
 (excluded.logo<>'' AND catalog_titles.logo<>excluded.logo) OR
 (excluded.collection_id<>0 AND catalog_titles.collection_id<>excluded.collection_id) OR
 (excluded.collection_name<>'' AND catalog_titles.collection_name<>excluded.collection_name) OR
 (excluded.child_count<>0 AND catalog_titles.child_count<>excluded.child_count) OR
 (excluded.leaf_count<>0 AND catalog_titles.leaf_count<>excluded.leaf_count) OR
 excluded.browsable>catalog_titles.browsable OR
 excluded.detail>catalog_titles.detail
)`

type noRows struct{}

func (noRows) LastInsertId() (int64, error) { return 0, nil }
func (noRows) RowsAffected() (int64, error) { return 0, nil }

// storedKeys reads which of titles' keys already have a row, 500 a statement.
func storedKeys(ctx context.Context, tx *sql.Tx, titles []core.Title) (map[int64]bool, error) {
	out := make(map[int64]bool, len(titles))
	for start := 0; start < len(titles); start += 500 {
		part := titles[start:min(start+500, len(titles))]
		args := make([]any, len(part))
		for i, t := range part {
			args[i] = int64(t.Key)
		}
		rows, err := tx.QueryContext(ctx, `SELECT key FROM catalog_titles WHERE key IN (`+placeholders(len(part))+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var k int64
			if err := rows.Scan(&k); err != nil {
				rows.Close()
				return nil, err
			}
			out[k] = true
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func upsertOne(ctx context.Context, tx *sql.Tx, t core.Title, o upsertOpts, now int64) error {
	if t.Key == 0 {
		return fmt.Errorf("catalog: upsert title with zero key")
	}
	if t.SortTitle == "" {
		t.SortTitle = sortTitle(t.Title)
	} else {
		t.SortTitle = foldSortTitle(t.SortTitle)
	}
	showTMDB := t.ShowIDs.TMDB
	if showTMDB == 0 && t.ShowKey != 0 {
		showTMDB = t.ShowKey.TMDB()
	}
	// For episodes/seasons, IDs.TMDB is their own TMDB id. show_tmdb stored separately.
	var releasedTS int64
	var lastAirTS int64
	if !t.LastAirDate.IsZero() {
		lastAirTS = t.LastAirDate.Unix()
	}
	if !t.Released.IsZero() {
		releasedTS = t.Released.Unix()
		if t.Year == 0 {
			t.Year = t.Released.Year()
		}
	}
	// A movie summary's release_date is the primary date. Remember it so
	// the year correction does not fetch the movie. A detailed row keeps
	// its own source: the summary date must not close the queue while the
	// stored year is still the theatrical year.
	if t.Type == core.TypeMovie && !o.detail && t.YearSource == "" && !t.Released.IsZero() {
		t.YearSource = yearSourcePrimary
		t.PrimaryReleased = t.Released
	}
	browsableInt := 0
	if o.browsable {
		browsableInt = 1
	}
	detailInt := 0
	if o.detail {
		detailInt = 1
	}

	homeInt := 0
	if homeRating(t.ContentRating, t.ContentRatingSource, o.region) {
		homeInt = 1
	}
	preserveInt := 0
	if o.preserveDetailed {
		preserveInt = 1
	}
	// A new movie row takes its year source at insert (what
	// saveMovieYearSource would write to a row that has none); the
	// conflict path leaves both columns to saveMovieYearSource.
	insertYearSource, insertPrimary := "", int64(0)
	if t.Type == core.TypeMovie && t.YearSource != "" {
		insertYearSource = t.YearSource
		if !t.PrimaryReleased.IsZero() {
			insertPrimary = t.PrimaryReleased.Unix()
		}
	}
	// Keep richer existing fields when a summary upsert would blank them:
	// handled per-field with CASE in ON CONFLICT for a few detail-only fields.
	result, err := tx.ExecContext(ctx, `
INSERT INTO catalog_titles (key,type,tmdb_id,imdb,tvdb,show_key,show_tmdb,show_title,season_title,season,episode,
 title,original_title,sort_title,tagline,summary,year,released_ts,runtime_sec,status,language,
 content_rating,content_rating_rank,content_rating_source,rating,vote_count,popularity,poster,backdrop,still,logo,
 collection_id,collection_name,child_count,leaf_count,browsable,added_at,fetched_at,detail,adult,detail_at,last_air_ts,
 year_source,primary_released_ts)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(key) DO UPDATE SET
 type=excluded.type, tmdb_id=excluded.tmdb_id, adult=excluded.adult,
 imdb=CASE WHEN excluded.imdb<>'' THEN excluded.imdb ELSE catalog_titles.imdb END,
 tvdb=CASE WHEN excluded.tvdb<>0 THEN excluded.tvdb ELSE catalog_titles.tvdb END,
 show_key=excluded.show_key, show_tmdb=excluded.show_tmdb, show_title=excluded.show_title,
 season_title=excluded.season_title, season=excluded.season, episode=excluded.episode,
 title=CASE WHEN excluded.title<>'' THEN excluded.title ELSE catalog_titles.title END,
 original_title=CASE WHEN excluded.original_title<>'' THEN excluded.original_title ELSE catalog_titles.original_title END,
 sort_title=CASE WHEN excluded.sort_title<>'' THEN excluded.sort_title ELSE catalog_titles.sort_title END,
 tagline=CASE WHEN excluded.tagline<>'' THEN excluded.tagline ELSE catalog_titles.tagline END,
 summary=CASE WHEN excluded.summary<>'' THEN excluded.summary ELSE catalog_titles.summary END,
 year=CASE WHEN excluded.detail=1 OR (catalog_titles.detail=0 AND excluded.year<>0) THEN excluded.year ELSE catalog_titles.year END,
 released_ts=CASE WHEN excluded.detail=1 OR (catalog_titles.detail=0 AND excluded.released_ts<>0) THEN excluded.released_ts ELSE catalog_titles.released_ts END,
 runtime_sec=CASE WHEN excluded.runtime_sec<>0 THEN excluded.runtime_sec ELSE catalog_titles.runtime_sec END,
 status=CASE WHEN excluded.status<>'' THEN excluded.status ELSE catalog_titles.status END,
 last_air_ts=CASE WHEN excluded.last_air_ts<>0 THEN excluded.last_air_ts ELSE catalog_titles.last_air_ts END,
 language=CASE WHEN excluded.language<>'' THEN excluded.language ELSE catalog_titles.language END,
 content_rating=`+ratingPrecedence("content_rating")+`,
 content_rating_rank=`+ratingPrecedence("content_rating_rank")+`,
 content_rating_source=`+ratingPrecedence("content_rating_source")+`,
 rating=excluded.rating, vote_count=excluded.vote_count, popularity=excluded.popularity,
 poster=CASE WHEN excluded.poster<>'' THEN excluded.poster ELSE catalog_titles.poster END,
 backdrop=CASE WHEN excluded.backdrop<>'' THEN excluded.backdrop ELSE catalog_titles.backdrop END,
 still=CASE WHEN excluded.still<>'' THEN excluded.still ELSE catalog_titles.still END,
 logo=CASE WHEN excluded.logo<>'' THEN excluded.logo ELSE catalog_titles.logo END,
 collection_id=CASE WHEN excluded.collection_id<>0 THEN excluded.collection_id ELSE catalog_titles.collection_id END,
 collection_name=CASE WHEN excluded.collection_name<>'' THEN excluded.collection_name ELSE catalog_titles.collection_name END,
 child_count=CASE WHEN excluded.child_count<>0 THEN excluded.child_count ELSE catalog_titles.child_count END,
 leaf_count=CASE WHEN excluded.leaf_count<>0 THEN excluded.leaf_count ELSE catalog_titles.leaf_count END,
 browsable=CASE WHEN excluded.browsable>catalog_titles.browsable THEN excluded.browsable ELSE catalog_titles.browsable END,
 added_at=CASE WHEN catalog_titles.added_at<>0 THEN catalog_titles.added_at
               WHEN excluded.browsable=1 THEN excluded.added_at ELSE 0 END,
 fetched_at=CASE WHEN `+materialTitleChangeSQL+` OR catalog_titles.content_rating<>(`+ratingPrecedence("content_rating")+`) THEN excluded.fetched_at ELSE catalog_titles.fetched_at END,
 detail=CASE WHEN excluded.detail>catalog_titles.detail THEN excluded.detail ELSE catalog_titles.detail END,
 detail_at=CASE WHEN excluded.detail=1 THEN excluded.detail_at ELSE catalog_titles.detail_at END
 WHERE (`+materialTitleChangeSQL+` OR
 (catalog_titles.added_at=0 AND excluded.browsable=1) OR
 catalog_titles.content_rating<>(`+ratingPrecedence("content_rating")+`) OR
 catalog_titles.content_rating_rank<>(`+ratingPrecedence("content_rating_rank")+`) OR
 catalog_titles.content_rating_source<>(`+ratingPrecedence("content_rating_source")+`)) AND (catalog_titles.detail=0 OR excluded.detail=1 OR ?=0)`,
		int64(t.Key), string(t.Type), t.IDs.TMDB, t.IDs.IMDB, t.IDs.TVDB, int64(t.ShowKey), showTMDB, t.ShowTitle, t.SeasonTitle, t.Season, t.Episode,
		t.Title, t.OriginalTitle, t.SortTitle, t.Tagline, t.Summary, t.Year, releasedTS, int64(t.Runtime.Seconds()), t.Status, t.Language,
		t.ContentRating, int(contentrating.LevelOf(t.ContentRating)), t.ContentRatingSource, t.Rating, t.VoteCount, t.Popularity, t.PosterPath, t.BackdropPath, t.StillPath, t.LogoPath,
		t.CollectionID, t.CollectionName, t.ChildCount, t.LeafCount, browsableInt, addedAtFor(browsableInt, now), now, detailInt, t.Adult, detailAtFor(detailInt, now), lastAirTS,
		insertYearSource, insertPrimary,
		homeInt, homeInt, homeInt, homeInt, homeInt, homeInt, homeInt, preserveInt)
	if err != nil {
		return fmt.Errorf("catalog: upsert %d: %w", t.Key, err)
	}
	// TMDB's rating, votes and popularity move on nearly every sync. When
	// nothing else changed, update only those three columns: the upsert
	// above assigns every column, and SQLite rewrites the entry of every
	// index on a column an UPDATE assigns (37 on catalog_titles), which made
	// a popularity-only refresh 33 WAL pages a title. After a full upsert
	// the values match and this matches no row.
	fresh := o.stored != nil && !o.stored[int64(t.Key)]
	if o.stored != nil {
		o.stored[int64(t.Key)] = true // a repeat in the batch updates it
	}
	// Small wobbles are written only per rating_drift.go. A row this upsert
	// inserted already holds the exact values.
	drift, driftArgs := "", []any(nil)
	if !o.detail && o.ratingCuts != nil && !ratingExact(t.Key, time.Unix(now, 0)) {
		drift, driftArgs = ratingDriftSQL(*o.ratingCuts, t.Popularity, t.VoteCount, t.Rating)
		drift = " AND " + drift
	}
	var jitter sql.Result = noRows{}
	if !fresh {
		jitter, err = tx.ExecContext(ctx, `UPDATE catalog_titles SET rating=?,vote_count=?,popularity=? WHERE key=? AND (rating<>? OR vote_count<>? OR popularity<>?) AND (detail=0 OR ?=1 OR ?=0)`+drift,
			append([]any{t.Rating, t.VoteCount, t.Popularity, int64(t.Key), t.Rating, t.VoteCount, t.Popularity, detailInt, preserveInt}, driftArgs...)...)
		if err != nil {
			return fmt.Errorf("catalog: upsert %d ratings: %w", t.Key, err)
		}
	}
	if o.preserveDetailed {
		n, err := result.RowsAffected()
		if err != nil {
			return err
		}
		m, err := jitter.RowsAffected()
		if err != nil {
			return err
		}
		if n+m == 0 {
			var detailed int
			if err := tx.QueryRowContext(ctx, `SELECT detail FROM catalog_titles WHERE key=?`, int64(t.Key)).Scan(&detailed); err != nil {
				return err
			}
			if detailed != 0 {
				// Search can promote an existing detail row without replacing its facts.
				if o.browsable {
					_, err = tx.ExecContext(ctx, `UPDATE catalog_titles SET browsable=1,added_at=CASE WHEN added_at=0 THEN ? ELSE added_at END WHERE key=? AND browsable=0`, now, int64(t.Key))
				}
				return err
			}
		}
	}
	// A successful detail fetch renews freshness even when its content is
	// unchanged. Keep this out of the UPSERT predicate: updating only this
	// unindexed column avoids the content-column triggers and index churn.
	// Summary writes never renew details, and the same timestamp writes nothing.
	if o.detail {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET detail_at=? WHERE key=? AND detail_at<>?`, now, int64(t.Key), now); err != nil {
			return fmt.Errorf("catalog: detail freshness %d: %w", t.Key, err)
		}
	}
	updateNames := updateReleaseNames
	if fresh {
		updateNames = func(ctx context.Context, tx *sql.Tx, key int64, typ core.MediaType) error {
			return insertFreshReleaseNames(ctx, tx, key, typ, t.Title, t.OriginalTitle)
		}
	}
	if err := updateNames(ctx, tx, int64(t.Key), t.Type); err != nil {
		return fmt.Errorf("catalog: index release name: %w", err)
	}
	if t.RemoteTrailerURL != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_remote_trailers(key, name, url) VALUES(?,?,?) ON CONFLICT(key) DO UPDATE SET name=excluded.name, url=excluded.url WHERE catalog_remote_trailers.name<>excluded.name OR catalog_remote_trailers.url<>excluded.url`, int64(t.Key), t.RemoteTrailerName, t.RemoteTrailerURL); err != nil {
			return err
		}
	}
	if o.releaseDates != nil && t.Type == core.TypeMovie {
		if err := saveHomeReleases(ctx, tx, t, o.releaseDates, now); err != nil {
			return err
		}
	}
	if o.airDates && !o.detail {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET fetched_at=CASE WHEN released_ts<>? OR year<>? THEN ? ELSE fetched_at END, released_ts=?, year=? WHERE key=? AND (released_ts<>? OR year<>?)`, releasedTS, t.Year, now, releasedTS, t.Year, int64(t.Key), releasedTS, t.Year); err != nil {
			return fmt.Errorf("catalog: upsert %d air date: %w", t.Key, err)
		}
	}
	if !fresh {
		if err := saveMovieYearSource(ctx, tx, t, detailInt); err != nil {
			return err
		}
	}
	// Replace join rows only when the incoming title carries that facet, so
	// summary upserts don't wipe detail-fetched genres/credits.
	joinChanged := false
	facets := [][]string{t.Genres, t.Keywords, t.AlternativeTitles, t.Studios, t.Countries}
	anyFacet := false
	for _, f := range facets {
		anyFacet = anyFacet || f != nil
	}
	if anyFacet {
		// Read even for a new row: facet rows may outlive a deleted title.
		stored, err := storedFacets(ctx, tx, int64(t.Key))
		if err != nil {
			return err
		}
		for i, f := range facetTables {
			if facets[i] == nil {
				continue
			}
			changed, err := replaceStoredPairs(ctx, tx, f.table, f.col, int64(t.Key), facets[i], stored[f.table])
			if err != nil {
				return err
			}
			joinChanged = joinChanged || changed
		}
	}
	if t.Cast != nil || t.Directors != nil || t.Writers != nil || t.Crew != nil {
		changed, err := replaceCredits(ctx, tx, t)
		if err != nil {
			return err
		}
		joinChanged = joinChanged || changed
	}
	if joinChanged {
		if _, err := tx.ExecContext(ctx, `UPDATE catalog_titles SET fetched_at=? WHERE key=?`, now, int64(t.Key)); err != nil {
			return err
		}
	}
	return nil
}

type creditKey struct {
	person     int64
	kind, role string
}

type creditRow struct {
	name             string
	ord              int
	thumb, provider  string
	imdb             string
	inserted, update bool
}

// replaceCredits makes t's stored credits exactly its cast and crew, writing
// only the difference: a refresh that recasts one part touches one row, not
// every credit with its person-name upkeep and index entries. The first
// credit of a (person, kind, role) wins, as the INSERT ... DO NOTHING it
// replaces kept it.
func replaceCredits(ctx context.Context, tx *sql.Tx, t core.Title) (bool, error) {
	want := map[creditKey]*creditRow{}
	var order []creditKey
	for _, group := range []struct {
		people []core.Person
		kind   string
	}{{t.Cast, "cast"}, {t.Directors, "director"}, {t.Writers, "writer"}, {t.Crew, "crew"}} {
		for i, p := range group.people {
			k := creditKey{p.TMDB, group.kind, p.Role}
			if want[k] != nil {
				continue
			}
			imdb := ""
			if p.TMDB > 0 && validPersonIMDB(p.IMDB) {
				imdb = p.IMDB
			}
			want[k] = &creditRow{name: p.Name, ord: i, thumb: p.ThumbPath, provider: p.ID, imdb: imdb, inserted: true}
			order = append(order, k)
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT person_tmdb,kind,role,name,ord,thumb,provider_id FROM catalog_credits WHERE key=?`, int64(t.Key))
	if err != nil {
		return false, err
	}
	var stale []creditKey
	for rows.Next() {
		var k creditKey
		var have creditRow
		if err := rows.Scan(&k.person, &k.kind, &k.role, &have.name, &have.ord, &have.thumb, &have.provider); err != nil {
			rows.Close()
			return false, err
		}
		w := want[k]
		if w == nil {
			stale = append(stale, k)
			continue
		}
		w.inserted = false
		w.update = w.name != have.name || w.ord != have.ord || w.thumb != have.thumb || w.provider != have.provider
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	changed := len(stale) > 0
	// Removals and additions are one statement each, in chunks that stay
	// well inside both engines' bound-parameter limits.
	const chunk = 200
	for start := 0; start < len(stale); start += chunk {
		part := stale[start:min(start+chunk, len(stale))]
		args := []any{int64(t.Key)}
		var conds []string
		for _, k := range part {
			conds = append(conds, "(person_tmdb=? AND kind=? AND role=?)")
			args = append(args, k.person, k.kind, k.role)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM catalog_credits WHERE key=? AND (`+strings.Join(conds, " OR ")+`)`, args...); err != nil {
			return false, err
		}
	}
	var added []any
	for _, k := range order {
		w := want[k]
		switch {
		case w.inserted:
			added = append(added, int64(t.Key), k.person, w.name, k.role, k.kind, w.ord, w.thumb, w.provider)
		case w.update:
			if _, err := tx.ExecContext(ctx, `UPDATE catalog_credits SET name=?,ord=?,thumb=?,provider_id=? WHERE key=? AND person_tmdb=? AND kind=? AND role=?`,
				w.name, w.ord, w.thumb, w.provider, int64(t.Key), k.person, k.kind, k.role); err != nil {
				return false, err
			}
			changed = true
		}
	}
	for start := 0; start < len(added); start += chunk * 8 {
		part := added[start:min(start+chunk*8, len(added))]
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_credits(key,person_tmdb,name,role,kind,ord,thumb,provider_id) VALUES `+valueRows(len(part)/8, 8)+` ON CONFLICT DO NOTHING`, part...); err != nil {
			return false, err
		}
		changed = true
	}
	if !changed {
		return false, nil
	}
	// One upsert of every credited person's IMDb id. A person appears once:
	// PostgreSQL refuses to update a row twice in one statement.
	var people []any
	seen := map[int64]bool{}
	for _, k := range order {
		if w := want[k]; w.imdb != "" && !seen[k.person] {
			seen[k.person] = true
			people = append(people, k.person, w.imdb)
		}
	}
	for start := 0; start < len(people); start += chunk * 2 {
		part := people[start:min(start+chunk*2, len(people))]
		if _, err := tx.ExecContext(ctx, `INSERT INTO catalog_people(person_tmdb,imdb) VALUES `+valueRows(len(part)/2, 2)+` ON CONFLICT(person_tmdb) DO UPDATE SET imdb=excluded.imdb WHERE catalog_people.imdb<>excluded.imdb`, part...); err != nil {
			return false, err
		}
	}
	return true, nil
}

func detailAtFor(detail int, now int64) int64 {
	if detail != 0 {
		return now
	}
	return 0
}

func addedAtFor(browsable int, now int64) int64 {
	if browsable != 0 {
		return now
	}
	return 0
}

func appendUnique(dst []string, v string) []string {
	if v == "" {
		return dst
	}
	for _, existing := range dst {
		if existing == v {
			return dst
		}
	}
	return append(dst, v)
}

func uniqueStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = appendUnique(out, v)
	}
	return out
}

// facetTables are the one-value-per-row facets a title upsert replaces.
var facetTables = []struct{ table, col string }{
	{"catalog_genres", "genre"}, {"catalog_keywords", "keyword"}, {"catalog_alt_titles", "title"},
	{"catalog_studios", "studio"}, {"catalog_countries", "country"},
}

// storedFacets reads key's stored values of every facet table in one
// statement, by table name.
func storedFacets(ctx context.Context, tx *sql.Tx, key int64) (map[string][]string, error) {
	var parts []string
	args := make([]any, 0, len(facetTables))
	for i, f := range facetTables {
		parts = append(parts, fmt.Sprintf(`SELECT %d, %s FROM %s WHERE key=?`, i, f.col, f.table))
		args = append(args, key)
	}
	rows, err := tx.QueryContext(ctx, strings.Join(parts, " UNION ALL "), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var i int
		var v string
		if err := rows.Scan(&i, &v); err != nil {
			return nil, err
		}
		out[facetTables[i].table] = append(out[facetTables[i].table], v)
	}
	return out, rows.Err()
}

// replaceStoredPairs makes key's values in table exactly vals, given the
// stored ones, writing only the difference. A refresh usually brings the
// same values, and one that adds a genre should not rewrite the others:
// every row written fires the search, tally and viewer-policy triggers and
// touches each index.
func replaceStoredPairs(ctx context.Context, tx *sql.Tx, table, col string, key int64, vals, stored []string) (bool, error) {
	want := make(map[string]bool, len(vals))
	for _, v := range vals {
		if v != "" {
			want[v] = true
		}
	}
	var stale []string
	have := make(map[string]bool, len(stored))
	for _, v := range stored {
		have[v] = true
		if !want[v] {
			stale = append(stale, v)
		}
	}
	// One statement for the removals and one for the additions, whatever
	// their number: a first detail fill adds a dozen keywords.
	if len(stale) > 0 {
		args := []any{key}
		for _, v := range stale {
			args = append(args, v)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE key=? AND `+col+` IN (`+placeholders(len(stale))+`)`, args...); err != nil {
			return false, err
		}
	}
	var added []any
	for _, v := range vals {
		if v == "" || have[v] {
			continue
		}
		have[v] = true
		added = append(added, key, v)
	}
	if len(added) > 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+`(key,`+col+`) VALUES `+valueRows(len(added)/2, 2)+` ON CONFLICT DO NOTHING`, added...); err != nil {
			return false, err
		}
	}
	return len(stale) > 0 || len(added) > 0, nil
}

// valueRows is n parenthesised rows of cols placeholders: "(?,?),(?,?)".
func valueRows(n, cols int) string {
	row := "(" + placeholders(cols) + ")"
	var b strings.Builder
	b.Grow(n * (len(row) + 1))
	for i := range n {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(row)
	}
	return b.String()
}

func (c *Catalog) queryTitles(ctx context.Context, q string, args ...any) ([]core.Title, error) {
	return c.queryTitlesMode(ctx, false, q, args...)
}

// queryPagedTitles is for queries whose SQL already applies all catalogue
// release and adult guards before LIMIT and OFFSET.
func (c *Catalog) queryPagedTitles(ctx context.Context, q string, args ...any) ([]core.Title, error) {
	return c.queryTitlesMode(ctx, true, q, args...)
}

func (c *Catalog) queryTitlesMode(ctx context.Context, sqlGated bool, q string, args ...any) ([]core.Title, error) {
	gen := c.writeGen.Load()
	minute := c.minuteBucket()
	rows, err := c.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Title
	for rows.Next() {
		st, err := scanTitle(rows)
		if err != nil {
			return nil, err
		}
		// Let hydration establish an undated Specials container's premiere
		// from its aired children; all other unknown dates remain excluded.
		undatedSpecials := st.t.Type == core.TypeSeason && st.t.Key.Season() == 0 && st.t.Released.IsZero()
		if sqlGated || (!st.t.Adult && (st.t.IsReleased(c.now()) || c.upcomingMovie(&st.t) || undatedSpecials)) {
			out = append(out, st.t)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	needAt := make([]int, 0, len(out))
	for i := range out {
		if cached, ok := c.cachedStored(out[i].Key, gen, minute); ok {
			out[i] = cached
			continue
		}
		needAt = append(needAt, i)
	}
	if len(needAt) > 0 {
		need := make([]core.Title, len(needAt))
		for j, i := range needAt {
			need[j] = out[i]
		}
		if err := c.hydrateSlice(ctx, need); err != nil {
			return nil, err
		}
		for j, i := range needAt {
			out[i] = need[j]
		}
	}
	visible := out
	if !sqlGated {
		visible = out[:0]
		for _, t := range out {
			if c.upcomingMovie(&t) || (t.IsReleased(c.now()) && (!(c.config().ReleaseMode == config.ModeDigital) || homeReleaseVisible(&t, c.now()))) {
				visible = append(visible, t)
			}
		}
	}
	c.rememberTitles(gen, minute, visible)
	return visible, nil
}

func (c *Catalog) getMeta(ctx context.Context, k string) string {
	var v string
	_ = c.db.QueryRowContext(ctx, `SELECT v FROM catalog_meta WHERE k=?`, k).Scan(&v)
	return v
}

// setMeta records a sync or sweep marker. No cached read depends on one,
// so it leaves the write generation, which every title, page and count
// cache is stamped with, alone.
func (c *Catalog) setMeta(ctx context.Context, k, v string) error {
	_, err := c.db.ExecContext(ctx, `INSERT INTO catalog_meta(k,v) VALUES(?,?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, k, v)
	return err
}

const (
	sortTitleFoldedKey     = "sort_title_folded"
	sortTitleFoldedVersion = "1"
	sortTitleFoldBatchSize = 2000
)

type sortTitleUpdate struct {
	key  int64
	sort string
}

// ensureSortTitlesFolded normalizes any pre-existing catalog_titles.sort_title
// rows once in keyset-paged batches so /all (ORDER BY sort_title) and
// /firstCharacter bucket counts share the same folded collation on existing
// databases without holding the write lock across the whole table.
func (c *Catalog) ensureSortTitlesFolded(ctx context.Context) {
	if c.getMeta(ctx, sortTitleFoldedKey) == sortTitleFoldedVersion {
		return
	}
	var total int
	if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles`).Scan(&total); err != nil {
		return
	}
	done := 0
	store.WorkProgress(ctx, "sort_titles", done, total)
	var updates []sortTitleUpdate
	lastKey := int64(math.MinInt64)
	for {
		if ctx.Err() != nil {
			return
		}
		rows, err := c.db.QueryContext(ctx,
			`SELECT key, title, sort_title FROM catalog_titles WHERE key > ? ORDER BY key ASC LIMIT ?`,
			lastKey, sortTitleFoldBatchSize)
		if err != nil {
			return
		}
		updates = updates[:0]
		pageRows := 0
		for rows.Next() {
			var key int64
			var title, stored string
			if err := rows.Scan(&key, &title, &stored); err != nil {
				rows.Close()
				return
			}
			lastKey = key
			pageRows++
			if want := foldedSortTitleForRow(title, stored); want != stored {
				updates = append(updates, sortTitleUpdate{key: key, sort: want})
			}
		}
		closeErr := rows.Close()
		if rows.Err() != nil || closeErr != nil {
			return
		}
		if len(updates) > 0 {
			if !c.applySortTitleBatch(ctx, updates) {
				return
			}
		}
		done += pageRows
		store.WorkProgress(ctx, "sort_titles", done, total)
		if pageRows < sortTitleFoldBatchSize {
			break
		}
	}
	_ = c.setMeta(ctx, sortTitleFoldedKey, sortTitleFoldedVersion)
}

func (c *Catalog) applySortTitleBatch(ctx context.Context, updates []sortTitleUpdate) bool {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return false
	}
	stmt, err := tx.PrepareContext(ctx, `UPDATE catalog_titles SET sort_title=? WHERE key=?`)
	if err != nil {
		_ = tx.Rollback()
		return false
	}
	for _, u := range updates {
		if _, err := stmt.ExecContext(ctx, u.sort, u.key); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return false
		}
	}
	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return false
	}
	if err := tx.Commit(); err != nil {
		return false
	}
	c.writeGen.Add(1)
	return true
}
