package plex

import (
	"context"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"github.com/braedonsaunders/ether/internal/transcode"
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
// title. The next start then delivers the base layer (hdr10BaseReason) without
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

	stallTextDV    = "This version couldn't start in Dolby Vision on this device. Play it again to watch it in HDR10, or choose another version, such as 1080p."
	stallTextOther = "This version can't play on this device. Choose another version, such as 1080p."
)

type startWatch struct {
	seen        time.Time // last timeline
	lastOffset  time.Duration
	played      time.Duration // reported position advanced with the clock
	deliveredAt time.Time     // first timeline holding startStallAhead of media
	done        bool          // disarmed after playback, or fired
}

type dvHintKey struct {
	userID   int64
	clientID string
	key      core.RatingKey
}

// startGuard holds bounded per-session watches and per-title fallbacks.
type startGuard struct {
	mu      sync.Mutex
	watches map[string]*startWatch // transcode session id
	hints   map[dvHintKey]time.Time
}

// stallSession is the live transcode session the guard reads.
type stallSession interface {
	core.PlaybackPosition
	Decision() core.Decision
	Info() core.MediaInfo
}

// observe records one timeline for transcode session tid. It returns true
// exactly once, when the session's start has stalled. ahead is the media the
// client has requested past its reported position.
func (g *startGuard) observe(tid, state string, offset, ahead time.Duration, now time.Time) bool {
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

func (g *startGuard) forget(tid string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.watches, tid)
}

func (g *startGuard) remember(k dvHintKey, now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hints == nil {
		g.hints = map[dvHintKey]time.Time{}
	}
	g.expireLocked(now)
	if _, ok := g.hints[k]; !ok && len(g.hints) >= dvBaseHintMax {
		var oldest dvHintKey
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

func (g *startGuard) baseOnly(k dvHintKey, now time.Time) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	at, ok := g.hints[k]
	return ok && now.Sub(at) < dvBaseHintTTL
}

func (g *startGuard) expire(now time.Time) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.expireLocked(now)
}

func (g *startGuard) expireLocked(now time.Time) {
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

// guardedStart reports whether stalled-start protection covers a session:
// Dolby Vision sources on any client, and Plex for iOS on any source. Other
// clients and sources keep their behaviour unchanged.
func guardedStart(pr *Request, v *core.StreamInfo) bool {
	if v != nil && v.DolbyVision {
		return true
	}
	return pr != nil && transcode.HasQuirk(pr.Product, pr.Platform, pr.Device, pr.Model, pr.Version, "plex-ios-hdr10-base")
}

// checkStartStall feeds one timeline to the guard. It returns the
// termination text when this client's start stalled, after remembering a
// Dolby Vision HDR10-base fallback for the next start.
func (s *Server) checkStartStall(pr *Request, key core.RatingKey, state string, offset time.Duration, tids []string) string {
	if s.d.Transcoder == nil || pr == nil || pr.User == nil {
		return ""
	}
	now := s.now()
	for _, id := range tids {
		e, ok := s.transcodes.get(id)
		if !ok || !e.started {
			continue
		}
		out, ok := s.plexHLSSession(id)
		if !ok {
			s.starts.forget(id)
			continue
		}
		live, ok := out.(stallSession)
		if !ok {
			observer, supported := out.(interface {
				Observe(context.Context) (*remotetranscode.ObservedSession, error)
			})
			if !supported {
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
			sample, err := observer.Observe(ctx)
			cancel()
			// No cached/zero fallback on errors, old samples or a different execution.
			if err != nil || sample == nil || sample.Identity().Session != id || sample.Identity().Generation == 0 || sample.RequestStarted().IsZero() || time.Since(sample.RequestStarted()) > 2*time.Second || time.Since(sample.RequestStarted()) < 0 {
				continue
			}
			live = sample
		}
		info := live.Info()
		v := info.Video()
		if !guardedStart(pr, v) {
			continue
		}
		base := max(e.offset, offset)
		if !s.starts.observe(id, state, offset, live.RequestedPosition()-base, now) {
			continue
		}
		d := live.Decision()
		s.log.Warn("playback start stalled: segments delivered but the player never advanced",
			"key", key, "client", pr.ClientID, "product", pr.Product, "platform", pr.Platform, "session", id,
			"position", offset, "requested", live.RequestedPosition(), "videoCopy", d.VideoCopy, "stripDolbyVision", d.StripDolbyVision)
		if v != nil && d.VideoCopy && v.DolbyVision && !d.StripDolbyVision && transcode.HasHDR10Base(v, e.caps) {
			s.starts.remember(dvHintKey{userID: pr.User.ID, clientID: pr.ClientID, key: key}, now)
			return stallTextDV
		}
		return stallTextOther
	}
	return ""
}

// dvBaseOnly reports a remembered Dolby Vision stall for this start.
func (s *Server) dvBaseOnly(pr *Request, key core.RatingKey) bool {
	return pr != nil && pr.User != nil && s.starts.baseOnly(dvHintKey{userID: pr.User.ID, clientID: pr.ClientID, key: key}, s.now())
}
