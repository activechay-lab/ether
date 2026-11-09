// Package homefilter shares bounded discovery suitability checks during one
// multi-request Home load. Viewer authorization remains a separate check.
package homefilter

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	kit "github.com/braedonsaunders/ether/internal/frontend/kit/homefilter"
)

type Snapshot = kit.Snapshot

func With(ctx context.Context, snapshot *Snapshot) context.Context {
	return kit.With(ctx, snapshot)
}

// Allowed preserves the producer's first occurrence for each key, just as the
// catalogue filter does. Oversized pools are processed but never retained.
func Allowed(ctx context.Context, titles []core.Title, filter func(context.Context, []core.Title) []core.Title) map[core.RatingKey]bool {
	return kit.Allowed(ctx, titles, filter)
}
