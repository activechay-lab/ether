package availability

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
)

type consoleEvidenceCatalog interface {
	RecordConsoleReleaseEvidence(context.Context, core.RatingKey, core.Release, string, string, int64, int64, int) error
	InvalidateConsoleReleaseEvidence(context.Context, string)
}

// All reads are keyed local rows. Only definitive provider answers enter cached;
// addon-provided markers remain hints. Absence and stale evidence stay unknown.
func (s *Service) publishConsoleEvidence(ctx context.Context, key core.RatingKey, r core.Release, source, policy string, cached int) {
	c, ok := s.d.Catalog.(consoleEvidenceCatalog)
	if !ok {
		return
	}
	var verified int64
	var state, storedPolicy string
	if err := s.db.QueryRowContext(ctx, `SELECT verified_at,state,policy FROM availability_releases WHERE id=? AND rating_key=? AND source_id=?`, r.ID, int64(key), source).Scan(&verified, &state, &storedPolicy); err != nil {
		return
	}
	until := int64(0)
	if state == "verified" && storedPolicy == policy && verified > 0 {
		until = verified + 21600
	}
	cacheUntil := int64(0)
	if cached >= 0 {
		cacheUntil = s.now().Unix() + 3600
	}
	if err := c.RecordConsoleReleaseEvidence(ctx, key, r, source, policy, until, cacheUntil, cached); err != nil {
		s.log.Debug("catalogue evidence projection", "error", err)
	}
}
func (s *Service) invalidateConsoleEvidence(ctx context.Context, id string) {
	c, ok := s.d.Catalog.(consoleEvidenceCatalog)
	if !ok {
		return
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT id FROM availability_releases WHERE id=? OR checked_id=?`, id, id)
	if err != nil {
		return
	}
	var ids []string
	for rows.Next() {
		var release string
		if rows.Scan(&release) == nil {
			ids = append(ids, release)
		}
	}
	rows.Close()
	for _, release := range ids {
		c.InvalidateConsoleReleaseEvidence(ctx, release)
	}
}
