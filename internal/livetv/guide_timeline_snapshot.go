package livetv

import (
	"context"
	"time"
)

func (s *Service) refreshTimelineGuide(ctx context.Context) error {
	s.virtualGuide.buildMu.Lock()
	defer s.virtualGuide.buildMu.Unlock()
	var epoch int64
	if err := s.o.DB.QueryRowContext(ctx, `SELECT generation FROM livetv_channel_epoch WHERE id=1`).Scan(&epoch); err != nil {
		return err
	}
	channels, _, err := s.channelSnapshot(ctx)
	if err != nil {
		return err
	}
	s.virtualGuide.mu.Lock()
	if epoch > s.virtualGuide.epoch {
		s.virtualGuide.epoch = epoch
	}
	revision := s.virtualGuide.revision
	s.virtualGuide.mu.Unlock()
	return s.refreshTimelineSnapshots(ctx, channels, s.o.Now(), epoch, revision)
}

// Timeline warming runs after every channel's eight-day guide is published.
// One worker and the existing compact compile cache bound transient expansion;
// timeline reads only slice these tables and never expand a schedule.
func (s *Service) refreshTimelineSnapshots(ctx context.Context, channels []Channel, now time.Time, epoch int64, revision uint64) error {
	compiled := &guideCompileCache{}
	seen := map[string]bool{}
	for _, c := range channels {
		if c.Kind != "virtual" || c.Personal != nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		seen[c.ID] = true
		s.ensureChannelReels(ctx, c)
		recipe := compiled.fingerprint(s, c, revision, now)
		s.virtualGuide.mu.RLock()
		prior := s.virtualGuide.timeline[c.ID]
		s.virtualGuide.mu.RUnlock()
		var entry *virtualGuideSnapshot
		if prior != nil && prior.recipe == recipe && !now.Before(prior.built) && !prior.until.Before(now.Add(28*24*time.Hour)) {
			updated := *prior
			updated.channel = c
			updated.playbackChannel = refreshSnapshotMetadata(prior.playbackChannel, c)
			entry = &updated
			if shareableGuide(c) {
				compiled.remember(recipe, entry)
			}
		} else {
			var err error
			entry, err = s.buildVirtualSnapshot(ctx, c, now, revision, compiled, true)
			if err != nil {
				return err
			}
		}
		published := *entry
		published.epoch = epoch
		s.virtualGuide.mu.Lock()
		if s.virtualGuide.timeline == nil {
			s.virtualGuide.timeline = map[string]*virtualGuideSnapshot{}
		}
		s.virtualGuide.timeline[c.ID] = &published
		s.virtualGuide.mu.Unlock()
	}
	s.virtualGuide.mu.Lock()
	for id := range s.virtualGuide.timeline {
		if !seen[id] {
			delete(s.virtualGuide.timeline, id)
		}
	}
	s.virtualGuide.mu.Unlock()
	return nil
}
