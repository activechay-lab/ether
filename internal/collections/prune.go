package collections

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("collections_retention", "collections")
	store.Register("collections_retention", 1, `CREATE INDEX collections_air_dates_attempted ON collections_air_dates(attempted_at) WHERE attempted_at>0;`)
}

// Retention for the collections lookup caches. All are derived from TMDB
// and catalogue lookups, so a deleted row only costs one repeat lookup.
const (
	// nameRetention: name<->id rows are re-validated after 30 days on use
	// and kept stale as a fallback when TMDB is down; rows untouched for
	// six months belong to names no spec references any more.
	nameRetention = 180 * 24 * time.Hour
	// externalRetention: IMDb/Letterboxd/title -> rating key mappings are
	// written once and never refreshed on read, so an item still on a list
	// is simply re-resolved once a year.
	externalRetention = 365 * 24 * time.Hour
	// airDateRetention bounds cached show air-date lookups for shows no
	// active collection has re-evaluated in 90 days.
	airDateRetention = 90 * 24 * time.Hour
)

// Prune deletes stale rows from collections_names, collections_external and
// collections_air_dates. Specs, members and owner data are never touched.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	now := s.now()
	var total int64
	for _, q := range []struct {
		sql string
		age time.Duration
	}{
		{`DELETE FROM collections_names WHERE at<?`, nameRetention},
		{`DELETE FROM collections_external WHERE at<?`, externalRetention},
	} {
		res, err := s.db.ExecContext(ctx, q.sql, now.Add(-q.age).Unix())
		if err != nil {
			return total, err
		}
		n, _ := res.RowsAffected()
		total += n
	}
	n, err := store.PruneBatched(ctx, s.db,
		`DELETE FROM collections_air_dates WHERE show_key IN (SELECT show_key FROM collections_air_dates WHERE attempted_at>0 AND attempted_at<? ORDER BY attempted_at LIMIT ?)`,
		now.Add(-airDateRetention).Unix())
	return total + n, err
}
