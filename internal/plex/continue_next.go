package plex

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
)

func (s *Server) continueExperience(pr *Request) experience.Continue {
	var memo showEpisodes
	var states map[core.RatingKey]core.WatchStatus
	// Selection needs numbering and watch state, not every episode's cast.
	// Restore full metadata before any policy/availability check or rendering.
	resolved := map[core.RatingKey]*core.Title{}
	fullTitle := func(ctx context.Context, t *core.Title) *core.Title {
		if t == nil {
			return nil
		}
		if full, ok := resolved[t.Key]; ok {
			return full
		}
		for _, full := range s.getTitles(withStoredPolicy(ctx), []core.RatingKey{t.Key}) {
			resolved[full.Key] = &full
		}
		return resolved[t.Key]
	}
	c := experience.Continue{ShowOnDeck: func(ctx context.Context, show core.RatingKey) (*core.Title, map[core.RatingKey]core.WatchStatus) {
		if list, ok := memo[show]; ok && states != nil {
			// appendNext uses the most recent timestamp in this map. Keep it
			// scoped to this show even though its state was fetched in a batch.
			st := make(map[core.RatingKey]core.WatchStatus, len(list.eps))
			for _, ep := range list.eps {
				if value, ok := states[ep.Key]; ok {
					st[ep.Key] = value
				}
			}
			selected := experience.ShowOnDeck(list.eps, st, func(t *core.Title) bool {
				full := fullTitle(ctx, t)
				return full == nil || s.sameDayUnavailable(ctx, pr, full)
			})
			return fullTitle(ctx, selected), st
		}
		return s.showOnDeck(ctx, pr, show, memo)
	}, PrepareShowOnDeck: func(ctx context.Context, shows []core.RatingKey) {
		if pre, ok := s.d.Catalog.(interface {
			PreloadStored(context.Context, []core.RatingKey)
		}); ok && len(shows) > 1 {
			// Each candidate's visibility checks its series: read them all
			// at once.
			pre.PreloadStored(ctx, shows)
		}
		titles := make([]core.Title, 0, len(shows))
		for _, show := range shows {
			titles = append(titles, core.Title{Key: show, Type: core.TypeShow})
		}
		memo, states = s.prefetchListEpisodesWith(ctx, pr, titles, !s.viewerRestricted(pr.User))
		if s.d.EpisodeOrder != nil {
			// Even an ungrouped show can hide or reorder Specials. Apply
			// presentation to the batch itself: a concurrent catalogue write
			// can invalidate its cache before a second Grandchildren read.
			for show, list := range memo {
				eps, err := s.d.EpisodeOrder.OrderStoredGrandchildren(ctx, pr.User, show, list.eps)
				if err != nil {
					delete(memo, show)
					continue
				}
				memo[show] = showEpisodeList{eps: eps}
			}
		}
		// Most shows need just this first candidate; batch its full metadata.
		// If availability excludes it, ShowOnDeck loads the next candidate.
		var keys []core.RatingKey
		for _, list := range memo {
			if t := experience.ShowOnDeck(list.eps, states, func(*core.Title) bool { return false }); t != nil {
				keys = append(keys, t.Key)
			}
		}
		for _, t := range s.getTitles(withStoredPolicy(ctx), keys) {
			resolved[t.Key] = &t
		}
	}, State: s.d.State, Now: s.now, FavoriteShows: s.d.FavoriteShows,
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
	if hidden, ok := s.d.State.(interface {
		ContinueHidden(context.Context, int64, []core.RatingKey) (map[core.RatingKey]bool, error)
	}); ok {
		personal := c.Filter
		c.Filter = func(ctx context.Context, viewer *core.User, items []core.Title) []core.Title {
			if viewer == nil {
				return nil
			}
			keys := make([]core.RatingKey, 0, len(items))
			for _, item := range items {
				key := item.Key
				if item.Type == core.TypeEpisode {
					key = core.ShowKey(item.Key.TMDB())
				}
				keys = append(keys, key)
			}
			blocked, err := hidden.ContinueHidden(ctx, viewer.ID, keys)
			if err != nil {
				return nil
			}
			out := items[:0]
			for i, item := range items {
				if !blocked[keys[i]] {
					out = append(out, item)
				}
			}
			if personal != nil {
				out = personal(ctx, viewer, out)
			}
			return out
		}
	}
	if s.d.EpisodeOrder != nil {
		c.EpisodeOrder = s.d.EpisodeOrder
	}
	if local, ok := s.d.Catalog.(interface {
		ContinueEpisodes(context.Context, []core.RatingKey) (map[core.RatingKey]*core.Title, error)
	}); ok {
		c.NextEpisodes = local.ContinueEpisodes
	}
	return c
}
