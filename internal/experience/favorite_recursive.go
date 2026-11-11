package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// favoriteRecursivePage answers a recursive TV listing filtered to the
// viewer's favorites ("IsFavorite", as Jellyfin for Roku's Favorites row asks)
// from the favorites themselves. The library walk would otherwise expand every
// show into its seasons and episodes to find the handful the viewer starred,
// which reads every season of the library inside one request (#421). The set
// is the same: a favorite show, season or episode in this library that the
// viewer may see and that matches the query. ok is false when this path does
// not apply and the caller must list the library.
func (s *Service) favoriteRecursivePage(ctx context.Context, v *core.User, library *Library, q frontend.ItemQuery) (core.Page, bool, error) {
	if library == nil || library.Type != core.TypeShow || !q.Recursive || q.Favorite == nil || !*q.Favorite ||
		q.AdjacentTo != 0 || q.StartAt != 0 || s.d.FavoriteTitleKeys == nil || s.d.CachedGetMany == nil {
		return core.Page{}, false, nil
	}
	keys, all, err := s.d.FavoriteTitleKeys(ctx, v, maxIndexedExclusions)
	if err != nil || !all {
		return core.Page{}, false, err
	}
	want := func(typ core.MediaType) bool { return len(q.Types) == 0 || containsMediaType(q.Types, typ) }
	var tree []core.RatingKey
	for _, k := range keys {
		switch k.Kind() {
		case core.KindShow:
			if want(core.TypeShow) {
				tree = append(tree, k)
			}
		case core.KindSeason:
			if want(core.TypeSeason) {
				tree = append(tree, k)
			}
		case core.KindEpisode:
			if want(core.TypeEpisode) {
				tree = append(tree, k)
			}
		}
	}
	permitted := []core.Title{}
	if len(tree) > 0 {
		titles, err := s.d.CachedGetMany(ctx, tree)
		if err != nil {
			return core.Page{}, false, err
		}
		query := q
		query.Favorite = nil
		for _, k := range tree {
			title, found := titles[k]
			if !found || !s.visible(ctx, v, &title) {
				continue
			}
			ok, err := s.matchesUserQuery(ctx, v, title, query)
			if err != nil {
				return core.Page{}, false, err
			}
			if ok {
				permitted = append(permitted, title)
			}
		}
		if err := s.sortTitles(ctx, permitted, q); err != nil {
			return core.Page{}, false, err
		}
	}
	total := len(permitted)
	start := min(max(q.Offset, 0), total)
	end := total
	if q.Limit > 0 {
		end = min(start+q.Limit, total)
	}
	return core.Page{Items: permitted[start:end], Total: total}, true, nil
}
