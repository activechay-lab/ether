package livetv

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	store.RegisterAfter("livetv_retention", "livetv")
	store.Register("livetv_retention", 1, `
ALTER TABLE livetv_guide_ai ADD COLUMN updated_ms BIGINT NOT NULL DEFAULT 0;
CREATE INDEX livetv_guide_ai_updated ON livetv_guide_ai(updated_ms) WHERE updated_ms>0;
`)
}

const (
	guideAIRetention        = 30 * 24 * time.Hour
	personalHistoryPruneAge = 14 * 24 * time.Hour
	refreshLeasePruneMargin = time.Hour
)

// Prune deletes expired or superseded derived rows in livetv:
//   - livetv_guide_ai cached match proposals older than guideAIRetention;
//   - livetv_source_history rows older than historyRetention or past historyLimit;
//   - livetv_guide_undo tokens past expires_ms;
//   - livetv_refresh_leases past until_ms + refreshLeasePruneMargin;
//   - livetv_personal daily rows superseded for >14d or belonging to deleted
//     channels, plus any orphaned livetv_personal_programmes rows.
func (s *Service) Prune(ctx context.Context) (int64, error) {
	if s == nil || s.o.DB == nil {
		return 0, nil
	}
	db := s.o.DB
	now := s.o.Now()
	nowMS := now.UnixMilli()

	steps := []struct {
		del  string
		args []any
	}{
		{
			`DELETE FROM livetv_guide_ai WHERE fingerprint IN (SELECT fingerprint FROM livetv_guide_ai WHERE updated_ms>0 AND updated_ms<? ORDER BY updated_ms LIMIT ?)`,
			[]any{now.Add(-guideAIRetention).UnixMilli()},
		},
		{
			`DELETE FROM livetv_source_history WHERE key IN (SELECT key FROM livetv_source_history WHERE updated_ms<? ORDER BY updated_ms LIMIT ?)`,
			[]any{now.Add(-historyRetention).UnixMilli()},
		},
		{
			`DELETE FROM livetv_guide_undo WHERE token IN (SELECT token FROM livetv_guide_undo WHERE expires_ms<=? ORDER BY expires_ms LIMIT ?)`,
			[]any{nowMS},
		},
		{
			`DELETE FROM livetv_refresh_leases WHERE id IN (SELECT id FROM livetv_refresh_leases WHERE until_ms<=? ORDER BY until_ms LIMIT ?)`,
			[]any{now.Add(-refreshLeasePruneMargin).UnixMilli()},
		},
		{
			`DELETE FROM livetv_personal WHERE (viewer_id,channel_id,day) IN (SELECT p.viewer_id,p.channel_id,p.day FROM livetv_personal p WHERE NOT EXISTS (SELECT 1 FROM livetv_channels c WHERE c.id=p.channel_id) OR (p.built_at<? AND EXISTS (SELECT 1 FROM livetv_personal newer WHERE newer.viewer_id=p.viewer_id AND newer.channel_id=p.channel_id AND (newer.built_at>p.built_at OR (newer.built_at=p.built_at AND newer.day>p.day)))) LIMIT ?)`,
			[]any{now.Add(-personalHistoryPruneAge).UnixMilli()},
		},
		{
			`DELETE FROM livetv_personal_programmes WHERE (viewer_id,channel_id,day,start_ms) IN (SELECT i.viewer_id,i.channel_id,i.day,i.start_ms FROM livetv_personal_programmes i WHERE NOT EXISTS (SELECT 1 FROM livetv_personal p WHERE p.viewer_id=i.viewer_id AND p.channel_id=i.channel_id AND p.day=i.day) LIMIT ?)`,
			nil,
		},
	}

	var total int64
	for _, st := range steps {
		n, err := store.PruneBatched(ctx, db, st.del, st.args...)
		total += n
		if err != nil {
			return total, err
		}
	}

	var count int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM livetv_source_history`).Scan(&count); err != nil {
		return total, err
	}
	if count > historyLimit {
		var cutoffMS int64
		if err := db.QueryRowContext(ctx, `SELECT updated_ms FROM livetv_source_history ORDER BY updated_ms DESC LIMIT 1 OFFSET ?`, historyLimit-1).Scan(&cutoffMS); err == nil {
			n, err := store.PruneBatched(ctx, db,
				`DELETE FROM livetv_source_history WHERE key IN (SELECT key FROM livetv_source_history WHERE updated_ms<? ORDER BY updated_ms LIMIT ?)`,
				cutoffMS)
			total += n
			if err != nil {
				return total, err
			}
		}
	}
	return total, nil
}
