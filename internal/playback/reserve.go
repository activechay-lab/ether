package playback

import (
	"context"
	"slices"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

const playableReserve = 90 * time.Second

// swapLatencyWindow keeps only a bounded recent history. The target is a
// source-cache hint, never a statement about decoded or delivered media.
type swapLatencyWindow struct {
	mu      sync.Mutex
	samples [32]time.Duration
	next    int
}

func (w *swapLatencyWindow) observe(d time.Duration) {
	if d <= 0 {
		return
	}
	w.mu.Lock()
	w.samples[w.next] = d
	w.next = (w.next + 1) % len(w.samples)
	w.mu.Unlock()
}

func (w *swapLatencyWindow) target() time.Duration {
	w.mu.Lock()
	var slowest time.Duration
	for _, d := range w.samples {
		slowest = max(slowest, d)
	}
	w.mu.Unlock()
	// Two observed handoff durations plus 20s of jitter, bounded to the
	// same two-minute lead allowed by the default encoder policy.
	return min(2*time.Minute, max(playableReserve, 2*slowest+20*time.Second))
}

type reserveState struct {
	mu         sync.Mutex
	until      time.Duration
	generation *generation
	running    bool
	cancel     context.CancelFunc
}

func (r *reserveState) stop() {
	r.mu.Lock()
	if r.cancel != nil {
		r.cancel()
	}
	r.cancel = nil
	r.generation = nil
	r.running = false
	r.until = 0
	r.mu.Unlock()
}

type standbyState struct {
	mu       sync.Mutex
	started  bool
	ready    *generation
	consumed chan struct{}
	prepared chan struct{}
}

// Source-cache reserve, independent of the player's own buffering policy. All
// reads use Engine.Prefetch's background priority and are capped at 256 MiB;
// the media-sized window is not held in RAM. No startup waits on this.
func (m *Manager) maintainReserve(s *session, position time.Duration, state string) {
	if state != "playing" {
		if state == "paused" || state == "stopped" {
			s.reserve.stop()
		}
		return
	}
	version := m.reserveConfigVersion.Load()
	config := m.config()
	cfg := config.Cache
	if m.deps.Cache == nil || cfg.Mode == "off" || cfg.PlaybackReadAhead() == 0 {
		return
	}
	g := s.current()
	if g == nil {
		return
	}
	maxBytes := int64(256 << 20)
	if g.res != nil && (g.res.Release.Protocol == core.ProtocolTorrent || g.res.Release.Protocol == core.ProtocolHTTP) {
		if config.Debrid.ReadAheadChunks < 0 {
			return
		}
		if config.Debrid.ReadAheadChunks > 0 {
			chunk := config.Debrid.ChunkBytes
			if chunk <= 0 {
				chunk = 8 << 20
			}
			if chunk < maxBytes {
				maxBytes = min(maxBytes, chunk*min(int64(config.Debrid.ReadAheadChunks), maxBytes/chunk))
			}
		}
	}
	info := g.infoSnapshot()
	if info.Duration <= 0 {
		return
	}
	s.speedMu.Lock()
	rate, known := s.speed.rate(m.now())
	s.speedMu.Unlock()
	if known && rate < mediaBps(info, g.src.Size())*1.25 {
		return
	}
	reserve := min(m.swapLatency.target(), cfg.PlaybackReadAhead())
	tm := g.timeMapSnapshot()
	lo := tm.KeyframeOffsetAt(position)
	if len(tm.Points) == 0 {
		lo = tm.OffsetAt(max(0, position-6*time.Second))
	}
	// A byte cap can cover much less media time at high bitrates. Never
	// record the full requested horizon when the admitted bytes cannot reach
	// it, and do not spend the entire cap behind the current playhead.
	currentOffset := tm.OffsetAt(position)
	if currentOffset-lo >= maxBytes {
		lo = currentOffset
	}
	wantedUntil := min(info.Duration, position+reserve)
	hi := tm.OffsetAt(min(info.Duration, wantedUntil+4*time.Second))
	n := min(max(0, hi-lo), maxBytes)
	until := min(wantedUntil, tm.TimeAt(lo+n))
	if n <= 0 || until <= position {
		return
	}
	r := &s.reserve
	r.mu.Lock()
	if version != m.reserveConfigVersion.Load() {
		r.mu.Unlock()
		return
	}
	if r.generation == g && position >= r.until-reserve && position+30*time.Second <= r.until {
		r.mu.Unlock()
		return
	}
	if r.running && r.generation == g && position >= r.until-reserve && position < r.until {
		r.mu.Unlock()
		return
	}
	if r.cancel != nil {
		r.cancel()
	}
	ctx, cancel := context.WithTimeout(g.ctx, 45*time.Second)
	r.generation = g
	r.until = until
	r.running = true
	r.cancel = cancel
	r.mu.Unlock()
	if !g.acquire() {
		cancel()
		r.mu.Lock()
		if r.generation == g && r.until == until {
			r.running = false
			r.until = position
		}
		r.mu.Unlock()
		return
	}
	m.mu.Lock()
	if m.closed || m.reserveActive >= 2 {
		m.mu.Unlock()
		cancel()
		g.release()
		r.mu.Lock()
		if r.generation == g && r.until == until {
			r.running = false
			r.until = position
		}
		r.mu.Unlock()
		return
	}
	m.reserveActive++
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		defer func() { m.mu.Lock(); m.reserveActive--; m.mu.Unlock() }()
		defer cancel()
		defer g.release()
		// These are source-byte hints, not proof that output has been decoded
		// or received. RecoveryBuffer reports contiguous published segments.
		err := m.deps.Engine.Prefetch(ctx, g.releaseID(), g.res.NZB, lo, n)
		r.mu.Lock()
		if r.generation == g && r.until == until {
			r.running = false
			if err != nil {
				r.until = position
			}
		}
		r.mu.Unlock()
	}()
}

