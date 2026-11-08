package experience

import (
	"context"
	"github.com/braedonsaunders/ether/internal/store"
	"time"
)

// RequestDatabaseObservation reads existing request counters without issuing
// queries. Missing instrumentation reports zero work, as before.
func RequestDatabaseObservation(ctx context.Context) (int64, time.Duration) {
	if stats := store.QueryStatsFrom(ctx); stats != nil {
		return stats.Queries(), stats.DBTime()
	}
	return 0, 0
}
