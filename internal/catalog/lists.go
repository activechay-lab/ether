package catalog

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/store"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/reqmemo"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func (c *Catalog) upsertSummaries(ctx context.Context, titles []core.Title, browsable bool) error {
	return c.upsertSummaryBatch(ctx, titles, browsable, false)
}

func (c *Catalog) upsertSummaryBatch(ctx context.Context, titles []core.Title, browsable, preserveDetailed bool) error {
	if browsable {
		return c.upsertMany(ctx, titles, upsertOpts{browsable: true, preserveDetailed: preserveDetailed})
	}
	// One transaction for the list, while retaining each title's threshold.
	// A transaction per summary adds two network round trips per tile in HA.
	if len(titles) == 0 {
		return nil
	}
	// Concurrent nodes may refresh overlapping lists in different rank order.
	// Take row locks in key order, while the caller retains its display order.
	titles = append([]core.Title(nil), titles...)
	sort.SliceStable(titles, func(i, j int) bool { return titles[i].Key < titles[j].Key })
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := c.now().Unix()
	for _, t := range titles {
		if err := upsertOne(ctx, tx, t, upsertOpts{browsable: c.meetsThreshold(t), preserveDetailed: preserveDetailed}, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	c.writeGen.Add(1)
	return nil
}

func limitTitles(items []core.Title, limit int) []core.Title {
	if limit > 0 && len(items) > limit {
		return items[:limit]
	}
	if items == nil {
		return []core.Title{}
	}
	return items
}

// reloadSummaries retains the source order and fallback used by the old
// per-title reads, while hydrating all saved summaries in bounded batches.
// It deliberately does not apply browse visibility: Collection may include
// future members, and its caller applies its own policy.
func (c *Catalog) reloadSummaries(ctx context.Context, titles []core.Title) []core.Title {
	if len(titles) == 0 {
		return titles
	}
	byKey := make(map[core.RatingKey]core.Title, len(titles))
	const batch = 200
	for chunk := range store.Chunks(titles, batch) {
		part := chunk
		args := make([]any, len(part))
		for i := range part {
			args[i] = int64(part[i].Key)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE key IN (`+placeholders(len(args))+`)`, args...)
		if err != nil {
			return c.reloadSummariesIndividually(ctx, titles)
		}
		var found []core.Title
		for rows.Next() {
			st, err := scanTitle(rows)
			if err != nil {
				rows.Close()
				return c.reloadSummariesIndividually(ctx, titles)
			}
			found = append(found, st.t)
		}
		err = rows.Err()
		rows.Close()
		if err != nil || c.hydrateSlice(ctx, found) != nil {
			return c.reloadSummariesIndividually(ctx, titles)
		}
		for _, t := range found {
			byKey[t.Key] = t
		}
	}
	out := make([]core.Title, 0, len(titles))
	for _, t := range titles {
		if found, ok := byKey[t.Key]; ok {
			out = append(out, found)
		} else {
			out = append(out, t)
		}
	}
	return out
}

func (c *Catalog) reloadSummariesIndividually(ctx context.Context, titles []core.Title) []core.Title {
	out := make([]core.Title, 0, len(titles))
	for _, t := range titles {
		if st, err := c.loadByKey(ctx, t.Key); err == nil {
			out = append(out, st.t)
		} else {
			out = append(out, t)
		}
	}
	return out
}

// reloadSearchRows retains reloadSummaries' source order and missing-row
// fallback, without fetching facts before the merged ranking is known.
func (c *Catalog) reloadSearchRows(ctx context.Context, titles []core.Title) []core.Title {
	byKey := make(map[core.RatingKey]core.Title, len(titles))
	const batch = 200
	for chunk := range store.Chunks(titles, batch) {
		part := chunk
		args := make([]any, len(part))
		for i := range part {
			args[i] = int64(part[i].Key)
		}
		rows, err := c.db.QueryContext(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE key IN (`+placeholders(len(args))+`)`, args...)
		if err != nil {
			return c.reloadSummaries(ctx, titles)
		}
		for rows.Next() {
			st, err := scanTitle(rows)
			if err != nil {
				rows.Close()
				return c.reloadSummaries(ctx, titles)
			}
			byKey[st.t.Key] = st.t
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return c.reloadSummaries(ctx, titles)
		}
	}
	out := make([]core.Title, 0, len(titles))
	for _, t := range titles {
		if row, ok := byKey[t.Key]; ok {
			t = row
		}
		out = append(out, t)
	}
	return out
}

// A search waits at most searchRemoteWait for TMDB when the mirror has too
// few matches, and searchRemoteGrace when it already has enough.
const (
	searchRemoteWait  = time.Second
	searchRemoteGrace = 150 * time.Millisecond
)

// Search reaches all of TMDB; matched titles join the browsable set.
func (c *Catalog) Search(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error) {
	return c.searchWithOpts(ctx, query, types, limit, false)
}

// SearchAll searches local and remote titles without applying browse-shelf
// visibility or home-release gating (used when importing external watch state).
func (c *Catalog) SearchAll(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error) {
	return c.searchWithOpts(ctx, query, types, limit, true)
}

func (c *Catalog) searchWithOpts(ctx context.Context, query string, types []core.MediaType, limit int, unfiltered bool) ([]core.Title, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []core.Title{}, nil
	}
	if limit <= 0 {
		limit = 20
	}
	items, err := c.searchTitlesOpts(ctx, query, types, limit, 0, unfiltered)
	if err != nil || len(items) > 0 {
		return items, err
	}
	title, year := searchTitleYear(query)
	if year == 0 {
		return items, nil
	}
	return c.searchTitlesOpts(ctx, title, types, limit, year, unfiltered)
}

// Only interpret a final, separated year after the literal query found nothing.
// Numeric titles and titles such as Blade Runner 2049 retain their literal match.
func searchTitleYear(query string) (string, int) {
	split := strings.LastIndexAny(query, " \t\n\r")
	if split < 1 {
		return query, 0
	}
	title, suffix := strings.TrimSpace(query[:split]), query[split+1:]
	if strings.HasPrefix(suffix, "(") && strings.HasSuffix(suffix, ")") {
		suffix = suffix[1 : len(suffix)-1]
	}
	if title == "" || len(suffix) != 4 {
		return query, 0
	}
	year, err := strconv.Atoi(suffix)
	if err != nil || year < 1900 || year > 2099 {
		return query, 0
	}
	return title, year
}

func (c *Catalog) searchTitlesOpts(ctx context.Context, query string, types []core.MediaType, limit, year int, unfiltered bool) ([]core.Title, error) {
	// Track origins so visibility and final hydration retain the different
	// local and remote semantics.
	localKeys := make(map[core.RatingKey]bool)
	remoteKeys := make(map[core.RatingKey]bool)
	deferredLocal := false
	finish := func(items []core.Title) ([]core.Title, error) {
		if !unfiltered {
			var remote []core.Title
			for _, item := range items {
				if !localKeys[item.Key] {
					remote = append(remote, item)
				}
			}
			remoteVisible := make(map[core.RatingKey]core.Title)
			for _, item := range c.FilterVisibility(ctx, remote) {
				remoteVisible[item.Key] = item
			}
			filtered := items[:0]
			f := c.visibilityFor(ctx)
			for _, item := range items {
				if localKeys[item.Key] {
					if c.visibleWith(f, &item) {
						filtered = append(filtered, item)
					}
				} else if visible, ok := remoteVisible[item.Key]; ok {
					filtered = append(filtered, visible)
				}
			}
			items = filtered
		}
		if year != 0 {
			var matching []core.Title
			for _, t := range items {
				if t.Year == year {
					matching = append(matching, t)
				}
			}
			if len(matching) > 0 {
				items = matching
			}
		}
		applySearchAffinity(ctx, items)
		sortTitlesForSearch(items)
		items = limitTitles(items, limit)
		if deferredLocal {
			keys := make([]core.RatingKey, 0, len(items))
			for _, t := range items {
				if localKeys[t.Key] {
					keys = append(keys, t.Key)
				}
			}
			loaded, err := c.searchCandidates(ctx, keys, unfiltered)
			if err != nil {
				return nil, err
			}
			byKey := make(map[core.RatingKey]core.Title, len(loaded))
			for _, t := range loaded {
				byKey[t.Key] = t
			}
			for i, t := range items {
				if localKeys[t.Key] {
					if full, ok := byKey[t.Key]; ok {
						full.SearchScore = t.SearchScore
						items[i] = full
					}
				}
			}
			var remote []core.Title
			for _, t := range items {
				if remoteKeys[t.Key] {
					remote = append(remote, t)
				}
			}
			loadedRemote := c.reloadSummaries(ctx, remote)
			byRemote := make(map[core.RatingKey]core.Title, len(loadedRemote))
			for _, t := range loadedRemote {
				byRemote[t.Key] = t
			}
			for i, t := range items {
				if full, ok := byRemote[t.Key]; ok {
					full.SearchScore = t.SearchScore
					items[i] = full
				}
			}
		}
		return items, nil
	}
	wantMovie, wantTV, wantSeason, wantEpisode := true, true, false, false
	if len(types) > 0 {
		wantMovie, wantTV = false, false
		for _, t := range types {
			switch t {
			case core.TypeMovie:
				wantMovie = true
			case core.TypeShow:
				wantTV = true
			case core.TypeSeason:
				wantSeason = true
			case core.TypeEpisode:
				wantEpisode = true
			}
		}
	}
	seen := map[core.RatingKey]bool{}
	var out []core.Title
	sq := parseSearchQuery(query)
	add := func(ts []core.Title) {
		for _, t := range ts {
			if t.Adult || (!unfiltered && !t.IsReleased(c.now())) || t.Key == 0 || seen[t.Key] {
				continue
			}
			seen[t.Key] = true
			if t.SearchScore == 0 {
				t.SearchScore = scoreSearchText(sq, &t)
			}
			out = append(out, t)
		}
	}
	wanted := func(ts []core.Title) []core.Title {
		var filtered []core.Title
		for _, t := range ts {
			if (t.Type == core.TypeMovie && wantMovie) || (t.Type == core.TypeShow && wantTV) || (t.Type == core.TypeSeason && wantSeason) || (t.Type == core.TypeEpisode && wantEpisode) {
				filtered = append(filtered, t)
			}
		}
		return filtered
	}
	// Local first (instant, works offline). Movies and shows come from the
	// in-memory title index (search_titles.go); seasons and episodes, and
	// every type until the index's first build, from the literal match.
	var localErr error
	ix := c.searchIndex(ctx, true)
	deferredLocal = year == 0 && !wantSeason && !wantEpisode && (unfiltered || (c.visibilityFor(ctx).Passive() && c.config().ReleaseMode != config.ModeDigital))
	if ix != nil && (wantMovie || wantTV) {
		keys, scores := c.searchKeys(ix, sq, wantMovie, wantTV, max(limit*3, 30))
		// When visibility needs no facets, rank and merge the lightweight
		// rows first. Only survivors need credits and other full metadata.
		var local []core.Title
		var err error
		if deferredLocal {
			local, err = c.searchCandidateRows(ctx, keys, unfiltered)
		} else {
			local, err = c.searchCandidates(ctx, keys, unfiltered)
		}
		if err != nil {
			localErr = err
		}
		for i := range local {
			local[i].SearchScore = scores[local[i].Key]
		}
		add(wanted(local))
		for _, item := range local {
			if seen[item.Key] {
				localKeys[item.Key] = true
			}
		}
	}
	if ix == nil || wantSeason || wantEpisode {
		types := []string{}
		if ix == nil {
			types = append(types, "'movie'", "'show'")
		}
		if wantSeason {
			types = append(types, "'season'")
		}
		if wantEpisode {
			types = append(types, "'episode'")
		}
		if len(types) > 0 {
			local, err := c.searchLiteral(ctx, query, strings.Join(types, ","), year, limit*2, unfiltered, ix == nil, deferredLocal)
			if err != nil && localErr == nil {
				localErr = err
			}
			add(wanted(local))
			for _, item := range local {
				if seen[item.Key] {
					localKeys[item.Key] = true
				}
			}
		}
	}
	// The long tail (tail_index.go): every other title in TMDB's export,
	// from this node's own index. Hits are summaries, never stored; the
	// visibility pass treats them as remote. A title whose date is not
	// known yet is let through; a confirmed undated or future one is not,
	// as for library titles.
	if wantMovie || wantTV {
		hits, scores := c.searchTail(sq, wantMovie, wantTV, max(limit*3, 30))
		tail := make([]core.Title, 0, len(hits))
		for _, e := range hits {
			t := e.Title
			if seen[t.Key] || (!unfiltered && !e.DateUnknown && !t.IsReleased(c.now())) {
				continue
			}
			tail = append(tail, t)
		}
		if len(tail) > 0 {
			if prefetch := c.posterPrefetch; prefetch != nil {
				prefetch(ctx, tail) // posters (L), overlapping the TMDB wait
			}
			out = mergeTailHits(out, tail, scores, limit)
			for _, t := range out {
				seen[t.Key] = true
			}
		}
	}
	// A missing or failing metadata service must not fail the request.
	// Context cancellation still aborts. A local query error is returned
	// only when there is nothing else to answer with.
	localAnswer := func() ([]core.Title, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if localErr != nil && len(out) == 0 {
			return nil, localErr
		}
		return finish(out)
	}
	if c.tmdb() == nil || (!wantMovie && !wantTV) {
		return localAnswer()
	}
	kinds := fmt.Sprintf("%t%t", wantMovie, wantTV)
	// TMDB widens the search beyond the mirror. It runs detached and is
	// waited for briefly: a search never waits long on the provider, and
	// results that arrive late are already in the mirror for the next one.
	fetch := func(ctx context.Context) (any, error) {
		var remote []core.Title
		if wantMovie && wantTV {
			for page := 1; page <= 3 && len(remote) < limit*2; page++ {
				res, err := c.tmdb().SearchMulti(ctx, query, page)
				if err != nil {
					if page == 1 {
						return nil, err
					}
					break
				}
				for _, r := range res.Results {
					switch r.MediaType {
					case "movie":
						remote = append(remote, c.movieToTitle(ctx, r.AsMovie()))
					case "tv":
						remote = append(remote, c.tvToTitle(ctx, r.AsTV()))
					}
				}
				if page >= res.TotalPages {
					break
				}
			}
		} else if wantMovie {
			res, err := c.tmdb().SearchMovies(ctx, query, 1)
			if err != nil {
				return nil, err
			}
			for _, m := range res.Results {
				remote = append(remote, c.movieToTitle(ctx, m))
			}
		} else if wantTV {
			res, err := c.tmdb().SearchTV(ctx, query, 1)
			if err != nil {
				return nil, err
			}
			for _, s := range res.Results {
				remote = append(remote, c.tvToTitle(ctx, s))
			}
		}
		// Search hits are summaries in memory only: a title is stored when
		// someone opens it (Get), never because it matched a search.
		c.rememberListSummaries(remote)
		return remote, nil
	}
	wait := searchRemoteWait
	switch {
	case slices.ContainsFunc(out, func(t core.Title) bool { return t.SearchScore >= searchConfident }):
		// The mirror holds the title the viewer typed: answer now. TMDB
		// still runs, detached, and what it finds joins the mirror (and the
		// search index) for the next search.
		wait = 0
	case len(out) >= limit:
		// The mirror already answers the search; take TMDB's results only if
		// they are immediate.
		wait = searchRemoteGrace
	}
	got, err := c.hydrateValue(ctx, "search:"+kinds+":"+strconv.Itoa(limit)+":"+query, wait, fetch)
	if err != nil {
		// TMDB is slow, failing or still answering: the mirror's matches stand.
		return localAnswer()
	}
	remote, _ := got.([]core.Title)
	if len(remote) > 0 {
		// Reload so callers get hydrated/genres-preserved rows, in one
		// batch rather than a read per result.
		loaded := c.reloadSummaries
		if deferredLocal {
			loaded = c.reloadSearchRows
		}
		for _, t := range loaded(ctx, remote) {
			if t.SearchScore = scoreSearchText(sq, &t); t.SearchScore == 0 {
				// TMDB matched a name the catalogue does not hold.
				t.SearchScore = searchRemoteOnly + popularityPrior(t.Popularity, t.VoteCount)
			}
			wasSeen := seen[t.Key]
			add([]core.Title{t})
			if deferredLocal && !wasSeen && seen[t.Key] {
				remoteKeys[t.Key] = true
			}
		}
	}
	return finish(out)
}

func (c *Catalog) queryTitlesUnfiltered(ctx context.Context, q string, args ...any) ([]core.Title, error) {
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
		if !st.t.Adult {
			out = append(out, st.t)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := c.hydrateSlice(ctx, out); err != nil {
		return nil, err
	}
	return out, nil
}

// sortTitlesForSearch orders search results best match first (SearchScore,
// see search_titles.go), then most popular.
func sortTitlesForSearch(items []core.Title) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].SearchScore != items[j].SearchScore {
			return items[i].SearchScore > items[j].SearchScore
		}
		return items[i].Popularity > items[j].Popularity
	})
}

