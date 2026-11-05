package playback

import (
	"context"
	"time"
)

// Called under Manager.mu. Rolling joins reuse saved resolution/probe/index
// metadata, hold no playing session, and admit at most two windows server-wide.
// A channel entry owns one operation; the engine's per-host caps and background
// priority remain authoritative. Bytes live in the existing bounded cache.
func (m *Manager) scheduleChannelWindowLocked(e *warmEntry, at time.Duration) bool {
	if at <= 0 || e.res == nil || e.probe == nil || e.windowRunning || m.channelWarmActive >= 2 || time.Since(e.windowAt) < 10*time.Second {
		return false
	}
	tm := TimeMap{Size: e.probe.info.Size, Duration: e.probe.info.Duration, Points: e.probe.points}
	lo, hi := warmSpanFor(tm, at, at+45*time.Second)
	n := min(hi-lo, int64(32<<20))
	if n <= 0 {
		return false
	}
	e.offset = at
	e.windowAt = time.Now()
	e.windowRunning = true
	m.channelWarmActive++
	m.wg.Add(1)
	res := *e.res
	go func() {
		defer m.wg.Done()
		defer func() { m.mu.Lock(); e.windowRunning = false; m.channelWarmActive--; m.mu.Unlock() }()
		ctx, cancel := context.WithTimeout(m.ctx, 15*time.Second)
		defer cancel()
		if err := m.deps.Engine.Prefetch(ctx, res.Release.ID, res.NZB, lo, n); err != nil && ctx.Err() == nil {
			m.log.Debug("channel join prefetch incomplete", "release", res.Release.ID, "offset", lo, "err", err)
		}
	}()
	return true
}
