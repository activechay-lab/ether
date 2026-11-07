package plex

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

// AccessPolicy adds owner-defined restrictions to the mandatory title guard.
type AccessPolicy = experience.AccessPolicy

func (s *Server) saferFetchCount(u *core.User, want int) int {
	if want <= 0 || !s.needPlexRatingFilter(u) {
		return want
	}
	n := want * 2
	if extra := want + 24; n > extra {
		n = extra
	}
	if n > 200 {
		n = 200
	}
	return n
}

func (s *Server) takeVisible(u *core.User, ts []core.Title, want int) []core.Title {
	out := s.visibleTitles(u, ts)
	if want > 0 && len(out) > want {
		return out[:want]
	}
	return out
}

func (s *Server) catalogBrowse(ctx context.Context, q core.BrowseQuery) (core.Page, error) {
	if s.plexMetaOn() {
		q.Filter.SaferPlexRating = true
	}
	return s.d.Catalog.Browse(ctx, q)
}

func (s *Server) visibleTitles(u *core.User, ts []core.Title) []core.Title {
	out := s.visibility().TitlesAllowed(u, ts)
	if !s.needPlexRatingFilter(u) || s.d.PlexMeta == nil {
		return out
	}
	keys := make([]core.RatingKey, 0, len(out))
	for i := range out {
		keys = append(keys, out[i].Key)
	}
	rows := s.d.PlexMeta.CachedMany(context.Background(), keys)
	var guids map[core.RatingKey]string
	if s.d.PlexGUID != nil {
		guids = s.d.PlexGUID.CachedMany(context.Background(), keys)
	}
	kept := out[:0]
	for i := range out {
		if s.plexMetaRatingAllowedCached(u, &out[i], rows, guids) {
			kept = append(kept, out[i])
		}
	}
	return kept
}

func (s *Server) titleAllowed(u *core.User, t *core.Title) bool {
	return experience.TitleAllowed(s.d.AccessPolicy, u, t) && s.plexMetaRatingAllowed(u, t)
}

func (s *Server) visibility() experience.Visibility {
	v := experience.Visibility{Policy: s.d.AccessPolicy, FilterAvailable: s.d.FilterAvailable,
		SectionFor: func(t core.MediaType) (int, bool) { sec, ok := s.untitledSectionFor(t); return sec.ID, ok }}
	if s.d.Catalog != nil {
		v.GetTitle = s.artworkTitle
	}
	if s.d.Playback != nil {
		v.Sessions = s.d.Playback.Sessions
	}
	return v
}

// openableTitles is the common row/detail admission decision. A pinned shelf
// retains its availability cut, but never bypasses current authorization.
func (s *Server) openableTitles(ctx context.Context, u *core.User, ts []core.Title, pinned bool) []core.Title {
	ts = s.visibleTitles(u, ts)
	s.preloadSeries(ctx, ts)
	if !pinned {
		ts = s.availableForViewer(ctx, u, ts)
	}
	out := make([]core.Title, 0, len(ts))
	for _, t := range ts {
		if s.admittedTitle(&t) && s.playableTitle(ctx, u, &t) {
			out = append(out, t)
		}
	}
	return out
}

func (s *Server) visibleTitle(ctx context.Context, u *core.User, t *core.Title) bool {
	if t == nil {
		return false
	}
	if !s.admittedTitle(t) {
		return false
	}
	if _, pinned := shelfMember(ctx, t.Key); pinned {
		return s.titleAllowed(u, t)
	}
	return s.visibility().Visible(ctx, u, t) && s.plexMetaRatingAllowed(u, t)
}

// admittedTitle shares a cheap predicate over facts the source already loaded.
func (s *Server) admittedTitle(t *core.Title) bool {
	if t == nil || t.IsFutureRelease(s.now()) {
		return false
	}
	if catalog, ok := s.d.Catalog.(interface{ TitleOpenable(*core.Title) bool }); ok {
		return catalog.TitleOpenable(t)
	}
	return !t.Adult
}

func (s *Server) playableTitle(ctx context.Context, u *core.User, t *core.Title) bool {
	return s.visibility().Playable(ctx, u, t) && s.plexMetaRatingAllowed(u, t)
}
func (s *Server) availableForViewer(ctx context.Context, u *core.User, ts []core.Title) []core.Title {
	return s.visibility().Available(ctx, u, ts)
}

func (s *Server) viewerRestricted(u *core.User) bool {
	return experience.ViewerRestricted(s.d.AccessPolicy, u)
}

// preloadSeries reads the series of a list's episodes and seasons in one
// batch: playableTitle checks each item's series, which otherwise loads them
// one by one (On Deck: hundreds of shows).
func (s *Server) preloadSeries(ctx context.Context, ts []core.Title) {
	pre, ok := s.d.Catalog.(interface {
		PreloadStored(context.Context, []core.RatingKey)
	})
	if !ok {
		return
	}
	var shows []core.RatingKey
	for i := range ts {
		if ts[i].ShowKey != 0 {
			shows = append(shows, ts[i].ShowKey)
		}
	}
	if len(shows) > 1 {
		pre.PreloadStored(ctx, shows)
	}
}
