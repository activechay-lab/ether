package stream

import (
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Playback read-ahead keeps minutes of the title being played downloaded into
// the disk segment cache ahead of its reader, so a provider that stalls for
// tens of seconds does not stall the viewer. The in-memory read-ahead
// (source.go) covers seconds; this covers Engine.playAhead of media time,
// sized from the bitrate playback reports through SetPlaybackDuration.
//
// It runs one loop per playing source at core.PriorityPrefetch, the lane the
// connection pool caps at a share of each provider's connections and never
// grants while a viewer's or read-ahead request waits, so it uses spare
// capacity without delaying demand reads, first bytes or seeks. Fetched
// segments go to disk only (never the hot buffer). It is driven by demand
// reads: a reader that stops reading (pause, session end) stops it within one
// batch, and Close cancels it. All playing sources together keep at most a
// quarter of the cache's limit (budget or free-space floor) ahead.

const (
	// playMinConsumed is how much a reader must have read sequentially
	// before it counts as playback rather than a probe or an index read
	// (capped at playMinMedia of media for low bitrates).
	playMinConsumed = 16 << 20
	playMinMedia    = 15 * time.Second
	// playIdle stops read-ahead for a reader that has not read for this
	// long (a long pause or an abandoned seek position).
	playIdle = 3 * time.Minute
	// playBatch is the most fetched before the target is re-planned.
	playBatch = 64 << 20
	// playLowWater (or an eighth of a smaller depth) avoids a batch for
	// every article the reader consumes.
	playLowWater = 8 << 20
	// playCacheShare: all playing sources together hold at most
	// 1/playCacheShare of the cache limit ahead of their readers.
	playCacheShare = 4
	// playBackoff pauses a loop after a fetch error other than a missing
	// article, so a broken provider is not hammered.
	playBackoff = 10 * time.Second
)

// UpdatePlaybackReadAhead changes the media time playing sources keep
// downloaded ahead (0 = off). Running loops pick it up on their next plan.
func (e *Engine) UpdatePlaybackReadAhead(d time.Duration) {
	e.playAhead.Store(int64(max(d, 0)))
}

func (e *Engine) playbackReadAhead() time.Duration { return time.Duration(e.playAhead.Load()) }

// SetPlaybackDuration implements core.PlaybackDurationSetter: the source's
// average bitrate (size over running time) turns minutes into bytes.
func (s *source) SetPlaybackDuration(d time.Duration) {
	if d <= 0 || s.plan.size <= 0 {
		return
	}
	s.playRate.Store(max(int64(float64(s.plan.size)/d.Seconds()), 1))
	s.kickPlayAhead()
}

// kickPlayAhead wakes (starting once) the playback read-ahead loop.
func (s *source) kickPlayAhead() {
	if s.playRate.Load() <= 0 || s.e.cache == nil || s.e.playbackReadAhead() <= 0 {
		return
	}
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	if !s.playRunning {
		s.playRunning = true
		s.raWG.Add(1) // under mu with closed unset: Close has not started waiting
		go s.playAheadLoop()
	}
	s.mu.Unlock()
	select {
	case s.playWake <- struct{}{}:
	default:
	}
}

func (s *source) playAheadLoop() {
	defer s.raWG.Done()
	s.e.playSources.Store(s, struct{}{})
	defer s.e.playSources.Delete(s)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.playWake:
		}
		for {
			c, from, to, ok := s.playPlan()
			if !ok {
				break
			}
			parallel := max(1, min(s.e.opts.PrefetchParallel, s.raLimit()/2))
			err := s.e.prefetchRangeN(s.ctx, s.rel, s.plan, s.releaseID, from, to-from, parallel, &s.meter)
			if s.ctx.Err() != nil {
				return
			}
			// Holes are left to the reader: a missing article goes through
			// its repair, a failing provider is retried by demand reads.
			s.mu.Lock()
			c.playNext = max(c.playNext, to)
			s.mu.Unlock()
			if err != nil && !isMissing(err) {
				s.e.log.Debug("playback read-ahead failed", "name", s.plan.name, "err", err)
				t := time.NewTimer(playBackoff)
				select {
				case <-s.ctx.Done():
					t.Stop()
					return
				case <-t.C:
				}
			}
		}
	}
}

// playAheadBytes is the byte depth for one source: the configured media time
// at the source's bitrate, within its share of the cache limit.
func (s *source) playAheadBytes() int64 {
	d, rate := s.e.playbackReadAhead(), s.playRate.Load()
	if d <= 0 || rate <= 0 || s.e.cache == nil {
		return 0
	}
	want := int64(d.Seconds() * float64(rate))
	if l, ok := s.e.cache.(core.SegmentCacheLimit); ok {
		switch limit := l.UsageLimit(); {
		case limit == 0:
			return 0
		case limit > 0:
			want = min(want, limit/(playCacheShare*max(1, s.e.activePlayers())))
		}
	}
	return want
}

// activePlayers counts the sources sharing the read-ahead allowance: those
// with a demand read within playIdle. A paused viewer's source keeps its loop
// (and its session) but stops shrinking the depth of viewers still playing,
// and counts again as soon as it reads.
func (e *Engine) activePlayers() int64 {
	cutoff := time.Now().Add(-playIdle).UnixNano()
	var n int64
	e.playSources.Range(func(k, _ any) bool {
		if k.(*source).lastRead.Load() > cutoff {
			n++
		}
		return true
	})
	return n
}

// playPlan picks the reader to read ahead of (the most recently active one
// that is playing) and the next batch [from, to) for it.
func (s *source) playPlan() (c *cursor, from, to int64, ok bool) {
	depth := s.playAheadBytes()
	if depth <= 0 {
		return nil, 0, 0, false
	}
	minConsumed := min(int64(playMinConsumed), max(int64(playMinMedia.Seconds()*float64(s.playRate.Load())), 1))
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, 0, 0, false
	}
	for _, cur := range s.cursors {
		if cur.consumed >= minConsumed && now.Sub(cur.used) < playIdle && (c == nil || cur.used.After(c.used)) {
			c = cur
		}
	}
	if c == nil {
		return nil, 0, 0, false
	}
	// The in-memory window right after the reader is the near read-ahead's.
	from = max(c.playNext, c.next+c.window)
	to = min(c.next+depth, s.plan.size)
	if to-from < min(playLowWater, depth/8) && to < s.plan.size || from >= to {
		return nil, 0, 0, false
	}
	return c, from, min(to, from+playBatch), true
}
