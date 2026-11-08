package playback

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// snapshotForAdmission joins only the index task already owned by this
// generation. It performs no provider or database reads and does not restart
// indexing. Slow/unsupported indexes retain the ordinary unindexed fallback.
func (m *Manager) snapshotForAdmission(ctx context.Context, s *session, caps core.ClientCaps, started time.Time, prepareBudget time.Duration) (*core.PlaySession, core.SourceRetirement, error) {
	if caps.Protocol == core.ProtocolHLS {
		if g := s.current(); g != nil {
			info := g.infoSnapshot()
			if info.Video() != nil && len(info.Keyframes) == 0 && (info.Container == "mkv" || info.Container == "matroska" || info.Container == "mp4" || info.Container == "mov") {
				budget := 300 * time.Millisecond
				if prepareBudget > 0 {
					budget = min(budget, prepareBudget-time.Since(started))
				}
				if budget > 0 {
					timer := time.NewTimer(budget)
					select {
					case <-g.indexReady:
					case <-ctx.Done():
						timer.Stop()
						return nil, s.sources, ctx.Err()
					case <-timer.C:
					}
					timer.Stop()
				}
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, s.sources, err
	}
	return s.snapshot(m.now()), s.sources, nil
}
