package notify

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func init() {
	// viewer_alerts is pinned by the delivered-main boundary, so retention
	// schema is its own component. Existing seen rows count as seen now:
	// a zero would let the first prune drop rows a baseline scan just wrote.
	const base = `ALTER TABLE viewer_episode_seen ADD COLUMN seen_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX viewer_episode_seen_seen_at ON viewer_episode_seen(seen_at);
CREATE INDEX viewer_episode_alerts_detected_at ON viewer_episode_alerts(detected_at);
`
	store.Register("viewer_alerts_retention", 1, base+`UPDATE viewer_episode_seen SET seen_at=CAST(strftime('%s','now') AS INTEGER);`)
	store.RegisterDialect("viewer_alerts_retention", 1, store.Postgres, store.TranslateDDL(base)+`UPDATE viewer_episode_seen SET seen_at=CAST(EXTRACT(EPOCH FROM now()) AS BIGINT);`)
	store.RegisterAfter("viewer_alerts_retention", "viewer_alerts")
}

const (
	// episodeAlertWindow: the detector alerts only episodes released this
	// recently, and records seen rows only for them.
	episodeAlertWindow = 48 * time.Hour
	// viewerAlertRetention outlasts ViewerPush.Drain's one-hour delivery
	// window and its 48-hour claims.
	viewerAlertRetention = 48 * time.Hour
	// viewerSeenRetention outlasts episodeAlertWindow with margin: a seen
	// row is written no earlier than its episode's release, so once it is
	// this old the episode is outside the window and seeing it again can
	// never alert.
	viewerSeenRetention = 7 * 24 * time.Hour
)

// Prune removes favourite-episode alert rows that can no longer be
// delivered and seen rows whose episodes can no longer alert, and reports
// the rows removed. notify_once keys are not aged out here: each guards an
// event its caller may still raise (an update version a downgraded server is
// offered again; watchlist episode keys are dropped by the watchlist prune).
func (s *Service) Prune(ctx context.Context) (int64, error) {
	if s == nil || s.db == nil {
		return 0, nil
	}
	now := s.opts.Now()
	n, err := store.PruneBatched(ctx, s.db, `DELETE FROM viewer_episode_alerts WHERE (viewer_id,episode_key) IN (SELECT viewer_id,episode_key FROM viewer_episode_alerts WHERE detected_at<? ORDER BY detected_at LIMIT ?)`, now.Add(-viewerAlertRetention).Unix())
	if err != nil {
		return n, err
	}
	m, err := store.PruneBatched(ctx, s.db, `DELETE FROM viewer_episode_seen WHERE (viewer_id,episode_key) IN (SELECT viewer_id,episode_key FROM viewer_episode_seen WHERE seen_at<? ORDER BY seen_at LIMIT ?)`, now.Add(-viewerSeenRetention).Unix())
	return n + m, err
}
