package app

import (
	"context"
	"github.com/braedonsaunders/ether/internal/availability"
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

func (a *App) buildAvailability() error {
	s, err := availability.New(availability.Deps{Clustered: a.clustered(), DB: a.db, Config: a.cfg.Read, ConfigGeneration: a.cfg.Generation, Feeds: a.indexers, Catalog: a.catalog, Validator: a.resolver,
		CacheCheck: a.debrid.CheckCached, Search: func(ctx context.Context, req core.SearchRequest) ([]core.Release, error) {
			if a.indexers == nil {
				return nil, nil
			}
			return a.indexers.Search(ctx, req)
		}, Watchlist: func(ctx context.Context) (map[core.RatingKey]bool, error) {
			out := map[core.RatingKey]bool{}
			follow := func(k core.RatingKey) {
				out[k] = true
				if k.Kind() == core.KindEpisode || k.Kind() == core.KindSeason {
					out[core.ShowKey(k.TMDB())] = true
				}
			}
			rows, err := a.db.QueryContext(ctx, `SELECT DISTINCT item_key FROM watchlist_items`)
			if err != nil {
				return nil, err
			}
			defer rows.Close()
			for rows.Next() {
				var key int64
				if err := rows.Scan(&key); err != nil {
					return nil, err
				}
				follow(core.RatingKey(key))
			}
			if err := rows.Err(); err != nil {
				return nil, err
			}
			if a.state != nil {
				recent, err := a.state.RecentlyViewedKeys(ctx, time.Now().Add(-30*24*time.Hour), 200)
				if err != nil {
					return nil, err
				}
				for _, k := range recent {
					follow(k)
				}
			}
			return out, nil
		}, Active: func() bool { return len(a.playback.Sessions()) > 0 },
		Seal: func(raw string) (string, error) { return a.cfg.Seal("availability-release", []byte(raw)) },
		Open: func(raw string) (string, error) {
			b, e := a.cfg.Unseal("availability-release", raw)
			return string(b), e
		},
		Logger: a.log.With("component", "availability"),
		// Section and home loads read the last evidence answer; judging
		// retained feed releases never runs inside a request (#303).
		Async: true,
		// Background requests to one indexer stay at least this far apart.
		Pace: 3 * time.Second,
	})
	a.availability = s
	if err == nil {
		a.closers = append(a.closers, s.Close)
	}
	return err
}
func (a *App) discoveredReleases(ctx context.Context, t *core.Title) []core.Release {
	if a.availability == nil {
		return nil
	}
	return a.availability.Candidates(ctx, t)
}
func (a *App) observeRelease(ctx context.Context, t *core.Title, r core.Release) {
	if a.availability != nil {
		a.availability.Observe(ctx, t, r)
	}
}

func (a *App) invalidateRelease(ctx context.Context, id string) {
	if a.availability != nil {
		a.availability.Invalidate(ctx, id)
	}
}

func (a *App) observeSearch(ctx context.Context, t *core.Title, rs []core.Release) {
	if a.availability != nil {
		a.availability.RecordSearch(ctx, t, rs)
	}
}
