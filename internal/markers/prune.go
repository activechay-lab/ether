package markers

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// Retention finds rows by age.
	store.Register("markers", 4, `
CREATE INDEX markers_cache_expires ON markers_cache(expires_at);
CREATE INDEX markers_plugin_cache_expires ON markers_plugin_cache(expires_at);`)
	store.Register("markers_breaks", 4, `
CREATE INDEX markers_breaks_cache_analyzed ON markers_breaks_cache(analyzed_at);
CREATE INDEX markers_breaks_observed_updated ON markers_breaks_observed(updated_at);`)
	store.RegisterAfter("markers_retention", "markers")
	store.Register("markers_retention", 1, `CREATE INDEX markers_refresh_queue_played ON markers_refresh_queue(last_played);`)
}

const (
	// BreakRetention is how long an act-break analysis or playout
	// observation is kept. A release that has not been analysed or aired
	// for this long is analysed again before its next airing (pre-air
	// Analyze decodes on a cache miss, inside the daily scan budget).
	BreakRetention = 180 * 24 * time.Hour
	// BreakBudgetRetention is how long a day's scan-budget row is kept;
	// only today's row is ever read.
	BreakBudgetRetention = 30 * 24 * time.Hour
	// RefreshQueueRetention bounds markers_refresh_queue even when no
	// marker plugin refresh loop is active.
	RefreshQueueRetention = 30 * 24 * time.Hour
)

// Prune deletes expired rows from the remote-lookup caches (markers_cache,
// markers_plugin_cache), act-break analyses and observations older than
// BreakRetention, scan-budget days older than BreakBudgetRetention, and
// refresh-queue entries older than RefreshQueueRetention.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	if s.o.DB == nil {
		return 0, nil
	}
	now := s.o.Now()
	breaks := now.Add(-BreakRetention).Unix()
	var total int64
	for _, q := range []struct {
		del string
		arg any
	}{
		{`DELETE FROM markers_cache WHERE (rating_key,runtime_ms) IN (SELECT rating_key,runtime_ms FROM markers_cache WHERE expires_at<=? ORDER BY expires_at LIMIT ?)`, now.Unix()},
		{`DELETE FROM markers_plugin_cache WHERE (rating_key,runtime_ms,source) IN (SELECT rating_key,runtime_ms,source FROM markers_plugin_cache WHERE expires_at<=? ORDER BY expires_at LIMIT ?)`, now.Unix()},
		{`DELETE FROM markers_breaks_cache WHERE (release_key,version) IN (SELECT release_key,version FROM markers_breaks_cache WHERE analyzed_at<? ORDER BY analyzed_at LIMIT ?)`, breaks},
		{`DELETE FROM markers_breaks_observed WHERE (release_key,version) IN (SELECT release_key,version FROM markers_breaks_observed WHERE updated_at<? ORDER BY updated_at LIMIT ?)`, breaks},
		// day is a UTC YYYY-MM-DD string, so it orders as text.
		{`DELETE FROM markers_breaks_budget WHERE day IN (SELECT day FROM markers_breaks_budget WHERE day<? ORDER BY day LIMIT ?)`, now.UTC().Add(-BreakBudgetRetention).Format("2006-01-02")},
		{`DELETE FROM markers_refresh_queue WHERE (rating_key,runtime_ms) IN (SELECT rating_key,runtime_ms FROM markers_refresh_queue WHERE last_played<? ORDER BY last_played LIMIT ?)`, now.Add(-RefreshQueueRetention).Unix()},
	} {
		n, err := store.PruneBatched(ctx, s.o.DB, q.del, q.arg)
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}
