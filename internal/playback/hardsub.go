package playback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
	"time"
)

type subtitleMemory interface {
	SubtitleVerdict(context.Context, string) (bool, bool)
	RememberSubtitleVerdict(context.Context, string, bool) error
}

func (m *Manager) generationHardSubbed(ctx context.Context, g *generation) bool {
	if g.res == nil {
		return false
	}
	r := g.res.Release
	if r.Info.HardSubbed || releaseparse.HardSubbed(r.Name) {
		return true
	}
	mem, ok := m.deps.Resolver.(subtitleMemory)
	if !ok {
		return false
	}
	if v, known := mem.SubtitleVerdict(ctx, r.ID); known {
		return v
	}
	detector, ok := m.deps.Transcoder.(interface {
		DetectHardSubs(context.Context, string, time.Duration) (bool, bool)
	})
	if !ok {
		return false
	}
	v, known := detector.DetectHardSubs(ctx, m.urlFor(g), g.infoSnapshot().Duration)
	if known {
		_ = mem.RememberSubtitleVerdict(ctx, r.ID, v)
	}
	return known && v
}

// RejectRelease records the exact source chosen for a deliberately removed item.
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
	r.Info.HardSubbed = true
	p, ok := m.deps.Resolver.(interface {
		HardSubtitleBlocked(context.Context, core.Release) bool
	})
	return ok && p.HardSubtitleBlocked(ctx, r)
}

func (m *Manager) titleReleaseRejected(ctx context.Context, key core.RatingKey, r core.Release) bool {
	checker, ok := m.deps.Resolver.(interface {
		TitleReleaseRejected(context.Context, core.RatingKey, core.Release) bool
	})
	return ok && checker.TitleReleaseRejected(ctx, key, r)
}

// RejectTitleRelease handles removal of the selected Plex queue item. Unplayed
// items have no selected source and create no rejection.
func (m *Manager) RejectTitleRelease(ctx context.Context, userID int64, key core.RatingKey) error {
	for _, ps := range m.Sessions() {
		if ps.UserID == userID && ps.Key == key && ps.Resolution != nil {
			return m.RejectRelease(ctx, key, ps.Resolution.Release)
		}
	}
	if m.resume == nil {
		return nil
	}
	rec, err := m.resume.get(ctx, userID, key)
	if err != nil {
		return err
	}
	if rec == nil {
		return nil
	}
	return m.RejectRelease(ctx, key, rec.release)
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
