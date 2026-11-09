package playback

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Speed-coupled down-failover (#28): when the bytes of a release arrive
// from Usenet slower than the media itself plays, the client will starve.
// Swap to a lower-bitrate candidate before that happens. The slow release
// is not broken, only slow, so it is excluded for this session only and
// never marked bad.

const speedWindow = 20 // seconds of fetch evidence considered

// speedTracker measures effective source-read delivery, including cached bytes.
// It is NOT fresh network throughput: play-ahead is reflected when its bytes
// are consumed, and idle time between reads is excluded. Never teach provider
// capacity from this demand-driven estimate. Evidence ages out per second.
type speedTracker struct {
	bytes [speedWindow + 1]int64
	durNs [speedWindow + 1]int64
	secs  [speedWindow + 1]int64
	reads [speedWindow + 1]int64
}

func (t *speedTracker) add(n int64, dur time.Duration, now time.Time) {
	sec := now.Unix()
	i := int(sec % int64(len(t.bytes)))
	if i < 0 {
		i = -i
	}
	if t.secs[i] != sec {
		t.secs[i], t.bytes[i], t.durNs[i] = sec, 0, 0
		t.reads[i] = 0
	}
	t.reads[i]++
	t.bytes[i] += n
	t.durNs[i] += dur.Nanoseconds()
}

func (t *speedTracker) reset() { *t = speedTracker{} }

// rate requires several reads and at least a second of read time; a single
// cold read after a start or seek cannot establish sustained delivery.
func (t *speedTracker) rate(now time.Time) (float64, bool) {
	sec := now.Unix()
	var bytes, durNs, reads int64
	for i := range t.bytes {
		if d := sec - t.secs[i]; d >= 0 && d <= speedWindow {
			reads += t.reads[i]
			bytes += t.bytes[i]
			durNs += t.durNs[i]
		}
	}
	if reads < 4 || bytes < 256<<10 || durNs < int64(time.Second) {
		return 0, false
	}
	return float64(bytes) / (float64(durNs) / float64(time.Second)), true
}

// mediaBps is the playback rate of a generation in bytes per second.
func mediaBps(info core.MediaInfo, size int64) float64 {
	if info.BitrateKbps > 0 {
		return float64(info.BitrateKbps) * 1000 / 8
	}
	if size > 0 && info.Duration > 0 {
		return float64(size) / info.Duration.Seconds()
	}
	return 0
}

// noteFetch records all source reads. Dropping fast reads biases delivery
// downward by discarding the bytes that read-ahead successfully supplied.
func (s *session) noteFetch(n int, elapsed time.Duration, now time.Time) {
	if n <= 0 || elapsed <= 0 {
		return
	}
	s.speedMu.Lock()
	s.speed.add(int64(n), elapsed, now)
	s.speedMu.Unlock()
}

// noteReadPos tracks the read position for seek detection and for the
// timestamp a down-failover resumes at. A jump restarts the grace period:
// a seek always looks slow for a moment.
func (s *session) noteReadPos(off int64, n int, now time.Time, grace time.Duration) {
	s.speedMu.Lock()
	defer s.speedMu.Unlock()
	if s.expectPos >= 0 && off != s.expectPos {
		s.graceUntil = now.Add(grace)
		s.slowChecks = 0
		s.speed.reset()
	}
	// A tail/index probe or a previous forward seek is not the playhead.
	// Following it with an earlier media read must move this position back.
	s.lastSrcPos = off + int64(n)
	s.expectPos = off + int64(n)
}

// resetSpeed restarts speed evidence for a new generation.
func (s *session) resetSpeed(now time.Time, grace time.Duration) {
	s.speedMu.Lock()
	s.speed.reset()
	s.genSince = now
	s.graceUntil = now.Add(grace)
	s.slowChecks = 0
	s.lastSrcPos = 0
	s.expectPos = -1
	s.speedMu.Unlock()
}

// speedLoop periodically evaluates every session's fetch rate.
func (m *Manager) speedLoop() {
	t := time.NewTicker(m.opts.SpeedCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.checkSpeeds()
		}
	}
}

// checkSpeeds runs one down-failover evaluation over all live sessions.
func (m *Manager) checkSpeeds() {
	now := m.now()
	m.mu.Lock()
	list := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		list = append(list, s)
	}
	m.mu.Unlock()
	for _, s := range list {
		m.checkSpeed(s, now)
	}
}

