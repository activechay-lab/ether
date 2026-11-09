package discovery

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/ratings"
	"github.com/braedonsaunders/ether/internal/watchlist"
)

// Frontend adapts Features to the domain-neutral frontend contract.
func (d *Features) Frontend() *frontend.DiscoveryFeatures {
	if d == nil {
		return nil
	}
	out := &frontend.DiscoveryFeatures{
		OverviewTranslate: d.OverviewTranslate,
		SelectArtworkFn:   d.SelectArtwork,
		PrepareArtworkFn:  d.PrepareArtwork,
		RenderArtworkFn:   d.RenderArtwork,
		CachedRecapFn:     d.CachedRecap,
	}
	if d.Watchlist != nil {
		out.Watchlist = discoveryWatchlist{d.Watchlist}
	}
	if d.Ratings != nil {
		out.Ratings = discoveryRatings{d.Ratings}
	}
	if d.Overlays != nil {
		out.Overlays = d.Overlays
	}
	if d.Personal != nil {
		out.Personal = d.Personal
	}
	if d.LLM != nil {
		out.LLM = d.LLM
	}
	return out
}

type discoveryWatchlist struct {
	s *watchlist.Service
}

func (w discoveryWatchlist) Hubs(ctx context.Context, viewer *core.User, limit int) ([]core.Hub, error) {
	return w.s.Hubs(ctx, viewer, limit)
}

func (w discoveryWatchlist) RecapsEnabled(ctx context.Context) bool {
	cfg, _ := w.s.Settings(ctx)
	return cfg.Recaps
}

func (w discoveryWatchlist) NaturalSearchEnabled(ctx context.Context) bool {
	cfg, err := w.s.Settings(ctx)
	return err == nil && cfg.NaturalSearch
}

type discoveryRatings struct {
	s *ratings.Service
}

func (r discoveryRatings) Cached(ctx context.Context, t *core.Title) ([]frontend.DiscoveryRatingBadge, error) {
	badges, err := r.s.Cached(ctx, t)
	if err != nil || len(badges) == 0 {
		return nil, err
	}
	out := make([]frontend.DiscoveryRatingBadge, len(badges))
	for i, b := range badges {
		out[i] = frontend.DiscoveryRatingBadge{
			Source: b.Source,
			Value:  b.PlexValue(),
			Image:  b.PlexImage(),
		}
	}
	return out, nil
}

func (r discoveryRatings) Settings(ctx context.Context) (frontend.DiscoveryRatingsSettings, error) {
	cfg, err := r.s.Settings(ctx)
	if err != nil {
		return frontend.DiscoveryRatingsSettings{}, err
	}
	return frontend.DiscoveryRatingsSettings{
		Enabled:  cfg.Enabled,
		Critic:   cfg.Critic,
		Audience: cfg.Audience,
		Missing:  cfg.Missing,
	}, nil
}

func (r discoveryRatings) Queue(t core.Title) {
	r.s.Queue(t)
}

func (r discoveryRatings) WithSettingsSnapshot(ctx context.Context) context.Context {
	return r.s.WithSettingsSnapshot(ctx)
}