// Prepare one replacement while the current encoder and delivered buffer keep
// playing. Retain the opened/probed generation, not just its NZB. Ownership
// transfers once to failover; otherwise the parent/session cancellation closes
// it and revokes its loopback grant. Speculation never changes watch state.
func (m *Manager) prepareSpeedAlternate(g *generation) {
	if m.config().Cache.DisableSpeculation {
		return
	}
	d := &g.standby
	d.mu.Lock()
	if d.started {
		d.mu.Unlock()
		return
	}
	d.started = true
	d.consumed = make(chan struct{})
	d.prepared = make(chan struct{})
	d.mu.Unlock()
	m.mu.Lock()
	if m.closed || m.standbyActive >= 2 {
		m.mu.Unlock()
		d.mu.Lock()
		d.started = false
		close(d.prepared)
		d.mu.Unlock()
		return
	}
	m.standbyActive++
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		var signal sync.Once
		readySignal := d.prepared
		notify := func() { signal.Do(func() { close(readySignal) }) }
		defer notify()
		defer func() { m.mu.Lock(); m.standbyActive--; m.mu.Unlock() }()
		s := g.sess
		ctx, cancel := context.WithTimeout(g.ctx, 20*time.Second)
		stop := context.AfterFunc(s.stopCtx, cancel)
		defer stop()
		defer cancel()
		s.mu.Lock()
		tried := append(slices.Clone(s.tried), g.releaseID())
		alts := slices.Clone(s.alternates)
		s.mu.Unlock()
		alts = orderLowerBitrateFirst(alts, g.res.Release)
		started := time.Now()
		var prepared *generation
		for attempt := 0; attempt < min(m.opts.MaxAttempts, 3) && ctx.Err() == nil; attempt++ {
			res, err := m.nextResolution(ctx, s, tried, alts)
			if err != nil {
				break
			}
			tried = append(tried, res.Release.ID)
			next, err := m.openGeneration(ctx, s, res, func(size int64) core.MediaInfo { return core.MediaInfo{Size: size, Estimated: true} })
			if err != nil {
				continue
			}
			bps := mediaBps(next.infoSnapshot(), next.src.Size())
			if bps <= 0 || bps >= mediaBps(g.infoSnapshot(), g.src.Size()) || m.preflightSwap(ctx, s, next) != nil {
				m.dropGeneration(next)
				continue
			}
			// Read real upcoming bytes before calling an alternate ready. A
			// successful header probe does not establish that its first articles
			// or seek target exist (the Friends failure was at byte 1,433,600).
			if err := m.warmAlternateWindow(ctx, g, next); err != nil {
				m.log.Debug("alternate preparation failed", "session", s.id, "release", next.releaseID(), "err", err)
				if permanentSourceFailure(err) {
					m.markBad(next.releaseID(), "missing media while preparing alternate")
				}
				m.dropGeneration(next)
				continue
			}
			prepared = next
			break
		}
		if prepared == nil {
			return
		}
		if ctx.Err() != nil || s.current() != g {
			m.dropGeneration(prepared)
			return
		}
		d.mu.Lock()
		d.ready = prepared
		d.mu.Unlock()
		notify()
		m.log.Info("playback alternate prepared", "session", s.id, "release", prepared.releaseID(), "took", time.Since(started))
		select {
		case <-d.consumed:
			return
		case <-g.ctx.Done():
		case <-s.stopCtx.Done():
		}
		d.mu.Lock()
		abandoned := d.ready
		d.ready = nil
		d.mu.Unlock()
		if abandoned != nil {
			m.dropGeneration(abandoned)
		}
	}()
}

