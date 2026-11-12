package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// PlayedEpisodeDeps answer a recursive, episodes-only played listing from the
// viewer's watch rows instead of walking every series. Both are required for
// that path; without them the listing walks the library.
type PlayedEpisodeDeps struct {
	// Page reads the viewer's played episodes, most recently played first
	// (desc) or least recently first, whose series and episode pass the
	// browse filter's SQL predicate, starting at offset. showKey, when
	// nonzero, restricts to one series.
	Page func(ctx context.Context, filter core.Filter, userID int64, showKey core.RatingKey, desc bool, offset, limit int) ([]core.Title, error)
	// Count counts Page's whole set.
	Count func(ctx context.Context, filter core.Filter, userID int64, showKey core.RatingKey) (int, error)
}

func (d PlayedEpisodeDeps) complete() bool {
	return d.Page != nil && d.Count != nil
}

// playedEpisodePage answers a recursive, episodes-only listing filtered to the
// viewer's played items (Jellyfin/Emby "Filters=IsPlayed", "SortBy=DatePlayed")
// from the viewer's own watch rows, never the library. The scan path expands
// every series of the library into its seasons and episodes to find the
// handful of episodes the viewer has played (#405), which read the whole TV
// library on the home screen's every launch. Here the SQL carries the viewer's
// exclusions and the played predicate, so the count and the page follow the
// viewer's history. Ordering is last-played; a client-specified "DatePlayed"
// picks the direction. ok is false when the request is not this shape.
func (s *Service) playedEpisodePage(ctx context.Context, v *core.User, library *Library, showKey core.RatingKey, q frontend.ItemQuery) (core.Page, bool, error) {
	d := s.d.PlayedEpisodes
	if !d.complete() || library == nil || library.Type != core.TypeShow || !q.Recursive ||
		len(q.Types) != 1 || q.Types[0] != core.TypeEpisode ||
		q.Watched == nil || !*q.Watched || q.Favorite != nil || q.Season != nil ||
		q.AdjacentTo != 0 || q.StartAt != 0 || q.Limit <= 0 {
		return core.Page{}, false, nil
	}
	// Only the order this path serves correctly: the viewer's last-played
	// time (Jellyfin/Emby "DatePlayed"). Any other order keeps today's path,
	// which never becomes a last-played scan.
	if q.Sort != core.SortPlayed {
		return core.Page{}, false, nil
	}
	// A viewer's own episode-group or hidden-specials setting changes which
	// episodes exist, which the played rows cannot see; keep the walk then.
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
	// holds offset+limit kept episodes, or the played set is exhausted,
	// instead of walking the library. The reads are bounded.
	// The page runs in the requested direction: DatePlayed descends from the
	// most recent play, ascending from the least recent.
	desc := q.Desc
	want := offset + q.Limit
	kept := make([]core.Title, 0, want)
	// The played set already comes from the viewer's watch state, so the
	// per-item watched re-check here would read state again for nothing.
	visq := q
	visq.Watched = nil
	for reads, at := 0, 0; len(kept) < want && reads < maxPlayedEpisodeBatches; reads++ {
		batch, err := d.Page(ctx, show, v.ID, showKey, desc, at, playedEpisodeBatch)
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
		if len(batch) < playedEpisodeBatch {
			break
		}
	}
	start := min(offset, len(kept))
	end := min(offset+q.Limit, len(kept))
	return core.Page{Items: kept[start:end], Total: total}, true, nil
}

// playedEpisodeBatch is the played episodes read per step.
const playedEpisodeBatch = 500

// maxPlayedEpisodeBatches bounds the reads behind one page. The SQL carries
// the viewer's exclusions, so the in-process check drops little.
const maxPlayedEpisodeBatches = 8
