package plex

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

func (s *Server) continueExperience(pr *Request) experience.Continue {
	batch := experience.EpisodeBatch{Catalog: s.d.Catalog, State: s.d.State}
	if s.d.EpisodeOrder != nil {
		batch.EpisodeOrder = s.d.EpisodeOrder
	}
	deck := &experience.ContinueDeck{
		Load: func(ctx context.Context, shows []core.RatingKey) (map[core.RatingKey][]core.Title, map[core.RatingKey]core.WatchStatus) {
			if pre, ok := s.d.Catalog.(interface {
				PreloadStored(context.Context, []core.RatingKey)
			}); ok && len(shows) > 1 {
				pre.PreloadStored(ctx, shows)
			}
			titles := make([]core.Title, 0, len(shows))
			for _, show := range shows {
				titles = append(titles, core.Title{Key: show, Type: core.TypeShow})
			}
			return batch.Load(ctx, pr.User, titles, !s.viewerRestricted(pr.User))
		},
		Hydrate: func(ctx context.Context, keys []core.RatingKey) []core.Title {
			return s.getTitles(withStoredPolicy(ctx), keys)
		},
		Unavailable: func(ctx context.Context, t *core.Title) bool { return s.sameDayUnavailable(ctx, pr, t) },
		Fallback: func(ctx context.Context, show core.RatingKey, eps []core.Title, prepared bool) (*core.Title, map[core.RatingKey]core.WatchStatus) {
			var memo showEpisodes
			if prepared {
				memo = showEpisodes{show: {eps: eps}}
			}
			return s.showOnDeck(ctx, pr, show, memo)
		},
	}
	if s.d.EpisodeOrder != nil {
		deck.Order = func(ctx context.Context, show core.RatingKey, eps []core.Title) ([]core.Title, error) {
			return s.d.EpisodeOrder.OrderStoredGrandchildren(ctx, pr.User, show, eps)
		}
	}
	c := experience.Continue{ShowOnDeck: deck.Select, PrepareShowOnDeck: deck.Prepare, State: s.d.State, Now: s.now, FavoriteShows: s.d.FavoriteShows,
		Grandchildren: func(ctx context.Context, k core.RatingKey) ([]core.Title, error) {
			return s.metadataGrandchildren(ctx, pr, k)
		},
		Unrestricted: func(u *core.User) bool { return !s.viewerRestricted(u) },
		NextEpisode: func(ctx context.Context, k core.RatingKey) (*core.Title, error) {
			if local, ok := s.d.Catalog.(interface {
				ContinueEpisodes(context.Context, []core.RatingKey) (map[core.RatingKey]*core.Title, error)
			}); ok {
				next, err := local.ContinueEpisodes(ctx, []core.RatingKey{k})
				if err != nil {
					return nil, err
				}
				if t := next[k]; t != nil {
					return t, nil
				}
				return nil, core.ErrNotFound
			}
			return s.d.Catalog.NextEpisode(ctx, k)
		}, Visible: s.visibleTitle,
		SameDayUnavailable: func(ctx context.Context, t *core.Title) bool { return s.sameDayUnavailable(ctx, pr, t) },
		GetTitles:          s.getTitles, SectionAllowed: func(u *core.User, t core.MediaType) bool {
			sec, ok := s.sectionFor(t)
			return !ok || s.canSeeSection(u, sec.ID)
		},
		OnError: func(err error) { s.log.Warn("load in-progress", "err", err) }}
	// Checking a selected episode's visibility loads its parent series. Read
	// those parents together before walking the history candidates; otherwise
	// a cold cache hydrates each show's facts, counts and dates separately.
	// Keep Get's normal freshness and policy checks after this stored preload.
	if pre, ok := s.d.Catalog.(interface {
		PreloadStored(context.Context, []core.RatingKey)
	}); ok {
		c.PrefetchTitles = pre.PreloadStored
	}
	if s.d.Discovery != nil && s.d.Discovery.Personal != nil {
		c.Filter = s.d.Discovery.Personal.FilterContinue
	}
	if s.d.EpisodeOrder != nil {
		c.EpisodeOrder = s.d.EpisodeOrder
	}
	if local, ok := s.d.Catalog.(interface {
		ContinueEpisodes(context.Context, []core.RatingKey) (map[core.RatingKey]*core.Title, error)
	}); ok {
		c.NextEpisodes = local.ContinueEpisodes
	}
	c.Filter = experience.ContinueFilter(c.State, c.Filter)
	return c
}