func (g *generation) takeSpeedAlternate(tried []string) *generation {
	d := &g.standby
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.ready == nil || slices.Contains(tried, d.ready.releaseID()) {
		return nil
	}
	next := d.ready
	d.ready = nil
	close(d.consumed)
	return next
}

// Buffer runway under a sustained deficit. Unknown buffer evidence leaves the
// legacy hysteresis intact; zero known runway calls for immediate recovery.
func bufferRunway(buffer time.Duration, ratio float64) time.Duration {
	if ratio >= 1 {
		return time.Hour
	}
	if ratio < 0 {
		ratio = 0
	}
	return time.Duration(float64(max(0, buffer)) / (1 - ratio))
}

// Track only time actually blocked inside live source reads. Encoder pauses,
// HTTP backpressure, probes and speculative reads are not network outages.
type pendingSourceReads struct {
	mu    sync.Mutex
	waits map[*time.Time]struct{}
}

func (p *pendingSourceReads) begin(now time.Time) func() {
	p.mu.Lock()
	if p.waits == nil {
		p.waits = make(map[*time.Time]struct{})
	}
	p.waits[&now] = struct{}{}
	p.mu.Unlock()
	return func() { p.mu.Lock(); delete(p.waits, &now); p.mu.Unlock() }
}
func (p *pendingSourceReads) longest(now time.Time) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	var elapsed time.Duration
	for start := range p.waits {
		elapsed = max(elapsed, now.Sub(*start))
	}
	return elapsed
}

func (m *Manager) warmAlternateWindow(ctx context.Context, current, next *generation) error {
	position, _, _ := m.repairHorizon(current, core.RepairEvent{Offset: -1})
	if hls, ok := m.deps.HLSLookup(current.sess.id); ok {
		if source, ok := hls.(core.RecoveryBufferSource); ok {
			b := source.RecoveryBuffer()
			if b.ReadyUntil > position && b.ReadyUntil < next.infoSnapshot().Duration {
				position = b.ReadyUntil
			}
		}
	}
	position = m.alignForFailover(ctx, current.sess, current, next, position)
	tm := next.timeMapSnapshot()
	lo := tm.KeyframeOffsetAt(max(0, position-4*time.Second))
	if len(tm.Points) == 0 {
		lo = tm.OffsetAt(max(0, position-8*time.Second))
	}
	if err := m.deps.Engine.Prefetch(ctx, next.releaseID(), next.res.NZB, 0, min(int64(2<<20), tm.Size)); err != nil {
		return err
	}
	return m.deps.Engine.Prefetch(ctx, next.releaseID(), next.res.NZB, lo, min(int64(4<<20), max(0, tm.Size-lo)))
}

// Spend at most a small fraction of proven runway joining work already in
// progress. Do not race a duplicate grab/probe while the standby warms bytes.
func (m *Manager) joinSpeedAlternate(ctx context.Context, g *generation, tried []string) *generation {
	if ready := g.takeSpeedAlternate(tried); ready != nil {
		return ready
	}
	_, buffer, known := m.repairHorizon(g, core.RepairEvent{Offset: -1})
	if !known || buffer < 4*time.Second {
		return nil
	}
	g.standby.mu.Lock()
	done := g.standby.prepared
	g.standby.mu.Unlock()
	if done == nil {
		return nil
	}
	timer := time.NewTimer(min(2*time.Second, buffer/4))
	defer timer.Stop()
	select {
	case <-done:
		return g.takeSpeedAlternate(tried)
	case <-ctx.Done():
		return nil
	case <-timer.C:
		return nil
	}
}
