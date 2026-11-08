package experience

import (
	"context"
	"errors"
	"sort"
	"strconv"

	"github.com/braedonsaunders/ether/internal/core"
)

// sqlFacets maps the facets the catalogue counts in SQL to its facet name
// (studios and countries unlimited: the walk listed every value). People are
// not counted in SQL: a credit facet would read every credit.
var sqlFacets = map[string]string{"genre": "genre", "studio": "studioAll", "country": "countryAll", "year": "year", "decade": "decade", "contentRating": "contentRating"}

// indexedFacets counts a library's facet values in SQL over the titles the
// viewer may see (the same exclusions an indexed page folds in), instead of
// loading the whole library. An empty viewID is every library the viewer
// may open. ok is false when the walk must answer: no SQL facet, or
// exclusions too large for one predicate.
func (s *Service) indexedFacets(ctx context.Context, viewer *core.Viewer, viewID, name string) ([]core.FacetValue, bool, error) {
	if s.d.FacetFilter == nil || s.d.PageExclusions == nil || sqlFacets[name] == "" {
		return nil, false, nil
	}
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return nil, false, err
	}
	var libs []Library
	for _, lib := range s.libraries() {
		if (viewID == "" || strconv.Itoa(lib.ID) == viewID) && SectionAllowed(v, lib.ID) {
			libs = append(libs, lib)
		}
	}
	if viewID != "" && len(libs) == 0 {
		return nil, false, core.ErrNotFound
	}
	counts := map[string]int{}
	for _, lib := range libs {
		ex := s.d.PageExclusions(ctx, v, lib.Type)
		if ex.Err != nil {
			return nil, false, ex.Err
		}
		if !ex.AvailabilityInSQL {
			return nil, false, nil
		}
		filter, fits, none := viewerBrowseFilter(core.Filter{Type: lib.Type}, v, ex, lib.Type)
		if !fits {
			return nil, false, nil
		}
		if none {
			continue
		}
		vals, err := s.d.FacetFilter(ctx, filter, sqlFacets[name])
		if err != nil {
			return nil, false, err
		}
		for _, val := range vals {
			if val.Key != "" {
				counts[val.Key] += val.Count
			}
		}
	}
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]core.FacetValue, 0, len(keys))
	seen := make(map[string]string, len(keys))
	for _, k := range keys {
		id := facetID(name, k)
		if old, ok := seen[id]; ok && old != k {
			return nil, false, errors.New("experience: facet identifier collision")
		}
		seen[id] = k
		out = append(out, core.FacetValue{ID: id, Key: k, Title: k, Count: counts[k]})
	}
	return out, true, nil
}
