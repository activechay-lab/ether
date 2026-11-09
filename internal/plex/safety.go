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
	v := experience.Visibility{Policy: s.d.AccessPolicy, FilterAvailable: s.d.FilterAvailable, JudgedOnFacts: s.d.JudgedOnFacts,
		SectionFor: func(t core.MediaType) (int, bool) { sec, ok := s.untitledSectionFor(t); return sec.ID, ok }}
	if s.d.Catalog != nil {
		v.GetTitle = s.seriesTitle
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
	ctx = s.withSeriesRows(ctx, u, ts)
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
	return s.visibleTitleWith(ctx, s.visibility(), u, t)
}

// visibleTitleWith is visibleTitle through vis (s.visibility() with a
// caller's own show lookup).
func (s *Server) visibleTitleWith(ctx context.Context, vis experience.Visibility, u *core.User, t *core.Title) bool {
	if t == nil {
		return false
	}
	if !s.admittedTitle(t) {
		return false
	}
	if _, pinned := shelfMember(ctx, t.Key); pinned {
		return s.titleAllowed(u, t)
	}
	return vis.Visible(ctx, u, t) && s.plexMetaRatingAllowed(u, t)
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

type seriesRowsKey struct{}

// withSeriesRows reads the series of a list's episodes and seasons in one
// batch, for the Playable checks of the list and of its rendering (which
// otherwise load them one by one; On Deck: hundreds of shows). They are read
// from the mirror as visibility rows, with the facts u's policy reads, not
// as full titles, and seriesTitle answers from them: a list never waits on
// a provider refresh of a show. Plex's own detail cache reads the shows it
// renders in full separately (titleCache). A catalogue without visibility
// rows preloads the series into its title cache instead.
func (s *Server) withSeriesRows(ctx context.Context, u *core.User, ts []core.Title) context.Context {
	have, _ := ctx.Value(seriesRowsKey{}).(map[core.RatingKey]core.Title)
	var shows []core.RatingKey
	seen := map[core.RatingKey]bool{}
	for i := range ts {
		if k := ts[i].ShowKey; k != 0 && !seen[k] {
			seen[k] = true
			if _, ok := have[k]; !ok {
				shows = append(shows, k)
			}
		}
	}
	if len(shows) == 0 {
		return ctx
	}
	if rows, ok := s.d.Catalog.(interface {
		CachedVisibilityRows(context.Context, []core.RatingKey, core.TitleFacts) (map[core.RatingKey]core.Title, error)
	}); ok {
		parents, err := rows.CachedVisibilityRows(ctx, shows, s.visibility().TitleFacts(u))
		if err == nil {
			merged := make(map[core.RatingKey]core.Title, len(have)+len(parents))
			for k, t := range have {
				merged[k] = t
			}
			for k, t := range parents {
				merged[k] = t
			}
			return context.WithValue(ctx, seriesRowsKey{}, merged)
		}
	}
	if pre, ok := s.d.Catalog.(interface {
		PreloadStored(context.Context, []core.RatingKey)
	}); ok && len(shows) > 1 {
		pre.PreloadStored(ctx, shows)
	}
	return ctx
}

// seriesTitle is the series lookup of s.visibility() (Playable): a series
// withSeriesRows read for this request, else artworkTitle.
func (s *Server) seriesTitle(ctx context.Context, key core.RatingKey) (*core.Title, error) {
	if rows, _ := ctx.Value(seriesRowsKey{}).(map[core.RatingKey]core.Title); rows != nil {
		if t, ok := rows[key]; ok {
			return &t, nil
		}
	}
	return s.artworkTitle(ctx, key)
}