// searchLiteral is the literal title match over types (a quoted SQL list):
// the most popular titles whose name holds query. Without the title index
// (walk) a dense movie and show term is read from the popularity index.
func (c *Catalog) searchLiteral(ctx context.Context, query, types string, year, n int, unfiltered, walk, deferFacts bool) ([]core.Title, error) {
	if walk && year == 0 {
		if keys, ok, err := c.searchByPopularity(ctx, query, n, unfiltered); err == nil && ok {
			if deferFacts {
				return c.searchCandidateRows(ctx, keys, unfiltered)
			}
			return c.searchCandidates(ctx, keys, unfiltered)
		}
	}
	match, args := c.titleMatch("", query)
	adult := ""
	if unfiltered {
		adult = "adult=0 AND "
	}
	q := `SELECT ` + titleCols + ` FROM catalog_titles WHERE ` + adult + `type IN (` + types + `) AND ` + match + ` ORDER BY CASE WHEN ?>0 AND year=? THEN 0 ELSE 1 END, popularity DESC LIMIT ?`
	args = append(args, year, year, n)
	if deferFacts {
		return c.querySearchRows(ctx, unfiltered, q, args...)
	}
	if unfiltered {
		return c.queryTitlesUnfiltered(ctx, q, args...)
	}
	return c.queryTitles(ctx, q, args...)
}

