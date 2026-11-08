package sessions

import (
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Stalled-start protection. A native player can accept a decision, fetch
// segments faster than real time, and never render (#302: Plex for iOS on a
// Dolby Vision 8.1 copy). The server sees healthy delivery. Only the client's
// timeline shows that nothing plays. Without a guard the viewer waits on a
// spinner forever.
//
// The guard watches each transcode session from its first timeline. It fires
// once if, for startStallWindow after the client already held
// startStallAhead of media past its playhead, the reported position never
// advanced with the clock. A slow start cannot trigger it: until the media is
// delivered there is no evidence, and the window only counts afterwards.
// Pausing re-arms the window. Two seconds of real playback disarm the watch
// for the rest of that session.
//
// Plex cannot be told to restart a player, and a player that has parsed the
// stream header keeps it, so the session ends with a terminationText. A Dolby
// Vision copy with an HDR10 base is remembered for this viewer, client and
// title. The next start then delivers the base layer without
// another prompt. Anything else asks for another version.
const (
	startStallWindow = 20 * time.Second
	startStallAhead  = 30 * time.Second
	startPlayedOK    = 2 * time.Second
	// A client reports at least every ten seconds while playing. An offset
	// step larger than the wall clock plus this slack is a seek, not play.
	startStepSlack = 2 * time.Second

	startWatchMax = 256
	startWatchTTL = 10 * time.Minute
	dvBaseHintMax = 512
	dvBaseHintTTL = 24 * time.Hour
)

type startWatch struct {
	seen        time.Time // last timeline
	lastOffset  time.Duration
	played      time.Duration // reported position advanced with the clock
	deliveredAt time.Time     // first timeline holding startStallAhead of media
	done        bool          // disarmed after playback, or fired
}

type StartHintKey struct {
	UserID   int64
	ClientID string
	Key      core.RatingKey
}

// StartGuard holds bounded per-session watches and per-title fallbacks.
type StartGuard struct {
	mu      sync.Mutex
	watches map[string]*startWatch // transcode session id
	hints   map[StartHintKey]time.Time
}

// Observe records one timeline for transcode session tid. It returns true
// exactly once, when the session's start has stalled. ahead is the media the
// client has requested past its reported position.
func (g *StartGuard) Observe(tid, state string, offset, ahead time.Duration, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	w := g.watches[tid]
	if w == nil {
		if state == "stopped" {
			return false
		}
		if g.watches == nil {
			g.watches = map[string]*startWatch{}
		}
		g.expireLocked(now)
		if len(g.watches) >= startWatchMax {
			return false // fail open: no guard rather than unbounded state
		}
		w = &startWatch{seen: now, lastOffset: offset}
		g.watches[tid] = w
		return false
	}
	if state == "stopped" {
		delete(g.watches, tid)
		return false
	}
	step, wall := offset-w.lastOffset, now.Sub(w.seen)
	w.lastOffset, w.seen = offset, now
	if w.done {
		return false
	}
	if state == "paused" {
		w.deliveredAt = time.Time{}
		return false
	}
	if step > 0 && step <= wall+startStepSlack {
		w.played += step
	}
	if w.played >= startPlayedOK {
		w.done = true
		return false
	}
	if ahead < startStallAhead {
		w.deliveredAt = time.Time{}
		return false
	}
	if w.deliveredAt.IsZero() {
		w.deliveredAt = now
		return false
	}
	if now.Sub(w.deliveredAt) < startStallWindow {
		return false
	}
	w.done = true
	return true
}

func (g *StartGuard) Forget(tid string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.watches, tid)
}

func (g *StartGuard) Remember(k StartHintKey, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hints == nil {
		g.hints = map[StartHintKey]time.Time{}
	}
	g.expireLocked(now)
	if _, ok := g.hints[k]; !ok && len(g.hints) >= dvBaseHintMax {
		var oldest StartHintKey
		var at time.Time
		for hk, t := range g.hints {
			if at.IsZero() || t.Before(at) {
				oldest, at = hk, t
			}
		}
		delete(g.hints, oldest)
	}
	g.hints[k] = now
}

func (g *StartGuard) BaseOnly(k StartHintKey, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.hints[k]
	return ok && now.Sub(at) < dvBaseHintTTL
}

func (g *StartGuard) Expire(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expireLocked(now)
}

func (g *StartGuard) expireLocked(now time.Time) {
	for id, w := range g.watches {
		if now.Sub(w.seen) > startWatchTTL {
			delete(g.watches, id)
		}
	}
	for k, at := range g.hints {
		if now.Sub(at) >= dvBaseHintTTL {
			delete(g.hints, k)
		}
	}
}
