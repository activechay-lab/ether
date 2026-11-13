package app

import (
	"context"

	"github.com/braedonsaunders/ether/internal/availability"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

// Keep optional catalogue contracts by embedding the concrete catalogue. The
// admin mirror and feed matcher still use the unfiltered catalogue.
type availabilityCatalog struct {
	*catalog.Catalog
	availability *availability.Service
	// stepAside is set on the Plex surface only: its section pages read this
	// catalogue directly, so held titles are excluded here in SQL.
	stepAside *experience.StepAside
}

func (c *availabilityCatalog) Browse(ctx context.Context, q core.BrowseQuery) (core.Page, error) {
	var err error
	if q.PlaylistSource != nil {
		source := *q.PlaylistSource
		source.Filter, err = c.availability.PagingFilter(ctx, source.Filter)
		if err != nil {
			return core.Page{}, err
		}
		q.PlaylistSource = &source
	}
	q.Filter, err = c.availability.PagingFilter(ctx, q.Filter)
	if err != nil {
		return core.Page{}, err
	}
	q.Filter = c.withStepAside(ctx, q.Filter)
	return c.Catalog.Browse(ctx, q)
}

// withStepAside excludes the titles a step-aside Plex Media Server holds
// (Plex surface only), keeping what the viewer has in progress on Ether.
func (c *availabilityCatalog) withStepAside(ctx context.Context, f core.Filter) core.Filter {
	if c.stepAside == nil {
		return f
	}
	switch f.Type {
	case core.TypeMovie, core.TypeShow, core.TypeSeason, core.TypeEpisode:
		if keys, active := c.stepAside.ExcludedKeys(ctx, nil, f.Type); active && len(keys) > 0 {
			f.ExcludeKeys = append(append([]core.RatingKey(nil), f.ExcludeKeys...), keys...)
		}
	}
	return f
}

// BrowseAvailabilityInSQL marks pages whose availability decision was applied
// before counting and pagination.
func (c *availabilityCatalog) BrowseAvailabilityInSQL() bool { return true }
func (c *availabilityCatalog) Facet(ctx context.Context, t core.MediaType, facet string) ([]core.FacetValue, error) {
	return c.FacetFilter(ctx, core.Filter{Type: t}, facet)
}
func (c *availabilityCatalog) FacetFilter(ctx context.Context, f core.Filter, facet string) ([]core.FacetValue, error) {
	f, err := c.availability.PagingFilter(ctx, f)
	if err != nil {
		return nil, err
	}
	// Facet counts (the first-letter jump bar above all) must count the same
	// titles the page lists, or Plex apps jump to the wrong offset.
	f = c.withStepAside(ctx, f)
	return c.Catalog.FacetFilter(ctx, f, facet)
}
func (c *availabilityCatalog) Children(ctx context.Context, k core.RatingKey) ([]core.Title, error) {
	ts, e := c.Catalog.Children(ctx, k)
	if e != nil {
		return nil, e
	}
	return c.availability.FilterDiscovery(ctx, c.Catalog.FilterVisibility(ctx, ts)), nil
}
func (c *availabilityCatalog) Grandchildren(ctx context.Context, k core.RatingKey) ([]core.Title, error) {
	ts, e := c.Catalog.Grandchildren(ctx, k)
	if e != nil {
		return nil, e
	}
	return c.availability.FilterDiscovery(ctx, c.Catalog.FilterVisibility(ctx, ts)), nil
}
func (c *availabilityCatalog) Search(ctx context.Context, q string, types []core.MediaType, n int) ([]core.Title, error) {
	gf, err := c.availability.PagingFilter(ctx, core.Filter{})
	if err != nil {
		return nil, err
	}
	ts, e := c.Catalog.Search(catalog.WithGateFilter(ctx, gf), q, types, n)
	if e != nil {
		return nil, e
	}
	vis := c.Catalog.FilterVisibility(ctx, ts)
	if c.Catalog.TMDBConfigured() {
		return c.availability.FilterDiscovery(ctx, vis), nil
	}
	return vis, nil
}
