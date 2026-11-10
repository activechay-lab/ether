package app

import (
	"context"
	"sync"

	"github.com/braedonsaunders/ether/internal/availability"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontends/plex"
)

// newStepAsideFor builds a protocol-scoped StepAside policy. Only the Plex
// surface steps aside for synced Plex Media Servers; Jellyfin, Emby, and Silo
// viewers do not see the owner's PMS and always receive nil.
func (a *App) newStepAsideFor(protocol core.Protocol) *experience.StepAside {
	if a == nil || protocol != "plex" {
		return nil
	}
	var mu sync.Mutex
	var cachedRev uint64
	var cachedHeld experience.StepAsideHeld

	return experience.NewStepAside(experience.StepAsideOptions{
		Held: func() experience.StepAsideHeld {
			if a.plexServers == nil {
				return experience.StepAsideHeld{}
			}
			snap := a.plexServers.Held()
			if snap.Empty() {
				return experience.StepAsideHeld{}
			}
			mu.Lock()
			defer mu.Unlock()
			if cachedRev == snap.Rev {
				return cachedHeld
			}
			shows := make([]core.RatingKey, 0, len(snap.Shows))
			for k := range snap.Shows {
				shows = append(shows, k)
			}
			cachedRev = snap.Rev
			cachedHeld = experience.StepAsideHeld{
				Rev:      snap.Rev,
				Movies:   snap.Movies,
				Episodes: snap.Episodes,
				Shows:    shows,
			}
			return cachedHeld
		},
		AiredEpisodes: func(ctx context.Context, shows []core.RatingKey) (map[core.RatingKey][]core.RatingKey, error) {
			if a.catalog == nil {
				return nil, nil
			}
			return a.catalog.AiredShowEpisodes(ctx, shows)
		},
		WriteGeneration: func() uint64 {
			if a.catalog == nil {
				return 0
			}
			return a.catalog.WriteGeneration()
		},
		State: a.state,
		Sessions: func() []*core.PlaySession {
			if a.playback == nil {
				return nil
			}
			return a.playback.Sessions()
		},
	})
}

func (a *App) applyPlexStepAside(spikeEnabled bool, deps *plex.Deps) {
	if spikeEnabled || a == nil || deps == nil || a.catalog == nil {
		return
	}
	stepAside := a.newStepAsideFor("plex")
	if stepAside == nil {
		return
	}
	deps.Catalog = &plexStepAsideCatalog{
		Catalog:      a.catalog,
		availability: a.availability,
		stepAside:    stepAside,
	}
	deps.FilterAvailable = stepAside.WrapAvailable(deps.FilterAvailable)
	deps.Search = a.searchSourcesFor(stepAside)
	deps.Experience = experience.NewPlexExperienceWithStepAside(stepAside)
	if deps.HomeCache != nil {
		baseCatalog := deps.HomeCache.Catalog
		deps.HomeCache.Catalog = func() uint64 {
			var gen uint64
			if baseCatalog != nil {
				gen = baseCatalog()
			}
			if heldGen := stepAside.Generation(); heldGen != 0 {
				gen = gen*1099511628211 ^ heldGen
			}
			return gen
		}
	}
}

type plexStepAsideCatalog struct {
	*catalog.Catalog
	availability *availability.Service
	stepAside    *experience.StepAside
}

func (c *plexStepAsideCatalog) Browse(ctx context.Context, q core.BrowseQuery) (core.Page, error) {
	var err error
	if c.availability != nil {
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
	}
	postFilter := false
	if c.stepAside != nil {
		if exKeys, active := c.stepAside.ExcludedKeys(ctx, nil, q.Filter.Type); active && len(exKeys) > 0 {
			if len(q.Filter.ExcludeKeys)+len(exKeys) <= 2000 {
				q.Filter.ExcludeKeys = append(append([]core.RatingKey{}, q.Filter.ExcludeKeys...), exKeys...)
			} else {
				postFilter = true
			}
		}
	}
	page, err := c.Catalog.Browse(ctx, q)
	if err != nil || !postFilter {
		return page, err
	}
	page.Items = c.stepAside.FilterAvailable(ctx, page.Items)
	return page, nil
}

func (c *plexStepAsideCatalog) BrowseAvailabilityInSQL() bool {
	return c.availability != nil && (c.stepAside == nil || !c.stepAside.Active())
}

func (c *plexStepAsideCatalog) Facet(ctx context.Context, t core.MediaType, facet string) ([]core.FacetValue, error) {
	return c.FacetFilter(ctx, core.Filter{Type: t}, facet)
}

func (c *plexStepAsideCatalog) FacetFilter(ctx context.Context, f core.Filter, facet string) ([]core.FacetValue, error) {
	if c.availability != nil {
		var err error
		f, err = c.availability.PagingFilter(ctx, f)
		if err != nil {
			return nil, err
		}
	}
	if c.stepAside != nil {
		if exKeys, active := c.stepAside.ExcludedKeys(ctx, nil, f.Type); active && len(exKeys) > 0 && len(f.ExcludeKeys)+len(exKeys) <= 2000 {
			f.ExcludeKeys = append(append([]core.RatingKey{}, f.ExcludeKeys...), exKeys...)
		}
	}
	return c.Catalog.FacetFilter(ctx, f, facet)
}

func (c *plexStepAsideCatalog) Children(ctx context.Context, k core.RatingKey) ([]core.Title, error) {
	ts, err := c.Catalog.Children(ctx, k)
	if err != nil {
		return nil, err
	}
	if c.availability != nil {
		ts = c.availability.FilterDiscovery(ctx, c.Catalog.FilterVisibility(ctx, ts))
	}
	return c.stepAside.FilterAvailable(ctx, ts), nil
}

func (c *plexStepAsideCatalog) Grandchildren(ctx context.Context, k core.RatingKey) ([]core.Title, error) {
	ts, err := c.Catalog.Grandchildren(ctx, k)
	if err != nil {
		return nil, err
	}
	if c.availability != nil {
		ts = c.availability.FilterDiscovery(ctx, c.Catalog.FilterVisibility(ctx, ts))
	}
	return c.stepAside.FilterAvailable(ctx, ts), nil
}

func (c *plexStepAsideCatalog) Search(ctx context.Context, q string, types []core.MediaType, n int) ([]core.Title, error) {
	ts, err := c.Catalog.Search(ctx, q, types, n)
	if err != nil {
		return nil, err
	}
	if c.availability != nil {
		ts = c.availability.FilterDiscovery(ctx, c.Catalog.FilterVisibility(ctx, ts))
	}
	return c.stepAside.FilterAvailable(ctx, ts), nil
}

func (c *plexStepAsideCatalog) CountUnplayedAired(ctx context.Context, userID int64, keys []core.RatingKey, maxRating string) (map[core.RatingKey]int, error) {
	counts, err := c.Catalog.CountUnplayedAired(ctx, userID, keys, maxRating)
	if err != nil || c.stepAside == nil {
		return counts, err
	}
	return c.stepAside.AdjustUnplayedCounts(ctx, userID, keys, counts)
}