// List returns TMDB curated/trending lists with a short in-memory TTL.
func (c *Catalog) List(ctx context.Context, list string, t core.MediaType, limit int) ([]core.Title, error) {
	if limit <= 0 {
		limit = 20
	}
	if list == "trending_day" || list == "trending_week" {
		return c.trendingList(ctx, list, t, limit)
	}
	cacheKey := fmt.Sprint(core.CatalogCandidates(ctx)) + list + "|" + string(t) + "|" + strconv.Itoa(limit) + fmt.Sprint("|viewer:", core.CatalogViewerID(ctx), "|revision:", c.ViewerRulesRevision(&core.User{ID: core.CatalogViewerID(ctx)}))
	c.listMu.Lock()
	if e, ok := c.listCache[cacheKey]; ok && c.now().Before(e.expires) {
		items := append([]core.Title(nil), e.items...)
		c.listMu.Unlock()
		return c.releasedTitles(ctx, c.FilterVisibility(ctx, items)), nil
	}
	c.listMu.Unlock()
	if c.tmdb() == nil {
		// Offline fallback: best-effort browse by popularity/rating.
		sortOrder := core.SortPopularity
		if list == "top_rated" {
			sortOrder = core.SortRating
		}
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: t}, Sort: sortOrder, Desc: true, Limit: limit})
		if err != nil {
			return nil, err
		}
		return page.Items, nil
	}
	items, err := c.fetchList(ctx, list, t, limit)
	if err != nil {
		return nil, err
	}
	if err := c.mirrorListSummaries(ctx, "list:"+cacheKey, items, false); err != nil {
		return nil, err
	}
	// Hydrate the list together: the facet queries are already batch-aware.
	keys := make([]core.RatingKey, 0, len(items))
	for _, it := range items {
		keys = append(keys, it.Key)
	}
	hydrated, loadErr := c.titlesByKeys(ctx, keys)
	byKey := make(map[core.RatingKey]core.Title, len(hydrated))
	for _, t := range hydrated {
		byKey[t.Key] = t
	}
	out := make([]core.Title, 0, len(items))
	for _, it := range items {
		if t, ok := byKey[it.Key]; ok {
			out = append(out, t)
		} else if loadErr != nil {
			if st, err := c.loadByKey(ctx, it.Key); err == nil {
				out = append(out, st.t)
			} else {
				out = append(out, it)
			}
		} else {
			out = append(out, it)
		}
	}
	out = limitTitles(c.releasedTitles(ctx, out), limit)
	c.listMu.Lock()
	c.listCache[cacheKey] = listEntry{items: append([]core.Title(nil), out...), expires: c.now().Add(c.listTTL)}
	c.listMu.Unlock()
	return out, nil
}

