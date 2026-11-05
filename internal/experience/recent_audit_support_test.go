package experience

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// RecentlyViewed loads unique history entries in their most recent order.
func RecentlyViewed(ctx context.Context, viewer *core.User, libraryID int, visibility Visibility, history func(context.Context, int64, time.Time, int) ([]core.HistoryEntry, error), titles func(context.Context, []core.RatingKey) []core.Title, libraryFor func(core.MediaType) (int, bool)) ([]core.Title, error) {
	return (Recent{Visibility: visibility}).Viewed(ctx, viewer, libraryID, history, titles, libraryFor)
}