func (m *Manager) checkSpeed(s *session, now time.Time) {
	g := s.current()
	if g == nil || s.isClosed() {
		return
	}
	s.mu.Lock()
	decision, downs, misses, viewerState := s.decision, s.downFailovers, s.downMisses, s.viewerState
	s.mu.Unlock()
	if viewerState == "paused" || viewerState == "stopped" {
		s.mu.Lock()
		s.encoderRecoveryReason, s.encoderRunway = "", nil
		s.mu.Unlock()
		s.speedMu.Lock()
		s.encoderSlowChecks = 0
		s.speedMu.Unlock()
		return
	}
	hls, hasHLS := m.deps.HLSLookup(s.id)
	if hasHLS && hls != nil && m.checkEncoderSpeed(s, g, hls, time.Now()) {
		return // encoder-bound output is not evidence against the provider
	}
	if downs+misses >= m.opts.MaxDownFailovers {
		return
	}
	if retry, ok := g.src.(core.DirectPlayRetrier); ok && retry.RetryDirectPlay() && !hasHLS && decision == core.DecisionDirectPlay {
		// Byte-range players do not renegotiate when delivery slows. Keep
		// their representation intact; bitrate adaptation belongs to an
		// actual HLS/DASH session, where SwapInput can restart the muxer.
		return
	}
	// Safe swap paths only: an HLS/DASH session restarts ffmpeg at the
	// mapped time; a direct-play session goes through the #5 logic
	// (exact-size splice, TS splice, or a switch to remux).
	if !hasHLS && decision != core.DecisionDirectPlay {
		return
	}
	info := g.infoSnapshot()
	media := mediaBps(info, g.src.Size())
	if media <= 0 {
		return
	}

	blocked := g.reads.longest(now)
	s.speedMu.Lock()
	fetch, ok := s.speed.rate(now)
	age := now.Sub(s.genSince)
	grace := now.Before(s.graceUntil) || age < m.opts.SpeedGrace
	// A source that stopped delivering bytes cannot produce completed-read
	// samples. A live blocked read is evidence; a quiet/paused encoder is not.
	if blocked >= 6*time.Second {
		fetch, ok = 0, true
	}
	if grace {
		s.slowChecks = 0
		s.speedMu.Unlock()
		return
	}
	if !ok {
		s.speedMu.Unlock()
		return
	}
	ratio := fetch / media
	if ratio < 1 {
		s.slowChecks++
	} else {
		s.slowChecks = 0
	}
	slow := s.slowChecks
	at := g.timeMapSnapshot().TimeAt(s.lastSrcPos)
	s.speedMu.Unlock()
	// Begin bounded preparation before the buffer is empty. A source slightly
	// faster than average still needs headroom for bursts, but never switch it
	// solely for lacking that headroom.
	if hasHLS && !grace && ratio < 1.2 {
		m.prepareSpeedAlternate(g)
	}
	_, buffer, bufferKnown := m.repairHorizon(g, core.RepairEvent{Offset: -1})
	// A comfortable buffer is direct evidence that recovery can wait, even
	// when completed source reads look slow. Apply this to every path.
	if bufferKnown && bufferRunway(buffer, ratio) > 25*time.Second {
		return
	}
	urgent := hasHLS && !grace && bufferKnown && slow >= 2 && ratio < 1 && bufferRunway(buffer, ratio) <= 25*time.Second
	if !urgent && (grace || ratio >= m.opts.SpeedThreshold || slow < m.opts.SpeedSlowChecks) {
		return
	}
	if position, ok := hls.(core.PlaybackPosition); ok {
		at = position.RequestedPosition()
	}
	s.speedMu.Lock()
	s.slowChecks = 0
	s.speedMu.Unlock()
	m.log.Warn("source slower than playback; down-failing over",
		"session", s.id, "key", s.ident.key, "release", g.res.Release.Name,
		"sourceReadBps", int64(fetch), "mediaBps", int64(media), "at", at,
		"buffer", buffer, "bufferKnown", bufferKnown, "runway", bufferRunway(buffer, ratio), "blockedRead", blocked)
	m.backgroundDownFailover(s, g, at)
}

// noteFailedDownFailover bounds unsuccessful adaptation attempts without
// stopping a source that is still delivering. Slow is not broken.
func (m *Manager) noteFailedDownFailover(s *session, tried []string) {
	if m.opts.MaxDownFailovers < 0 || s == nil {
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	for _, id := range tried {
		if id != "" && !slices.Contains(s.tried, id) {
			s.tried = append(s.tried, id)
		}
	}
	s.downMisses++
	s.mu.Unlock()
}

// backgroundDownFailover runs a speed-triggered failover off the monitor.
func (m *Manager) backgroundDownFailover(s *session, g *generation, at time.Duration) {
	if !g.backgroundFailover.CompareAndSwap(false, true) {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		g.backgroundFailover.Store(false)
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		defer g.backgroundFailover.Store(false)
		ctx, cancel := context.WithTimeout(m.ctx, m.opts.FailoverTimeout)
		defer cancel()
		// Stopping the session ends the swap; retiring g does not.
		if s.stopCtx != nil {
			stop := context.AfterFunc(s.stopCtx, cancel)
			defer stop()
		}
		if _, err := m.failoverWith(ctx, s, g, at, failoverOpts{
			reason:       "source slower than playback",
			followPlayer: true, preserveBuffer: true,
			down: true,
		}); err != nil && ctx.Err() == nil && !errors.Is(err, errRequireRemux) {
			m.log.Error("down-failover failed", "session", s.id, "err", err)
		}
	}()
}