func (c *Catalog) fetchList(ctx context.Context, list string, t core.MediaType, limit int) ([]core.Title, error) {
	pages := (limit + 19) / 20
	if pages < 1 {
		pages = 1
	}
	maxPages := 5
	if list == "trending_day" || list == "trending_week" {
		maxPages = trendPoolSize / 20
	}
	if pages > maxPages {
		pages = maxPages
	}
	collectMovies := func(fn func(page int) (*tmdb.Paged[tmdb.Movie], error)) ([]core.Title, error) {
		var out []core.Title
		for p := 1; p <= pages; p++ {
			res, err := fn(p)
			if err != nil {
				return out, err
			}
			for _, m := range res.Results {
				out = append(out, c.movieToTitle(ctx, m))
			}
			if p >= res.TotalPages || len(out) >= limit {
				break
			}
		}
		return out, nil
	}
	collectTV := func(fn func(page int) (*tmdb.Paged[tmdb.TVShow], error)) ([]core.Title, error) {
		var out []core.Title
		for p := 1; p <= pages; p++ {
			res, err := fn(p)
			if err != nil {
				return out, err
			}
			for _, s := range res.Results {
				out = append(out, c.tvToTitle(ctx, s))
			}
			if p >= res.TotalPages || len(out) >= limit {
				break
			}
		}
		return out, nil
	}
	switch list {
	case "trending_day", "trending_week":
		window := "week"
		if list == "trending_day" {
			window = "day"
		}
		switch t {
		case core.TypeMovie:
			return collectMovies(func(p int) (*tmdb.Paged[tmdb.Movie], error) { return c.tmdb().TrendingMovies(ctx, window, p) })
		case core.TypeShow:
			return collectTV(func(p int) (*tmdb.Paged[tmdb.TVShow], error) { return c.tmdb().TrendingTV(ctx, window, p) })
		default:
			var out []core.Title
			for p := 1; p <= pages; p++ {
				res, err := c.tmdb().TrendingAll(ctx, window, p)
				if err != nil {
					return out, err
				}
				for _, r := range res.Results {
					if r.MediaType == "movie" {
						out = append(out, c.movieToTitle(ctx, r.AsMovie()))
					} else if r.MediaType == "tv" {
						out = append(out, c.tvToTitle(ctx, r.AsTV()))
					}
				}
				if p >= res.TotalPages || len(out) >= limit {
					break
				}
			}
			return out, nil
		}
	case "popular", "top_rated", "now_playing", "upcoming", "airing_today", "on_the_air":
		if t == core.TypeShow {
			l := list
			if l == "now_playing" {
				l = "airing_today"
			}
			if l == "upcoming" {
				l = "on_the_air"
			}
			return collectTV(func(p int) (*tmdb.Paged[tmdb.TVShow], error) { return c.tmdb().TVList(ctx, l, p) })
		}
		l := list
		if l == "airing_today" || l == "on_the_air" {
			l = "now_playing"
		}
		return collectMovies(func(p int) (*tmdb.Paged[tmdb.Movie], error) { return c.tmdb().MovieList(ctx, l, p) })
	default:
		return nil, fmt.Errorf("catalog: unknown list %q", list)
	}
}

