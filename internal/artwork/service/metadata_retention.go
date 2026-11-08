package service

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Image metadata rows stay keyed per viewer (artworkMetadataKey): rendered
// discovery art and collection collages differ by viewer and policy, so a
// shared row could carry another viewer's personalised or restricted image.
// Instead each row records when it was last used, at most once a day, and
// rows unused for metadataRetention (superseded tags, departed viewers,
// images no longer shown) are pruned and recomputed if ever needed again.
func init() {
	// artwork_metadata is pinned by the Prism upgrade acceptance test, so
	// retention schema is its own component. Existing rows count as used now.
	const base = `ALTER TABLE artwork_image_metadata ADD COLUMN used_at INTEGER NOT NULL DEFAULT 0;
CREATE INDEX artwork_image_metadata_used_at ON artwork_image_metadata(used_at);
`
	store.Register("artwork_metadata_retention", 1, base+`UPDATE artwork_image_metadata SET used_at=CAST(strftime('%s','now') AS INTEGER);`)
	store.RegisterDialect("artwork_metadata_retention", 1, store.Postgres, store.TranslateDDL(base)+`UPDATE artwork_image_metadata SET used_at=CAST(EXTRACT(EPOCH FROM now()) AS BIGINT);`)
	store.RegisterAfter("artwork_metadata_retention", "artwork_metadata")
}

const (
	// metadataTouchEvery bounds used_at writes to one per row per day.
	metadataTouchEvery = 24 * time.Hour
	// metadataRetention is how long an unused row is kept.
	metadataRetention = 90 * 24 * time.Hour
)

// touch queues a used_at update for a row last touched over a day ago and
// marks m touched, reporting whether it did. It never blocks: a full queue
// drops the touch and the next use retries. The caller holds q.mu.
func (q *MetadataQueue) touch(key string, m *artworkMetadata) bool {
	now := time.Now().Unix()
	if now-m.usedAt < int64(metadataTouchEvery/time.Second) {
		return false
	}
	select {
	case q.touches <- key:
		m.usedAt = now
		return true
	default:
		return false
	}
}

func (q *MetadataQueue) runTouches() {
	defer q.workers.Done()
	for {
		select {
		case <-q.ctx.Done():
			return
		case key := <-q.touches:
			ctx, cancel := context.WithTimeout(q.ctx, 5*time.Second)
			now := time.Now().Unix()
			// Another node may have touched it today already.
			_, _ = q.db.ExecContext(ctx, `UPDATE artwork_image_metadata SET used_at=? WHERE image_key=? AND used_at<?`, now, key, now-int64(metadataTouchEvery/time.Second))
			cancel()
		}
	}
}

// Prune removes image metadata unused for metadataRetention and reports the
// rows removed.
func (q *MetadataQueue) Prune(ctx context.Context) (int64, error) {
	if q == nil || q.db == nil {
		return 0, nil
	}
	return store.PruneBatched(ctx, q.db, `DELETE FROM artwork_image_metadata WHERE image_key IN (SELECT image_key FROM artwork_image_metadata WHERE used_at<? ORDER BY used_at LIMIT ?)`, time.Now().Add(-metadataRetention).Unix())
}

// Prune removes stale image metadata (MetadataQueue.Prune).
func (a *Source) Prune(ctx context.Context) (int64, error) {
	if a == nil {
		return 0, nil
	}
	return a.meta.Prune(ctx)
}
