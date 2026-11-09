package experience

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"strconv"
)

// ExportLibraryPage uses the regular catalogue and discovery policy. It does
// not resolve a source, issue a session, or materialize a library in memory.
func (s *Service) ExportLibraryPage(ctx context.Context, viewer *core.Viewer, device core.Device, viewID string, after core.RatingKey, limit int) (frontend.LibraryExportPage, error) {
	ctx = core.WithCatalogViewer(ctx, viewer)
	v, err := s.viewer(ctx, viewer)
	if err != nil {
		return frontend.LibraryExportPage{}, err
	}
	id, err := strconv.Atoi(viewID)
	if err != nil || !SectionAllowed(v, id) || s.d.Browse == nil {
		return frontend.LibraryExportPage{}, core.ErrNotFound
	}
	var library *Library
	libs := s.libraries()
	for j := range libs {
		if libs[j].ID == id {
			library = &libs[j]
			break
		}
	}
	if library == nil {
		return frontend.LibraryExportPage{}, core.ErrNotFound
	}
	limit = min(max(limit, 1), 250)
	filter := core.Filter{Type: library.Type, MaxContentRating: v.MaxContentRating}
	if s.d.PageExclusions != nil {
		ex := s.d.PageExclusions(ctx, v, library.Type)
		if ex.Err != nil {
			return frontend.LibraryExportPage{}, ex.Err
		}
		if folded, fits, none := viewerBrowseFilter(filter, v, ex, library.Type); fits {
			filter = folded
			if none {
				return frontend.LibraryExportPage{Done: true}, nil
			}
		}
	}
	// An export row shows list facts (genres, directors, three cast) and
	// carries what this viewer's policy reads; a title availability judges
	// on its facts comes back in full. Detail stays with one-title reads.
	q := core.BrowseQuery{Filter: filter, Sort: core.SortKey, AfterKey: after, Limit: limit, NoTotal: true}
	if detail, ok := s.d.Visibility.judgedOnFacts(ctx); ok {
		q.ListRows = &core.ListRows{Facts: s.d.Visibility.TitleFacts(v) | core.FactGenres, Detail: detail}
	}
	page, err := s.d.Browse(ctx, q)
	if err != nil {
		return frontend.LibraryExportPage{}, err
	}
	result := frontend.LibraryExportPage{Done: len(page.Items) < limit, Next: after, Items: []core.Title{}}
	if len(page.Items) > 0 {
		result.Next = page.Items[len(page.Items)-1].Key
	}
	candidates := s.d.Visibility.Available(ctx, v, page.Items)
	for _, t := range candidates {
		if s.d.Visibility.TitleAllowed(v, &t) && s.d.Visibility.Playable(ctx, v, &t) {
			result.Items = append(result.Items, t)
		}
	}
	return result, nil
}
