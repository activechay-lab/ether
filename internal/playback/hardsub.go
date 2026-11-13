package playback

import (
	"context"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

type subtitleMemory interface {
	SubtitleVerdict(context.Context, string) (bool, bool)
	RememberSubtitleVerdict(context.Context, string, bool) error
}

// knownHardSubbed never starts a decoder. Startup only reads existing evidence.
func (m *Manager) knownHardSubbed(ctx context.Context, g *generation) bool {
	if g.res == nil {
		return false
	}
	m.cfgMu.RLock()
	allow := m.cfg.Resolver.HardSubtitles == "allow"
	m.cfgMu.RUnlock()
	if allow {
		return false
	}
	r := g.res.Release
	if releaseparse.HardSubbed(r.Name) {
		return true
	}
	mem, ok := m.deps.Resolver.(subtitleMemory)
	if !ok {
		return false
	}
	v, _ := mem.SubtitleVerdict(ctx, r.ID)
	return v
}

// startSubtitleCheck is called only after client telemetry confirms a picture
// and ten seconds of playback. No startup, range writer or HLS encoder waits
// for this worker. The generation hold keeps the loopback URL valid.
func (m *Manager) startSubtitleCheck(s *session) {
	s.mu.Lock()
	ready := s.viewerState == "playing" && !s.exp.pictureAt.IsZero() && m.now().Sub(s.exp.pictureAt) >= 10*time.Second
	g := s.cur
	s.mu.Unlock()
	if !ready || g == nil || g.res == nil {
		return
	}
	m.mu.Lock()
	if m.closed || m.subtitleRunning || m.subtitleScheduled[g.res.Release.ID] {
		m.mu.Unlock()
		return
	}
	if !g.acquire() {
		m.mu.Unlock()
		return
	}
	if m.subtitleScheduled == nil {
		m.subtitleScheduled = map[string]bool{}
	}
	if len(m.subtitleScheduled) >= 10000 {
		clear(m.subtitleScheduled)
	}
	m.subtitleScheduled[g.res.Release.ID] = true
	m.subtitleRunning = true
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer g.release()
		defer m.wg.Done()
		defer func() { m.mu.Lock(); m.subtitleRunning = false; m.mu.Unlock() }()
		mem, ok := m.deps.Resolver.(subtitleMemory)
		if !ok {
			return
		}
		detector, ok := m.deps.Transcoder.(interface {
			DetectHardSubs(context.Context, string, time.Duration) (bool, bool)
		})
		if !ok {
			return
		}
		ctx, cancel := context.WithTimeout(g.ctx, 8*time.Second)
		defer cancel()
		r := g.res.Release
		if _, known := mem.SubtitleVerdict(ctx, r.ID); known || releaseparse.HardSubbed(r.Name) {
			return
		}
		claims, ok := m.deps.Resolver.(interface {
			ClaimSubtitleCheck(context.Context, string) (bool, error)
		})
		if !ok {
			return
		}
		claimed, err := claims.ClaimSubtitleCheck(ctx, r.ID)
		if err != nil || !claimed {
			return
		}
		v, known := detector.DetectHardSubs(ctx, m.urlFor(g)+"?background=1", g.infoSnapshot().Duration)
		if known {
			_ = mem.RememberSubtitleVerdict(ctx, r.ID, v)
		}
	}()
}

// RejectRelease records a deliberate bad-release report, never housekeeping.
func (m *Manager) RejectRelease(ctx context.Context, key core.RatingKey, r core.Release) error {
	reject, ok := m.deps.Resolver.(interface {
		RejectRelease(context.Context, core.RatingKey, core.Release) error
	})
	if !ok {
		return nil
	}
	return reject.RejectRelease(ctx, key, r)
}
func (m *Manager) hardSubtitleBlocked(ctx context.Context, r core.Release) bool {
	p, ok := m.deps.Resolver.(interface {
		HardSubtitleBlocked(context.Context, core.Release) bool
	})
	return ok && p.HardSubtitleBlocked(ctx, r)
}

func (m *Manager) titleReleaseRejected(ctx context.Context, key core.RatingKey, r core.Release) bool {
	m.mu.Lock()
	failed := m.openFailures[key][r.ID]
	m.mu.Unlock()
	if failed {
		return true
	}
	checker, ok := m.deps.Resolver.(interface {
		TitleReleaseRejected(context.Context, core.RatingKey, core.Release) bool
	})
	return ok && checker.TitleReleaseRejected(ctx, key, r)
}

// RejectReleaseID retains all known title associations for an owner's cache
// removal even when the resolver's recent search descriptors have expired.
func (m *Manager) RejectReleaseID(ctx context.Context, id string) error {
	if m.resume == nil {
		return nil
	}
	records, err := m.resume.all(ctx)
	if err != nil {
		return err
	}
	for _, rec := range records {
		if rec.release.ID == id {
			if err = m.RejectRelease(ctx, rec.key, rec.release); err != nil {
				return err
			}
		}
	}
	return nil
}
