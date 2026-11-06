package livetv

import (
	"context"
	"time"
)

func (s *Service) refreshTimelineGuide(ctx context.Context) error {
	s.virtualGuide.buildMu.Lock()
	defer s.virtualGuide.buildMu.Unlock()
	channels, _, err := s.channelSnapshot(ctx)
	if err != nil {
		return err
	}
	s.virtualGuide.mu.RLock()
	epoch, revision := s.virtualGuide.epoch, s.virtualGuide.revision
	s.virtualGuide.mu.RUnlock()
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
