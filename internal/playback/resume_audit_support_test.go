package playback

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
)

// put stores a record and its keyframe index: at once, or — on a clustered
// node that cannot reach the database — when the database is back. The
// NZB is written to local disk first either way.
func (s *resumeStore) put(ctx context.Context, rec resumeRecord, nzb []byte, points []core.KeyframePoint) error {
	return s.putWithProbe(ctx, rec, nzb, points, core.MediaInfo{})
}
func (s *resumeStore) liveProbe(userID int64, key core.RatingKey, releaseID string, nzb []byte) *probeCache {
	hash := ""
	if len(nzb) > 0 {
		hash = nzbHash(nzb)
	}
	return s.liveProbeHash(userID, key, releaseID, hash)
}
