package playback

import (
	"context"

	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// saveProbe persists probed facts next to a resume record, bound to the
// digest of the NZB payload they were probed from. Without a payload
// there is nothing to bind to, so nothing is stored.
func (s *resumeStore) saveProbe(ctx context.Context, userID int64, key core.RatingKey, releaseID string, nzb []byte, info core.MediaInfo, at time.Time) error {
	return saveProbeWith(ctx, s.db, userID, key, releaseID, nzb, info, at)
}

// loadProbe rebuilds the warm probeCache for a resume release: the
// persisted probe plus the persisted keyframe index. A row is a hit
// only for the exact NZB payload it was probed from (digest match), and
// probeGeneration additionally refuses the facts when the opened
// source's identity or size disagrees. Sidecar enrichment was stripped
// on save, so the rebuilt entry asks for AugmentMedia to run again
// (needsAugment).
func (s *resumeStore) loadProbe(ctx context.Context, userID int64, key core.RatingKey, releaseID string, nzb []byte) (*probeCache, error) {
	if len(nzb) == 0 {
		return nil, nil
	}
	return s.loadProbeHash(ctx, userID, key, releaseID, nzbHash(nzb))
}
