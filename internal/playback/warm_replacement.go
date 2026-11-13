package playback

import (
	"context"
	"fmt"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// ReplaceWarmSource performs one normal resolution after a permanent warm
// failure. Nothing is remembered until the replacement's actual resume bytes
// have been read successfully. It runs inside the caller's bounded warm job.
func (m *Manager) ReplaceWarmSource(ctx context.Context, user *core.User, key core.RatingKey, caps core.ClientCaps, old core.Release, offset, duration time.Duration, failure error) error {
	if !permanentSourceFailure(failure) || ctx.Err() != nil {
		return failure
	}
	if user == nil {
		return core.ErrUnauthorized
	}
	id := identity{userID: user.ID, key: key, clientID: caps.ClientID}
	m.rememberOpenFailure(&session{ident: id}, old, failure)
	if err := m.deps.Resolver.MarkBad(ctx, old.ID, failure.Error()); err != nil {
		return err
	}
	title, err := m.deps.Catalog.Get(ctx, key)
	if err != nil {
		return err
	}
	if title == nil {
		return core.ErrNotFound
	}
	profile := m.selectProfile(caps, user, key)
	s := newSession(randomID(16), id, user, caps, title, profileKey(profile), m.now())
	s.originalLanguage = title.Language
	defer s.sources.seal()
	res, err := m.deps.Resolver.Resolve(core.WithIndexerPurpose(core.WithUnattendedSearch(ctx), core.IndexerPurposeWarmup), core.ResolveRequest{Title: title, User: user, Client: caps, Exclude: []string{old.ID}, PreferVideoCodec: old.Info.VideoCodec})
	if err != nil {
		return err
	}
	if res == nil || res.Release.ID == old.ID {
		return core.ErrNotAvailable
	}
	g, err := m.openGeneration(ctx, s, res, func(size int64) core.MediaInfo {
		info := EstimateInfo(title, caps, profile)
		info.Size = size
		return info
	})
	if err != nil {
		return err
	}
	defer m.dropGeneration(g)
	info := g.infoSnapshot()
	if info.Duration > 0 {
		duration = info.Duration
	}
	if duration <= 0 {
		return fmt.Errorf("replacement has no resume duration: %w", core.ErrNotAvailable)
	}
	tm := g.timeMapSnapshot()
	tm.Size, tm.Duration = g.src.Size(), duration
	start := max(int64(0), tm.KeyframeOffsetAt(offset)-(4<<20))
	end := tm.OffsetAt(min(duration, offset+25*time.Second))
	length := min(max(int64(0), end-start), int64(256<<20))
	if budget := m.config().Cache.BudgetBytes; budget > 0 {
		length = min(length, budget/10)
	}
	length = min(length, g.src.Size()-start)
	if length <= 0 {
		return core.ErrNotAvailable
	}
	if err := m.deps.Engine.Prefetch(ctx, res.Release.ID, res.NZB, start, length); err != nil {
		m.rememberOpenFailure(s, res.Release, err)
		if permanentSourceFailure(err) {
			m.markBad(res.Release.ID, err.Error())
		}
		return err
	}
	if m.resume != nil {
		rec := resumeRecord{userID: user.ID, key: key, release: res.Release, profile: profileKey(profile), originalLanguage: title.Language, size: g.src.Size(), duration: duration, updatedAt: m.now()}
		err = m.resume.putWithProbe(ctx, rec, res.NZB, keyframesOf(tm.Points), info)
		if err != nil {
			return err
		}
		var probe *probeCache
		if facts, ok := persistedProbeFacts(info); ok {
			probe = &probeCache{releaseID: res.Release.ID, info: facts, points: tm.Points, nzbHash: nzbHash(res.NZB), needsAugment: true}
		}
		m.resume.noteLive(rec, res.NZB, probe)
	}
	m.log.Info("resume source replaced before play", "key", key, "old_release", old.ID, "new_release", res.Release.ID, "reason", failure.Error())
	return nil
}
