package perfharness

import (
	"context"
	"fmt"

	"github.com/braedonsaunders/ether/internal/availability"
	"github.com/braedonsaunders/ether/internal/core"
)

// HiddenChurnKeys is how many keys one churned republish adds and removes:
// real evidence arriving between two refreshes on a large library.
const HiddenChurnKeys = 24

// HiddenChurnRow measures one background hidden-set republish after churn.
// It publishes a set sized with the library (a tenth of its movies, keys
// far outside the fixture's TMDB ids so no listing changes), drops
// HiddenChurnKeys keys and adds HiddenChurnKeys new ones, and republishes.
// The row's rows are the membership rows that republish wrote, its bounds
// and the gate's scale comparison fail if they track the set instead of
// the churn (the full-generation-copy regression: a ~1.3 s writer hold
// every refresh on a large library).
func HiddenChurnRow(ctx context.Context, svc *availability.Service, scale Scale) (*Row, error) {
	if svc == nil {
		return nil, fmt.Errorf("hidden churn: no availability service")
	}
	setSize := max(scale.Movies/10, 4*HiddenChurnKeys)
	const base = 900_000_000
	keys := make([]core.RatingKey, 0, setSize)
	for i := range setSize {
		keys = append(keys, core.MovieKey(int64(base+i)))
	}
	if _, err := svc.PublishHiddenSet(ctx, "movie", keys); err != nil {
		return nil, fmt.Errorf("hidden churn: publish base set: %w", err)
	}
	churned := append([]core.RatingKey{}, keys[HiddenChurnKeys:]...)
	for i := range HiddenChurnKeys {
		churned = append(churned, core.MovieKey(int64(base+setSize+i)))
	}
	stats, err := svc.PublishHiddenSet(ctx, "movie", churned)
	if err != nil {
		return nil, fmt.Errorf("hidden churn: republish: %w", err)
	}
	if stats.Added != HiddenChurnKeys || stats.Removed != HiddenChurnKeys {
		return nil, fmt.Errorf("hidden churn: republish wrote +%d/-%d keys, want +%d/-%d", stats.Added, stats.Removed, HiddenChurnKeys, HiddenChurnKeys)
	}
	written := int64(stats.Added + stats.Removed)
	stmts := int64(6 + (stats.Added+499)/500 + (stats.Removed+499)/500)
	return &Row{
		Client: "background", Step: "hidden publish churn",
		RowsMin: written, RowsMax: written,
		// A little bookkeeping (current pointer, retire record, retention
		// sweep) rides along; a copy of the set never fits under this.
		RowsBound: 8 * 2 * HiddenChurnKeys,
		QueriesMin: stmts, QueriesMax: stmts,
		DBTime: stats.Hold, WarmP50: stats.Hold,
	}, nil
}