// Similar returns TMDB recommendations ∪ similar (recommendations first).
func (c *Catalog) Similar(ctx context.Context, key core.RatingKey, limit int) ([]core.Title, error) {
	if limit <= 0 {
		limit = 20
	}
	items, err := reqmemo.Do(ctx, fmt.Sprintf("catalog:similar:%p:%d:%d", c, key, limit), func() ([]core.Title, error) {
		return c.similar(ctx, key, limit)
	})
	return copySummaryTitles(items), err
}

func copySummaryTitles(items []core.Title) []core.Title {
	if items == nil {
		return nil
	}
	out := make([]core.Title, len(items))
	for i := range items {
		t := items[i]
		t.Genres = slices.Clone(t.Genres)
		t.Keywords = slices.Clone(t.Keywords)
		t.AlternativeTitles = slices.Clone(t.AlternativeTitles)
		t.ShowAlternativeTitles = slices.Clone(t.ShowAlternativeTitles)
		t.Studios = slices.Clone(t.Studios)
		t.Countries = slices.Clone(t.Countries)
		t.Cast = slices.Clone(t.Cast)
		t.Directors = slices.Clone(t.Directors)
		t.Writers = slices.Clone(t.Writers)
		t.Crew = slices.Clone(t.Crew)
		out[i] = t
	}
	return out
}

