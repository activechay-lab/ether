package experience

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

const maxPersonFacetIDs = 256

// PersonFacetIDs maps requested credits on one currently visible title to the
// same identifiers used by library person facets. An episode-only credit may
// have no corresponding library-wide facet and therefore may not resolve via
// ResolveFacet for that library.
func (s *Service) PersonFacetIDs(ctx context.Context, viewer *core.Viewer, _ core.Device, key core.RatingKey, ids []int64) (map[int64]string, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	if len(ids) > maxPersonFacetIDs {
		return nil, errors.New("experience: too many person IDs")
	}
	wanted := make(map[int64]bool, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, errors.New("experience: invalid person ID")
		}
		wanted[id] = true
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, err
	}
	title, err := s.item(ctx, v, key)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]string)
	seen := make(map[string]int64)
	for _, group := range [][]core.Person{title.Cast, title.Directors, title.Writers, title.Crew} {
		for _, p := range group {
			if p.TMDB <= 0 || p.Name == "" {
				continue
			}
			id := personFacetID(p.TMDB)
			if old, ok := seen[id]; ok && old != p.TMDB {
				return nil, errors.New("experience: facet identifier collision")
			}
			seen[id] = p.TMDB
			if wanted[p.TMDB] {
				result[p.TMDB] = id
			}
		}
	}
	return result, nil
}

// resolvePersonFacet resolves a person facet id whose TMDB id is known, as
// genres and studios resolve: against the library-wide credits of the
// libraries the viewer may open, leaving which titles the viewer may see
// under the person to the filtered page. One statement pair reads the
// person's most popular credit and the name on it, however many titles
// credit them; ok is false when the credit index is unavailable.
func (s *Service) resolvePersonFacet(ctx context.Context, viewer *core.Viewer, viewID, id string, tmdb int64) (core.FacetValue, bool, error) {
	if s.d.PersonCreditsMany == nil {
		return core.FacetValue{}, false, nil
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return core.FacetValue{}, true, err
	}
	var types []core.MediaType
	for _, lib := range s.libraries() {
		if (viewID != "" && strconv.Itoa(lib.ID) != viewID) || !SectionAllowed(v, lib.ID) {
			continue
		}
		switch lib.Type {
		case core.TypeMovie:
			types = append(types, core.TypeMovie)
		case core.TypeShow:
			types = append(types, core.TypeShow, core.TypeSeason, core.TypeEpisode)
		}
	}
	if len(types) == 0 {
		return core.FacetValue{}, true, core.ErrNotFound
	}
	credits, err := s.d.PersonCreditsMany(core.WithoutCatalogViewer(ctx), []int64{tmdb}, types, 1)
	if err != nil {
		return core.FacetValue{}, true, err
	}
	for _, credit := range credits[tmdb] {
		if credit.Person.TMDB == tmdb && strings.TrimSpace(credit.Person.Name) != "" {
			return core.FacetValue{ID: id, Key: strconv.FormatInt(tmdb, 10), Title: credit.Person.Name}, true, nil
		}
	}
	return core.FacetValue{}, true, core.ErrNotFound
}
