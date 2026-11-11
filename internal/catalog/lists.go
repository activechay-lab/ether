package catalog

import (
	"context"
	"errors"
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
	hydrateLocalLate := false
	// A caller ranking the candidate window itself reads its page in full
	// (core.SearchCandidates); a summary search shows rows as they are.
	cand := core.SearchCandidatesFrom(ctx)
	if cand == nil && core.SearchSummaryFacts(ctx) {
		cand = &core.SearchCandidates{Facts: core.VisibilityFacts}
	}
	// partial holds the library titles read as rows, with windowFacts: the
	// facts the visibility rules and the release gate decide by. The rest
	// waits for the titles that survive.
	// A deferred search (no rule or gate decides by a fact) reads none.
	partial := make(map[core.RatingKey]bool)
	var windowFacts core.TitleFacts
	if !unfiltered {
		windowFacts = c.policyFacts(c.visibilityFor(ctx))
	}
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
					if c.visibleFor(ctx, f, &item) {
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
		if (deferredLocal || hydrateLocalLate) && cand != nil {
			// The caller's candidate window: library rows stay partial,
			// with the facts its policy and filters read.
			if err := c.finishCandidates(ctx, cand, items, partial, windowFacts); err != nil {
				return nil, err
			}
		} else if deferredLocal || hydrateLocalLate {
			// The page's library titles in full, read once in a batch and
			// cached as Get caches them, so the frontend's own reads of
			// these titles (visibility, playability, a show's seasons) hit
			// the cache instead of loading each one again.
			keys := make([]core.RatingKey, 0, len(items))
			for _, t := range items {
				if localKeys[t.Key] && (t.Type == core.TypeMovie || t.Type == core.TypeShow) {
					keys = append(keys, t.Key)
				}
			}
			if core.SearchListFacts(ctx) {
				// Shown as list items: the facts a list shows, not cached.
				loaded, err := c.searchListRows(ctx, keys)
				if err != nil {
					return nil, err
				}
				for i, t := range items {
					if full, ok := loaded[t.Key]; ok && localKeys[t.Key] {
						full.SearchScore = t.SearchScore
						items[i] = full
					}
				}
			} else {
				c.PreloadStored(ctx, keys)
				for i, t := range items {
					if !localKeys[t.Key] || (t.Type != core.TypeMovie && t.Type != core.TypeShow) {
						continue
					}
					if st, err := c.loadByKey(ctx, t.Key); err == nil {
						full := st.t
						full.SearchScore = t.SearchScore
						items[i] = full
					} else if !errors.Is(err, core.ErrNotFound) {
						return nil, err
					}
				}
			}
		}
		if deferredLocal {
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
		// Posters last, on the final page: ranking never sees them. A long-
		// tail hit whose poster is unknown is looked up under posterWait;
		// one that misses it shows Ether's generated poster this time.
		c.FillPosters(ctx, items, func(t *core.Title) bool { return !localKeys[t.Key] && c.tailPosterUnknown(ctx, t) }, posterWait)
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
		// The window is three pages: read its rows with only the facts the
		// visibility rules and the release gate decide by (none when they
		// decide by nothing). Only the page that survives is read in full,
		// in finish, or by the caller of a candidate search.
		local, err := c.searchCandidateRows(ctx, keys, unfiltered, cand != nil)
		if err == nil {
			err = c.hydrateFacts(ctx, local, windowFacts)
		}
		if err != nil {
			localErr = err
		}
		hydrateLocalLate = true
		for i := range local {
			local[i].SearchScore = scores[local[i].Key]
		}
		add(wanted(local))
		for _, item := range local {
			if seen[item.Key] {
				localKeys[item.Key] = true
				partial[item.Key] = true
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
			// Deferred, or for a candidate search, the literal match reads
			// movies and shows as rows, as the title index's are read.
			rows := deferredLocal || (cand != nil && !unfiltered)
			local, err := c.searchLiteral(ctx, query, strings.Join(types, ","), year, limit*2, unfiltered, ix == nil, deferredLocal, cand != nil && !unfiltered)
			if err == nil && rows {
				hydrateLocalLate = true
				var movies []core.Title
				var at []int
				for i := range local {
					if local[i].Type == core.TypeMovie || local[i].Type == core.TypeShow {
						movies = append(movies, local[i])
						at = append(at, i)
					}
				}
				err = c.hydrateFacts(ctx, movies, windowFacts)
				for j, i := range at {
					local[i] = movies[j]
				}
			}
			if err != nil && localErr == nil {
				localErr = err
			}
			add(wanted(local))
			for _, item := range local {
				if seen[item.Key] {
					localKeys[item.Key] = true
					if rows && (item.Type == core.TypeMovie || item.Type == core.TypeShow) {
						partial[item.Key] = true
					}
				}
			}
		}
	}
	// The long tail (tail_index.go): every other title in TMDB's export,
	// from this node's own index. Hits are summaries, never stored; the
	// visibility pass treats them as remote. A title whose date is not
	// known yet is let through; a confirmed undated or future one is not,
	// as for library titles.
	// Tail hits rank under the outside margin (search_tail_merge.go); one
	// that is the exact title answers without waiting for TMDB.
	tailKeys := map[core.RatingKey]bool{}
	tailAnswered := false
	if wantMovie || wantTV {
		hits, scores := c.searchTail(sq, wantMovie, wantTV, max(limit*3, 30))
		tail := make([]core.Title, 0, len(hits))
		for _, e := range hits {
			t := e.Title
			if seen[t.Key] || c.inLibrary(t.Key) || (!unfiltered && !e.DateUnknown && !t.IsReleased(c.now())) {
				continue
			}
			tail = append(tail, t)
			tailKeys[t.Key] = true
		}
		tailAnswered = tailAnswers(tail, scores)
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
	searchKey := "search:" + kinds + ":" + strconv.Itoa(limit) + ":" + query
	remoteKey := c.config().Language + ":" + searchKey
	// TMDB widens the search beyond the mirror. It runs detached and is
	// waited for briefly: a search never waits long on the provider, and
	// results that arrive late answer the next one (remoteSearches).
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
		c.remoteSearches.put(remoteKey, remote, c.now())
		return remote, nil
	}
	wait := searchRemoteWait
	switch {
	case tailAnswered || slices.ContainsFunc(out, func(t core.Title) bool { return t.SearchScore >= searchConfident }):
		// The mirror holds the title the viewer typed: answer now. TMDB
		// still runs, detached, and what it finds answers the next search
		// (remoteSearches).
		wait = 0
	case len(out) >= limit:
		// The mirror already answers the search; take TMDB's results only if
		// they are immediate.
		wait = searchRemoteGrace
	}
	// A detached search that finished answers the next one: its hits are
	// summaries in memory, never stored, so the mirror cannot.
	remote, ok := c.remoteSearches.get(remoteKey, c.now())
	if !ok {
		got, err := c.hydrateValue(ctx, searchKey, wait, fetch)
		if err != nil {
			// TMDB is slow, failing or still answering: the mirror's matches stand.
			return localAnswer()
		}
		remote, _ = got.([]core.Title)
	}
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
			if !c.inLibrary(t.Key) {
				t.SearchScore = outsideScore(t.SearchScore)
			}
			if tailKeys[t.Key] {
				// TMDB's summary of a tail hit: its display title, date,
				// poster and votes replace the export's name alone.
				if i := slices.IndexFunc(out, func(o core.Title) bool { return o.Key == t.Key }); i >= 0 {
					t.SearchScore = max(t.SearchScore, out[i].SearchScore)
					out[i] = t
				}
				continue
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
func (c *Catalog) searchLiteral(ctx context.Context, query, types string, year, n int, unfiltered, walk, deferFacts, partial bool) ([]core.Title, error) {
	if walk && year == 0 {
		if keys, ok, err := c.searchByPopularity(ctx, query, n, unfiltered); err == nil && ok {
			if deferFacts {
				return c.searchCandidateRows(ctx, keys, unfiltered, partial)
			}
			return c.searchCandidates(ctx, keys, unfiltered, partial)
		}
	}
	match, args := c.titleMatch("", query)
	adult := ""
	if unfiltered {
		adult = "adult=0 AND "
	}
	// With the rank built, movies and shows order by its popularity
	// snapshot, as the popularity walk does.
	pop := "popularity"
	if c.rankReady.Load() {
		pop = "COALESCE((SELECT -r.rk FROM catalog_rank r WHERE r.key=catalog_titles.key), popularity)"
	}
	gateWhere := ""
	var gateArgs []any
	if !unfiltered {
		if gc, ga := c.searchGateConds(ctx, "catalog_titles"); gc != "" {
			gateWhere = " AND " + gc
			gateArgs = ga
		}
	}
	q := `SELECT ` + titleCols + ` FROM catalog_titles WHERE ` + adult + `type IN (` + types + `) AND ` + match + gateWhere + ` ORDER BY CASE WHEN ?>0 AND year=? THEN 0 ELSE 1 END, ` + pop + ` DESC LIMIT ?`
	args = append(args, gateArgs...)
	args = append(args, year, year, n)
	if deferFacts {
		return c.querySearchRows(ctx, unfiltered, partial, q, args...)
	}
	if unfiltered {
		return c.queryTitlesUnfiltered(ctx, q, args...)
	}
	return c.queryTitlesRows(ctx, false, partial, q, args...)
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
	// A franchise the mirror holds is served from it; TMDB refreshes it
	// behind the read at most once a day (new parts land in the mirror for
	// the next read). A franchise page never waits on TMDB or writes on the
	// request, except for one the mirror has no parts of yet.
	var name string
	var fetchedAt int64
	_ = c.db.QueryRowContext(ctx, `SELECT name, fetched_at FROM catalog_collections WHERE tmdb_id=?`, tmdbCollection).Scan(&name, &fetchedAt)
	items, err := c.queryTitles(ctx, `SELECT `+titleCols+` FROM catalog_titles WHERE collection_id=? AND type='movie' ORDER BY released_ts ASC`, tmdbCollection)
	if err != nil {
		return "", nil, err
	}
	if len(items) > 0 {
		if c.tmdb() != nil && c.now().Sub(time.Unix(fetchedAt, 0)) > collectionRefreshAfter {
			_ = c.hydrate(ctx, fmt.Sprintf("collection-refresh:%d", tmdbCollection), 0, func(ctx context.Context) error {
				_, _, err := c.fetchCollection(ctx, tmdbCollection)
				return err
			})
		}
		if name == "" {
			name = items[0].CollectionName
		}
		return name, c.releasedTitles(ctx, items), nil
	}
	if c.tmdb() != nil {
		return c.fetchCollection(ctx, tmdbCollection)
	}
	return "", nil, core.ErrNotFound
}

// collectionRefreshAfter spaces a mirrored franchise's TMDB refreshes.
const collectionRefreshAfter = 24 * time.Hour

// fetchCollection reads a franchise from TMDB and mirrors its parts.
func (c *Catalog) fetchCollection(ctx context.Context, tmdbCollection int64) (string, []core.Title, error) {
	d, err := c.tmdb().CollectionDetails(ctx, tmdbCollection)
	if err != nil {
		if isNotFound(err) {
			return "", nil, err
		}
		return "", nil, core.ErrNotFound
	}
	c.saveCollection(ctx, d.ID, d.Name, d.Overview, d.PosterPath, d.BackdropPath)
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
		if !t.Adult && t.IsReleased(c.now()) && c.visibleFor(ctx, c.visibilityFor(ctx), &t) {
			out = append(out, t)
		}
	}
	return out
}

// searchListRows reads keys' rows with their list facts (hydrateListSlice).
func (c *Catalog) searchListRows(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
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
		if err := c.hydrateListSlice(ctx, items); err != nil {
			return nil, err
		}
		for _, t := range items {
			out[t.Key] = t
		}
	}
	return out, nil
}

// searchCandidateRows reads the same title rows as searchCandidates without
// their facts. It retains key order and the ordinary release and cache rules;
// partial rows stay rows (a cached title is not swapped in).
func (c *Catalog) searchCandidateRows(ctx context.Context, keys []core.RatingKey, unfiltered, partial bool) ([]core.Title, error) {
	if len(keys) == 0 {
		return nil, nil
	}
	args := make([]any, len(keys))
	for i, k := range keys {
		args[i] = int64(k)
	}
	q := `SELECT ` + titleCols + ` FROM catalog_titles WHERE key IN (` + placeholders(len(keys)) + `)`
	if !unfiltered {
		if gc, ga := c.searchGateConds(ctx, "catalog_titles"); gc != "" {
			q += " AND " + gc
			args = append(args, ga...)
		}
	}
	titles, err := c.querySearchRows(ctx, unfiltered, partial, q, args...)
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

func (c *Catalog) querySearchRows(ctx context.Context, unfiltered, partial bool, query string, args ...any) ([]core.Title, error) {
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
		if !unfiltered && !partial {
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

// searchCandidates reads and hydrates titles in the supplied key order;
// partial leaves movies and shows rows (queryTitlesRows).
func (c *Catalog) searchCandidates(ctx context.Context, keys []core.RatingKey, unfiltered, partial bool) ([]core.Title, error) {
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
	if !unfiltered {
		if gc, ga := c.searchGateConds(ctx, "catalog_titles"); gc != "" {
			q += " AND " + gc
			args = append(args, ga...)
		}
	}
	var titles []core.Title
	var err error
	if unfiltered {
		titles, err = c.queryTitlesUnfiltered(ctx, q, args...)
	} else {
		titles, err = c.queryTitlesRows(ctx, false, partial, q, args...)
	}
	if err != nil {
		return nil, err
	}
	sort.SliceStable(titles, func(i, j int) bool { return at[titles[i].Key] < at[titles[j].Key] })
	return titles, nil
}