func (c *Catalog) similar(ctx context.Context, key core.RatingKey, limit int) ([]core.Title, error) {
	// Resolve seasons/episodes to their show.
	kind := key.Kind()
	id := key.TMDB()
	var out []core.Title
	seen := map[core.RatingKey]bool{key: true}
	if kind == core.KindSeason || kind == core.KindEpisode {
		seen[core.ShowKey(id)] = true
	}
	add := func(ts []core.Title) {
		for _, t := range ts {
			if !seen[t.Key] {
				seen[t.Key] = true
				out = append(out, t)
			}
		}
	}
	if c.tmdb() == nil {
		// Offline: same genres via browse is the recommender's job; return nothing.
		return []core.Title{}, nil
	}
	switch kind {
	case core.KindMovie:
		if res, err := c.tmdb().MovieRecommendations(ctx, id, 1); err == nil {
			for _, m := range res.Results {
				add([]core.Title{c.movieToTitle(ctx, m)})
			}
		} else if res2, err2 := c.tmdb().MovieSimilar(ctx, id, 1); err2 != nil {
			return nil, err
		} else {
			for _, m := range res2.Results {
				add([]core.Title{c.movieToTitle(ctx, m)})
			}
			break
		}
		if res, err := c.tmdb().MovieSimilar(ctx, id, 1); err == nil {
			for _, m := range res.Results {
				add([]core.Title{c.movieToTitle(ctx, m)})
			}
		}
	case core.KindShow, core.KindSeason, core.KindEpisode:
		if res, err := c.tmdb().TVRecommendations(ctx, id, 1); err == nil {
			for _, s := range res.Results {
				add([]core.Title{c.tvToTitle(ctx, s)})
			}
		} else if res2, err2 := c.tmdb().TVSimilar(ctx, id, 1); err2 != nil {
			return nil, err
		} else {
			for _, s := range res2.Results {
				add([]core.Title{c.tvToTitle(ctx, s)})
			}
			break
		}
		if res, err := c.tmdb().TVSimilar(ctx, id, 1); err == nil {
			for _, s := range res.Results {
				add([]core.Title{c.tvToTitle(ctx, s)})
			}
		}
	default:
		return nil, fmt.Errorf("%w: key %s", core.ErrNotFound, key)
	}
	out = limitTitles(c.releasedTitles(ctx, out), limit)
	if err := c.mirrorListSummaries(ctx, fmt.Sprintf("similar:%d:%d", key, limit), out, false); err != nil {
		return nil, err
	}
	reloaded := c.reloadSummaries(ctx, out)
	return c.releasedTitles(ctx, reloaded), nil
}

// Collection returns a TMDB franchise and its parts sorted by release date.
func (c *Catalog) Collection(ctx context.Context, tmdbCollection int64) (string, []core.Title, error) {
	type collectionResult struct {
		name  string
		items []core.Title
	}
	r, err := reqmemo.Do(ctx, fmt.Sprintf("catalog:collection:%p:%d", c, tmdbCollection), func() (collectionResult, error) {
		name, items, err := c.collection(ctx, tmdbCollection)
		return collectionResult{name, items}, err
	})
	return r.name, copySummaryTitles(r.items), err
}

