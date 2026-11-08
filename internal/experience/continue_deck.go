package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// ContinueDeck owns request-scoped history candidate selection. Loads may use
// lightweight episode records; selection always restores full metadata before
// checking availability or returning a title to a caller's visibility policy.
// It is used serially within one viewer request, never shared between viewers.
type ContinueDeck struct {
	Load        func(context.Context, []core.RatingKey) (map[core.RatingKey][]core.Title, map[core.RatingKey]core.WatchStatus)
	Order       func(context.Context, core.RatingKey, []core.Title) ([]core.Title, error)
	Hydrate     func(context.Context, []core.RatingKey) []core.Title
	Unavailable func(context.Context, *core.Title) bool
	// Fallback receives an ordered batch when state loading failed; it must
	// retain that snapshot while retrying state rather than reloading episodes.
	Fallback func(context.Context, core.RatingKey, []core.Title, bool) (*core.Title, map[core.RatingKey]core.WatchStatus)
	episodes map[core.RatingKey][]core.Title
	states   map[core.RatingKey]core.WatchStatus
	resolved map[core.RatingKey]*core.Title
}

func (d *ContinueDeck) full(ctx context.Context, t *core.Title) *core.Title {
	if t == nil {
		return nil
	}
	if full, ok := d.resolved[t.Key]; ok {
		return full
	}
	for _, full := range d.Hydrate(ctx, []core.RatingKey{t.Key}) {
		d.resolved[full.Key] = &full
	}
	return d.resolved[t.Key]
}

// Prepare applies presentation to the actual batch, so cache invalidation
// between reads cannot restore hidden specials or discard episode ordering.
func (d *ContinueDeck) Prepare(ctx context.Context, shows []core.RatingKey) {
	if d.resolved == nil {
		d.resolved = map[core.RatingKey]*core.Title{}
	}
	d.episodes, d.states = d.Load(ctx, shows)
	if d.Order != nil {
		for show, eps := range d.episodes {
			ordered, err := d.Order(ctx, show, eps)
			if err != nil {
				delete(d.episodes, show)
				continue
			}
			d.episodes[show] = ordered
		}
	}
	var keys []core.RatingKey
	for _, eps := range d.episodes {
		if t := ShowOnDeck(eps, d.states, func(*core.Title) bool { return false }); t != nil {
			keys = append(keys, t.Key)
		}
	}
	for _, t := range d.Hydrate(ctx, keys) {
		d.resolved[t.Key] = &t
	}
}

func (d *ContinueDeck) Select(ctx context.Context, show core.RatingKey) (*core.Title, map[core.RatingKey]core.WatchStatus) {
	if eps, ok := d.episodes[show]; ok && d.states != nil {
		// Recency must come only from this show's state, not its batch neighbors.
		states := make(map[core.RatingKey]core.WatchStatus, len(eps))
		for _, ep := range eps {
			if st, ok := d.states[ep.Key]; ok {
				states[ep.Key] = st
			}
		}
		selected := ShowOnDeck(eps, states, func(t *core.Title) bool {
			full := d.full(ctx, t)
			return full == nil || d.Unavailable(ctx, full)
		})
		return d.full(ctx, selected), states
	}
	eps, prepared := d.episodes[show]
	return d.Fallback(ctx, show, eps, prepared)
}

// ContinueFilter composes persisted removals with optional personal preferences.
// Surfaces can omit the composed filter for library-scoped resume listings.
func ContinueFilter(state core.State, personal func(context.Context, *core.User, []core.Title) []core.Title) func(context.Context, *core.User, []core.Title) []core.Title {
	return func(ctx context.Context, viewer *core.User, items []core.Title) []core.Title {
		filtered, ok := (Continue{State: state}).filterHidden(ctx, viewer, items)
		if !ok {
			return nil
		}
		if personal != nil {
			return personal(ctx, viewer, filtered)
		}
		return filtered
	}
}

// filterHidden applies persisted viewer removals before optional personal
// preferences on every surface. A failed read must not redisplay removed items.
func (c Continue) filterHidden(ctx context.Context, viewer *core.User, items []core.Title) ([]core.Title, bool) {
	hidden, ok := c.State.(interface {
		ContinueHidden(context.Context, int64, []core.RatingKey) (map[core.RatingKey]bool, error)
	})
	if !ok {
		return items, true
	}
	if viewer == nil {
		return nil, false
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
		return nil, false
	}
	out := items[:0]
	for i, item := range items {
		if !blocked[keys[i]] {
			out = append(out, item)
		}
	}
	return out, true
}
