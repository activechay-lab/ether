package experience

import (
	"cmp"
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

const maxIndexedExclusions = 2000

// indexedLibraryPage asks the catalogue for the requested offset and limit.
// It is used only when the SQL predicate already includes this viewer's
// exclusions. A page that still fails the in-process checks is refused so the
// caller scans, which is the path for filters SQL cannot see.
func (s *Service) indexedLibraryPage(ctx context.Context, v *core.User, library *Library, q frontend.ItemQuery) (core.Page, bool, error) {
	if s == nil || s.d.PageExclusions == nil || s.d.Browse == nil || library == nil || v == nil {
		return core.Page{}, false, nil
	}
	if !indexedBrowseQuery(library, q) {
		return core.Page{}, false, nil
	}
	filter := q.Filter
	filter.Type = library.Type
	ex := s.d.PageExclusions(ctx, v, library.Type)
	if ex.Err != nil {
		return core.Page{}, false, ex.Err
	}
	filter, fits, none := viewerBrowseFilter(filter, v, ex, library.Type)
	if !fits {
		return core.Page{}, false, nil
	}
	if none {
		return core.Page{Items: []core.Title{}}, true, nil
	}
	filter, fits, none, err := s.userStateFilter(ctx, v, filter, q)
	if err != nil || !fits {
		return core.Page{}, false, err
	} else if none {
		return core.Page{Items: []core.Title{}}, true, nil
	}
	page, err := s.d.Browse(ctx, core.BrowseQuery{Filter: filter, Sort: q.Sort, Desc: q.Desc, Offset: max(q.Offset, 0), Limit: q.Limit, NoTotal: q.NoTotal})
	if err != nil {
		return core.Page{}, false, err
	}
	if !s.indexedPageHolds(ctx, v, page.Items, q, ex.AvailabilityInSQL) {
		return core.Page{}, false, nil
	}
	return page, true, nil
}

// userStateFilter folds a played or favorite filter into the SQL filter:
// played is a watch-state predicate, favorites the viewer's favorite keys
// (included, or excluded for "not a favorite"). fits is false when the
// favorites are too many for one predicate.
func (s *Service) userStateFilter(ctx context.Context, v *core.User, filter core.Filter, q frontend.ItemQuery) (out core.Filter, fits, none bool, err error) {
	if q.Watched != nil {
		state := core.FilterGroup{UserID: v.ID, Unwatched: boolPtr(!*q.Watched)}
		if filter.Group == nil {
			filter.Group = &state
		} else {
			filter.Group = &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{*filter.Group, state}}
		}
	}
	if q.Favorite != nil {
		if s.d.FavoriteTitleKeys == nil || filter.IncludeKeys != nil {
			return filter, false, false, nil
		}
		keys, all, err := s.d.FavoriteTitleKeys(ctx, v, maxIndexedExclusions)
		if err != nil || !all || len(filter.ExcludeKeys)+len(keys) > maxIndexedExclusions {
			return filter, false, false, err
		}
		if *q.Favorite {
			if len(keys) == 0 {
				return filter, true, true, nil
			}
			filter.IncludeKeys = keys
		} else {
			filter.ExcludeKeys = append(filter.ExcludeKeys, keys...)
		}
	}
	return filter, true, false, nil
}

func boolPtr(b bool) *bool { return &b }

// viewerBrowseFilter folds what the viewer may not see into filter for a
// library of typ, so SQL pages and totals match the in-process checks:
// hidden keys, blocked genres and keywords, the tightest rating cap and the
// plex.tv share predicate. fits is false when the exclusions are too large
// for one predicate; none reports that no title can match.
func viewerBrowseFilter(filter core.Filter, v *core.User, ex LibraryPageExclusions, typ core.MediaType) (out core.Filter, fits, none bool) {
	if len(ex.Keys) > maxIndexedExclusions || len(ex.ExcludeGenres) > 64 || len(ex.ExcludeKeywords) > 64 {
		return filter, false, false
	}
	filter.ExcludeKeys = append(append([]core.RatingKey{}, filter.ExcludeKeys...), ex.Keys...)
	filter.HiddenMovieGeneration = ex.Availability.HiddenMovieGeneration
	filter.HiddenShowGeneration = ex.Availability.HiddenShowGeneration
	filter.HiddenEpisodeGeneration = ex.Availability.HiddenEpisodeGeneration
	filter.DigitalDiscovery = ex.Availability.DigitalDiscovery
	filter.DigitalDateEvidence = ex.Availability.DigitalDateEvidence
	filter.AvailabilityKeepKeys = append(filter.AvailabilityKeepKeys, ex.Availability.AvailabilityKeepKeys...)
	filter.ExcludeGenres = append(append([]string{}, ex.ExcludeGenres...), filter.ExcludeGenres...)
	filter.ExcludeKeywords = append(append([]string{}, ex.ExcludeKeywords...), filter.ExcludeKeywords...)
	if v != nil {
		filter.CatalogViewerID = v.ID
		filter.MaxContentRating = tightenRating(filter.MaxContentRating, v.MaxContentRating)
	}
	share := ex.Movie
	if typ == core.TypeShow || typ == core.TypeSeason || typ == core.TypeEpisode {
		share = ex.Television
	}
	filter, none = MergeShareFilter(filter, share)
	return filter, true, none
}