func (c *Catalog) collection(ctx context.Context, tmdbCollection int64) (string, []core.Title, error) {
	if tmdbCollection <= 0 {
		return "", nil, core.ErrNotFound
	}
	if c.tmdb() != nil {
		d, err := c.tmdb().CollectionDetails(ctx, tmdbCollection)
		if err == nil {
			// The page has the franchise's fields; storing them is behind it.
			_ = c.hydrate(ctx, fmt.Sprintf("collection-save:%d", d.ID), 0, func(ctx context.Context) error {
				c.saveCollection(ctx, d.ID, d.Name, d.Overview, d.PosterPath, d.BackdropPath)
				return nil
			})
			var items []core.Title
			for _, m := range d.Parts {
				t := c.movieToTitle(ctx, m)
				t.CollectionID = d.ID
				t.CollectionName = d.Name
				items = append(items, t)
			}
			sort.Slice(items, func(i, j int) bool { return items[i].Released.Before(items[j].Released) })
			if err := c.mirrorListSummaries(ctx, fmt.Sprintf("collection:%d", tmdbCollection), items, false); err != nil {
				return d.Name, nil, err
			}
			out := c.reloadSummaries(ctx, items)
			return d.Name, c.releasedTitles(ctx, out), nil
		} else if isNotFound(err) {
			return "", nil, err
		}
		// fall through to local on transient errors
	}
	var name string
	_ = c.db.QueryRowContext(ctx, `SELECT name FROM catalog_collections WHERE tmdb_id=?`, tmdbCollection).Scan(&name)
	items, err := c.queryTitles(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE collection_id=? AND type='movie' ORDER BY released_ts ASC`, tmdbCollection)
	if err != nil {
		return "", nil, err
	}
	if len(items) == 0 {
		return "", nil, core.ErrNotFound
	}
	if name == "" && len(items) > 0 {
		name = items[0].CollectionName
	}
	return name, c.releasedTitles(ctx, items), nil
}

func isNotFound(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "not found") || err == core.ErrNotFound)
}

// ByExternalID looks up by imdb ("tt123") or tvdb id.
func (c *Catalog) ByExternalID(ctx context.Context, source, id string) (*core.Title, error) {
	return c.byExternalID(ctx, source, id, "", false)
}

// ByExternalIDForType looks up an external ID restricted to typ ("movie",
// "show", or "episode"), avoiding cross-type integer ID collisions on TVDB and
// allowing stored non-adult titles even when gated on browse shelves.
func (c *Catalog) ByExternalIDForType(ctx context.Context, source, id string, typ core.MediaType) (*core.Title, error) {
	return c.byExternalID(ctx, source, id, typ, true)
}

func (c *Catalog) getOrStored(ctx context.Context, key core.RatingKey, allowUnfiltered bool) (*core.Title, error) {
	t, err := c.Get(ctx, key)
	if err != nil && allowUnfiltered {
		if st, serr := c.loadByKey(ctx, key); serr == nil && st != nil && !st.t.Adult {
			return &st.t, nil
		}
	}
	return t, err
}

func (c *Catalog) byExternalID(ctx context.Context, source, id string, typ core.MediaType, allowUnfiltered bool) (*core.Title, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, core.ErrNotFound
	}
	queryFn := c.queryTitles
	if allowUnfiltered {
		queryFn = c.queryTitlesUnfiltered
	}
	// Local first.
	var items []core.Title
	var err error
	switch source {
	case "imdb", "imdb_id":
		if typ == core.TypeMovie || typ == core.TypeShow || typ == core.TypeEpisode {
			items, err = queryFn(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE imdb=? AND type=? LIMIT 1`, id, string(typ))
		} else {
			items, err = queryFn(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE imdb=? AND type IN ('movie','show','episode') ORDER BY CASE type WHEN 'episode' THEN 1 ELSE 0 END LIMIT 1`, id)
		}
	case "tvdb", "tvdb_id":
		n, perr := strconv.ParseInt(id, 10, 64)
		if perr != nil {
			return nil, fmt.Errorf("catalog: tvdb id %q: %w", id, perr)
		}
		if typ == core.TypeMovie || typ == core.TypeShow || typ == core.TypeEpisode {
			items, err = queryFn(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE tvdb=? AND type=? LIMIT 1`, n, string(typ))
		} else {
			items, err = queryFn(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE tvdb=? AND type IN ('movie','show') ORDER BY CASE type WHEN 'show' THEN 0 ELSE 1 END LIMIT 1`, n)
		}
	default:
		return nil, fmt.Errorf("catalog: unsupported external source %q", source)
	}
	if err != nil {
		return nil, err
	}
	if len(items) > 0 {
		// Return the detail-enriched version.
		return c.getOrStored(ctx, items[0].Key, allowUnfiltered)
	}
	if c.tmdb() == nil {
		return nil, core.ErrNotFound
	}
	found, err := c.tmdb().FindByExternalID(ctx, source, id)
	if err != nil {
		return nil, err
	}
	pickMovie := func() (*core.Title, bool, error) {
		if len(found.MovieResults) == 0 {
			return nil, false, nil
		}
		t := c.movieToTitle(ctx, found.MovieResults[0])
		if err := c.upsertSummaries(ctx, []core.Title{t}, false); err != nil {
			return nil, true, err
		}
		res, err := c.getOrStored(ctx, t.Key, allowUnfiltered)
		return res, true, err
	}
	pickTV := func() (*core.Title, bool, error) {
		if len(found.TVResults) == 0 {
			return nil, false, nil
		}
		t := c.tvToTitle(ctx, found.TVResults[0])
		if err := c.upsertSummaries(ctx, []core.Title{t}, false); err != nil {
			return nil, true, err
		}
		res, err := c.getOrStored(ctx, t.Key, allowUnfiltered)
		return res, true, err
	}
	pickEpisode := func() (*core.Title, bool, error) {
		for _, e := range found.TVEpisodeResults {
			if e.ShowID != 0 && episodeKeyFits(e.SeasonNumber, e.EpisodeNumber) {
				res, err := c.getOrStored(ctx, core.EpisodeKey(e.ShowID, e.SeasonNumber, e.EpisodeNumber), allowUnfiltered)
				return res, true, err
			}
		}
		return nil, false, nil
	}
	switch typ {
	case core.TypeMovie:
		if res, ok, err := pickMovie(); ok {
			return res, err
		}
		return nil, core.ErrNotFound
	case core.TypeShow:
		if res, ok, err := pickTV(); ok {
			return res, err
		}
		return nil, core.ErrNotFound
	case core.TypeEpisode:
		if res, ok, err := pickEpisode(); ok {
			return res, err
		}
		return nil, core.ErrNotFound
	}
	if source == "tvdb" || source == "tvdb_id" {
		if res, ok, err := pickTV(); ok {
			return res, err
		}
		if res, ok, err := pickMovie(); ok {
			return res, err
		}
	} else {
		if res, ok, err := pickMovie(); ok {
			return res, err
		}
		if res, ok, err := pickTV(); ok {
			return res, err
		}
	}
	if res, ok, err := pickEpisode(); ok {
		return res, err
	}
	return nil, core.ErrNotFound
}

// Apply the same release rule to remote lists and cached responses as SQL reads.
func (c *Catalog) releasedTitles(ctx context.Context, items []core.Title) []core.Title {
	out := make([]core.Title, 0, len(items))
	for _, t := range items {
		if !t.Adult && t.IsReleased(c.now()) && c.visibleWith(c.visibilityFor(ctx), &t) {
			out = append(out, t)
		}
	}
	return out
}

// searchCandidateRows reads the same title rows as searchCandidates without
// their facts. It retains key order and the ordinary release and cache rules.
func (c *Catalog) searchCandidateRows(ctx context.Context, keys []core.RatingKey, unfiltered bool) ([]core.Title, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = int64(k)
	}
	titles, err := c.querySearchRows(ctx, unfiltered, `SELECT `+titleCols+` FROM catalog_titles WHERE key IN (`+placeholders(len(keys))+`)`, args...)
	if err != nil {
		return nil, err
	}
	byKey := make(map[core.RatingKey]core.Title, len(titles))
	for _, t := range titles {
		byKey[t.Key] = t
	}
	out := make([]core.Title, 0, len(keys))
	for _, key := range keys {
		if t, ok := byKey[key]; ok {
			out = append(out, t)
		}
	}
	return out, nil
}

func (c *Catalog) querySearchRows(ctx context.Context, unfiltered bool, query string, args ...any) ([]core.Title, error) {
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	gen, minute := c.writeGen.Load(), c.minuteBucket()
	var out []core.Title
	for rows.Next() {
		st, err := scanTitle(rows)
		if err != nil {
			return nil, err
		}
		t := st.t
		if t.Adult || (!unfiltered && !t.IsReleased(c.now()) && !c.upcomingMovie(&t)) {
			continue
		}
		if !unfiltered {
			if cached, ok := c.cachedStored(t.Key, gen, minute); ok {
				t = cached
			}
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// searchCandidates reads and hydrates titles in the supplied key order.
func (c *Catalog) searchCandidates(ctx context.Context, keys []core.RatingKey, unfiltered bool) ([]core.Title, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	args := make([]any, len(keys))
	at := make(map[core.RatingKey]int, len(keys))
	for i, k := range keys {
		args[i] = int64(k)
		at[k] = i
	}
	q := `SELECT ` + titleCols + ` FROM catalog_titles WHERE key IN (` + placeholders(len(keys)) + `)`
	var titles []core.Title
	var err error
	if unfiltered {
		titles, err = c.queryTitlesUnfiltered(ctx, q, args...)
	} else {
		titles, err = c.queryTitles(ctx, q, args...)
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(titles, func(i, j int) bool { return at[titles[i].Key] < at[titles[j].Key] })
	return titles, nil
}
