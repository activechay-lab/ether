package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

type storedEpisodeBatchReader interface {
	StoredGrandchildren(context.Context, []core.RatingKey) (map[core.RatingKey][]core.Title, error)
}

// EpisodeBatch loads canonical shows and their viewer state together. Shows
// with customized groups and shows listed with seasons retain the caller's
// individual presentation path rather than substituting canonical episodes.
type EpisodeBatch struct {
	Catalog      core.Catalog
	State        core.State
	EpisodeOrder interface {
		Grouped(context.Context, *core.User, []core.RatingKey) (map[core.RatingKey]bool, error)
	}
}

// Load uses lightweight records only when the caller's policy authorizes them.
// Returned state is request-local and must never be cached across viewers.
func (b EpisodeBatch) Load(ctx context.Context, viewer *core.User, ts []core.Title, selectionOnly bool) (map[core.RatingKey][]core.Title, map[core.RatingKey]core.WatchStatus) {
	store, ok := b.Catalog.(storedEpisodeBatchReader)
	if !ok || b.State == nil || viewer == nil {
		return nil, nil
	}
	withSeasons := map[core.RatingKey]bool{}
	for i := range ts {
		if ts[i].Type == core.TypeSeason {
			withSeasons[core.ShowKey(ts[i].Key.TMDB())] = true
		}
	}
	var shows []core.RatingKey
	for i := range ts {
		if ts[i].Type == core.TypeShow && !withSeasons[ts[i].Key] {
			shows = append(shows, ts[i].Key)
		}
	}
	if len(shows) == 0 {
		return nil, nil
	}
	if b.EpisodeOrder != nil {
		grouped, err := b.EpisodeOrder.Grouped(ctx, viewer, shows)
		if err != nil {
			return nil, nil
		}
		kept := shows[:0]
		for _, k := range shows {
			if !grouped[k] {
				kept = append(kept, k)
			}
		}
		shows = kept
	}
	load := store.StoredGrandchildren
	if selectionOnly {
		if candidates, ok := b.Catalog.(interface {
			StoredEpisodeCandidates(context.Context, []core.RatingKey) (map[core.RatingKey][]core.Title, error)
		}); ok {
			load = candidates.StoredEpisodeCandidates
		}
	}
	found, err := load(ctx, shows)
	if err != nil || len(found) == 0 {
		return nil, nil
	}
	var keys []core.RatingKey
	for _, eps := range found {
		for _, e := range eps {
			keys = append(keys, e.Key)
		}
	}
	states, err := b.State.GetMany(ctx, viewer.ID, keys)
	if err != nil {
		return found, nil
	}
	return found, states
}
