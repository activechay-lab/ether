package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// FavoriteEpisodeDeps answer a recursive, episodes-only favorite listing from
// the viewer's favorite rows instead of walking every series. Both are
// required for that path; without them the listing reads the favorite keys in
// process, which still bounds it by the favorite set.
type FavoriteEpisodeDeps struct {
	// Page reads the viewer's favorite episodes, in episode SortName order
	// (series sort title, then season, then episode), whose series and
	// episode pass the browse filter's SQL predicate, starting at offset.
	// showKey, when nonzero, restricts to one series.
	Page func(ctx context.Context, filter core.Filter, userID int64, showKey core.RatingKey, offset, limit int) ([]core.Title, error)
	// Count counts Page's whole set.
	Count func(ctx context.Context, filter core.Filter, userID int64, showKey core.RatingKey) (int, error)
}

func (d FavoriteEpisodeDeps) complete() bool {
	return d.Page != nil && d.Count != nil
}

// favoriteEpisodePage answers a recursive, episodes-only listing filtered to
// the viewer's favorites (Jellyfin/Emby "Filters=IsFavorite",
// "IncludeItemTypes=Episode", "SortBy=SortName") from the viewer's own favorite
// rows, never the library. The scan path expands every series of the library
// into its seasons and episodes to find the handful the viewer starred, which
// read the whole TV library on Moonfin's Favorites screen (#405). Here the SQL
// carries the viewer's exclusions and the favorite predicate, so the count and
// the page follow the viewer's favorites. Ordering is the requested SortName; a
// client asking another order keeps today's path. ok is false when the request
// is not this shape.
func (s *Service) favoriteEpisodePage(ctx context.Context, v *core.User, library *Library, showKey core.RatingKey, q frontend.ItemQuery) (core.Page, bool, error) {
	d := s.d.FavoriteEpisodes
	if !d.complete() || library == nil || library.Type != core.TypeShow || !q.Recursive ||
		len(q.Types) != 1 || q.Types[0] != core.TypeEpisode ||
		q.Favorite == nil || !*q.Favorite || q.Watched != nil || q.Season != nil ||
		q.AdjacentTo != 0 || q.StartAt != 0 || q.Limit <= 0 {
		return core.Page{}, false, nil
	}
	// Only the one order this path serves correctly: SortName (SortTitle).
	// Any other order keeps today's path, which never becomes a favorite scan
	// unless the favorites are already in hand.
	if q.Sort != core.SortTitle || q.Desc {
		return core.Page{}, false, nil
	}
	// A viewer's own episode-group or hidden-specials setting changes which
	// episodes exist, which the favorite rows cannot see; keep the walk then.
	if s.d.StoredEpisodes.complete() {
		if custom, err := s.d.StoredEpisodes.Customized(ctx, v); err != nil || custom {
			return core.Page{}, false, err
		}
	}
	show := core.Filter{Type: core.TypeShow, MaxContentRating: v.MaxContentRating}
	if s.d.PageExclusions != nil {
		ex := s.d.PageExclusions(ctx, v, core.TypeEpisode)
		if ex.Err != nil {
			return core.Page{}, false, ex.Err
		}
		var fits, none bool
		show, fits, none = viewerBrowseFilter(show, v, ex, core.TypeShow)
		if !fits {
			return core.Page{}, false, nil
		}
		if none {
			return core.Page{Items: []core.Title{}}, true, nil
		}
	}
	if ViewerRestricted(s.d.Visibility.Policy, v) {
		if s.d.PageExclusions == nil {
			return core.Page{}, false, nil
		}
		if id, ok := s.d.Visibility.SectionFor(core.TypeEpisode); !ok || !SectionAllowed(v, id) {
			return core.Page{Items: []core.Title{}}, true, nil
		}
	}
	if s.d.ParentTitles == nil {
		return core.Page{}, false, nil
	}
	total, err := d.Count(ctx, show, v.ID, showKey)
	if err != nil {
		return core.Page{}, false, err
	}
	offset := max(q.Offset, 0)
	if total == 0 || offset >= total {
		return core.Page{Items: []core.Title{}, Total: total}, true, nil
	}
	// The SQL already carries the viewer's exclusions, so the in-process
	// check drops little; the page reads forward in bounded batches until it
	// holds offset+limit kept episodes, or the favorite set is exhausted,
	// instead of walking the library. The reads are bounded.
	want := offset + q.Limit
	kept := make([]core.Title, 0, want)
	visq := q
	visq.Favorite = nil
	for reads, at := 0, 0; len(kept) < want && reads < maxFavoriteEpisodeBatches; reads++ {
		batch, err := d.Page(ctx, show, v.ID, showKey, at, favoriteEpisodeBatch)
		if err != nil {
			return core.Page{}, false, err
		}
		if len(batch) == 0 {
			break
		}
		at += len(batch)
		visible, err := s.visibleEpisodes(ctx, v, batch, visq, show.HiddenShowSeq != 0)
		if err != nil {
			return core.Page{}, false, err
		}
		kept = append(kept, visible...)
		if len(batch) < favoriteEpisodeBatch {
			break
		}
	}
	start := min(offset, len(kept))
	end := min(offset+q.Limit, len(kept))
	return core.Page{Items: kept[start:end], Total: total}, true, nil
}

// favoriteEpisodeBatch is the favorite episodes read per step.
const favoriteEpisodeBatch = 500

// maxFavoriteEpisodeBatches bounds the reads behind one page. The SQL carries
// the viewer's exclusions, so the in-process check drops little.
const maxFavoriteEpisodeBatches = 8
