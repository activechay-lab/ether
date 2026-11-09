package ratings

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("ratings_retention", "ratings")
	store.Register("ratings_retention", 1, `CREATE INDEX ratings_cache_updated ON ratings_cache(updated);`)
}

// maxCacheAge is the longest cache interval SaveSettings accepts (2160 h).
// A row older than this can never be served whatever the setting, so it is
// safe to delete; rows inside it are kept in case the owner raises the
// interval. Rows of a provider the owner switched away from age out too.
const maxCacheAge = 2160 * time.Hour

// Prune deletes rating rows older than the maximum cache interval. It is
// meant to be called periodically.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	age := maxCacheAge
	if cfg, err := s.settings(ctx); err == nil {
		if d := time.Duration(cfg.CacheHours) * time.Hour; d > age {
			age = d
		}
	}
	return store.PruneBatched(ctx, s.db,
		`DELETE FROM ratings_cache WHERE (item_key,provider) IN (SELECT item_key,provider FROM ratings_cache WHERE updated<? ORDER BY updated LIMIT ?)`,
		s.o.Now().Add(-age).Unix())
}
