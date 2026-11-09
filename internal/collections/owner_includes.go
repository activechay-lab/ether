package collections

import (
	"context"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
)

type catalogBrowsability interface {
	ExplainBrowsability(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error)
	IncludeTitle(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error)
	RemoveInclude(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error)
}

type catalogSearchAll interface {
	SearchAll(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error)
}

type notConfiguredError struct{ msg string }

func (e notConfiguredError) Error() string       { return e.msg }
func (e notConfiguredError) NotConfigured() bool { return true }

// TitleLibraryStatus explains whether key is in the owner's browsable library
// and, if not, every active rule keeping it out.
func (s *Service) TitleLibraryStatus(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error) {
	c, ok := s.cat.(catalogBrowsability)
	if !ok {
		return catalog.BrowsabilityStatus{}, notConfiguredError{"catalogue library overrides are unavailable"}
	}
	return c.ExplainBrowsability(ctx, key)
}

// IncludeTitle promotes key into the owner's browsable library unless blocked
// by content policy, adult rules, or release playability gates.
func (s *Service) IncludeTitle(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error) {
	c, ok := s.cat.(catalogBrowsability)
	if !ok {
		return catalog.BrowsabilityStatus{}, notConfiguredError{"catalogue library overrides are unavailable"}
	}
	return c.IncludeTitle(ctx, key)
}

// RemoveInclude removes an owner library override for key.
func (s *Service) RemoveInclude(ctx context.Context, key core.RatingKey) (catalog.BrowsabilityStatus, error) {
	c, ok := s.cat.(catalogBrowsability)
	if !ok {
		return catalog.BrowsabilityStatus{}, notConfiguredError{"catalogue library overrides are unavailable"}
	}
	return c.RemoveInclude(ctx, key)
}

// SearchAllTitles searches TMDB and the local mirror without hiding titles
// excluded by catalogue visibility or release gates, so the owner console can
// inspect any non-adult title and see why it is not in the library.
func (s *Service) SearchAllTitles(ctx context.Context, q string, t core.MediaType, limit int) ([]core.Title, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var types []core.MediaType
	if t == core.TypeMovie || t == core.TypeShow {
		types = []core.MediaType{t}
	}
	if all, ok := s.cat.(catalogSearchAll); ok {
		return all.SearchAll(ctx, q, types, limit)
	}
	return s.cat.Search(ctx, q, types, limit)
}
