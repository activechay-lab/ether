package experience

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// DiscoverySearch uses natural search only when enabled and successful.
func DiscoverySearch(source *frontend.DiscoveryFeatures) func(context.Context, *core.User, string, int) ([]core.Title, bool) {
	return func(ctx context.Context, viewer *core.User, query string, limit int) ([]core.Title, bool) {
		if source == nil || source.LLM == nil || source.Watchlist == nil || !source.Watchlist.NaturalSearchEnabled(ctx) {
			return nil, false
		}
		items, err := source.LLM.NaturalSearch(ctx, viewer, query, limit)
		if err != nil {
			return nil, false
		}
		return items, true
	}
}