// MergeShareFilter narrows filter by a plex.tv share predicate. Allow lists
// intersect, so a client's own contentRating filter can only narrow a share;
// deny lists and collection names add; rating bounds tighten. none reports
// that the two allow lists have no rating in common.
func MergeShareFilter(filter, share core.Filter) (out core.Filter, none bool) {
	filter.ContentRatings, none = intersectFold(filter.ContentRatings, share.ContentRatings)
	filter.ExcludeContentRatings = append(append([]string{}, share.ExcludeContentRatings...), filter.ExcludeContentRatings...)
	filter.MinContentRating = cmp.Or(filter.MinContentRating, share.MinContentRating)
	filter.MaxContentRating = tightenRating(filter.MaxContentRating, share.MaxContentRating)
	filter.IncludeCollectionNames = append(append([]string{}, share.IncludeCollectionNames...), filter.IncludeCollectionNames...)
	filter.ExcludeCollectionNames = append(append([]string{}, share.ExcludeCollectionNames...), filter.ExcludeCollectionNames...)
	return filter, none
}

// intersectFold is the case-insensitive intersection of two allow lists,
// where an empty list allows everything. none reports an empty intersection
// of two non-empty lists.
func intersectFold(a, b []string) ([]string, bool) {
	switch {
	case len(b) == 0:
		return a, false
	case len(a) == 0:
		return append([]string(nil), b...), false
	}
	var out []string
	for _, x := range a {
		for _, y := range b {
			if strings.EqualFold(strings.TrimSpace(x), strings.TrimSpace(y)) {
				out = append(out, x)
				break
			}
		}
	}
	return out, len(out) == 0
}

func tightenRating(current, extra string) string {
	if extra != "" && (current == "" || int(contentrating.LevelOf(extra)) < int(contentrating.LevelOf(current))) {
		return extra
	}
	return current
}

func (s *Service) indexedPageHolds(ctx context.Context, v *core.User, items []core.Title, q frontend.ItemQuery, availabilityInSQL bool) bool {
	if len(items) == 0 {
		return true
	}
	if !s.pageStateHolds(ctx, v, items, q) {
		return false
	}
	// The played and favorite checks ran for the whole page above.
	q.Watched, q.Favorite = nil, nil
	kept := items
	if !availabilityInSQL {
		kept = s.d.Visibility.Available(ctx, v, items)
	}
	have := make(map[core.RatingKey]bool, len(kept))
	for _, title := range kept {
		have[title.Key] = true
	}
	for i := range items {
		title := items[i]
		if !have[title.Key] || !s.d.Visibility.TitleAllowed(v, &title) || !s.d.Visibility.Playable(ctx, v, &title) {
			return false
		}
		ok, err := s.matchesUserQuery(ctx, v, title, q)
		if err != nil || !ok {
			return false
		}
	}
	return true
}

// pageStateHolds checks a page's played and favorite filters in one read
// each (the watch state includes writes not yet stored, so a page the SQL
// predicate admitted can still fail and fall back to the scan).
func (s *Service) pageStateHolds(ctx context.Context, v *core.User, items []core.Title, q frontend.ItemQuery) bool {
	keys := make([]core.RatingKey, len(items))
	for i := range items {
		keys[i] = items[i].Key
	}
	if q.Watched != nil {
		watched, err := s.d.State.GetMany(ctx, v.ID, keys)
		if err != nil {
			return false
		}
		for _, k := range keys {
			if (watched[k].ViewCount > 0) != *q.Watched {
				return false
			}
		}
	}
	if q.Favorite != nil {
		if s.d.IsFavoriteMany == nil {
			return false
		}
		favorites, err := s.d.IsFavoriteMany(ctx, v, keys)
		if err != nil {
			return false
		}
		for _, k := range keys {
			if favorites[k] != *q.Favorite {
				return false
			}
		}
	}
	return true
}

// indexedBrowseQuery is a single catalogue page. A recursive movie library is
// still that page: movies have no children. A recursive show library is not,
// once the request can include seasons or episodes.
func indexedBrowseQuery(library *Library, q frontend.ItemQuery) bool {
	if library == nil || q.Season != nil || q.AdjacentTo != 0 || q.StartAt != 0 || q.Limit <= 0 {
		return false
	}
	// A movie's played state is its own watch row. A series' is derived
	// from its episodes, which the predicate cannot see.
	if (q.Watched != nil || q.Favorite != nil) && library.Type != core.TypeMovie {
		return false
	}
	for _, typ := range q.Types {
		if typ != library.Type {
			return false
		}
	}
	if library.Type == core.TypeShow && q.Recursive && (len(q.Types) == 0 || containsMediaType(q.Types, core.TypeSeason) || containsMediaType(q.Types, core.TypeEpisode)) {
		return false
	}
	return true
}
