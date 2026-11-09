package stream

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Per-source delivery accounting and adaptive read-ahead concurrency (#385).
//
// Adaptation uses decoded fetch completion rate, including pool wait. It is a
// scheduling hint, not measured socket speed. Provider pools remain authoritative
// for priority, connection limits and fairness; source budgets bound queued work.

const (
	// adaptiveHeadroom is the delivery target relative to the media bitrate.
	adaptiveHeadroom = 1.5
	// adaptiveMargin covers a fetch or two stuck on a slow article.
	adaptiveMargin = 2
	// maxAdaptiveParallel bounds one source's read-ahead fetches whatever
	// the account allows.
	maxAdaptiveParallel = 64
	// streamConnShare sizes speculative work against primary capacity.
	// A minimum one fetch per reader preserves progress on tiny pools; the
	// provider scheduler, not this estimate, reserves actual sockets.
	streamConnShare = 0.75
	// activeStreamWindow: a source that read this recently is playing.
	activeStreamWindow = 30 * time.Second
	// perFetchAlpha weighs the newest per-fetch throughput sample.
	perFetchAlpha = 0.2
)

// deliveryMeter measures one source's network delivery on the wall clock.
type deliveryMeter struct {
	owner                 *source // immutable after construction
	budgetMu              sync.Mutex
	speculative, prefetch int
	changed               chan struct{}
	mu                    sync.Mutex
	inflight              int
	since                 time.Time // when inflight last rose from zero
	busy                  time.Duration
	bytes                 int64
	fetches               int64
	perFetch              float64 // EWMA bytes per second of one demand or read-ahead fetch
}

// begin marks a network fetch outstanding and returns its start time.
func (m *deliveryMeter) begin() time.Time {
	now := time.Now()
	if m == nil {
		return now
	}
	m.mu.Lock()
	now = time.Now()
	if m.inflight == 0 {
		m.since = now
	}
	m.inflight++
	m.mu.Unlock()
	return now
}

// end completes a fetch begun at start. n bytes landed when ok. Background
// prefetch fetches wait behind every viewer's work for a slot, so their
// duration says nothing about one connection's speed; they still count as
// delivery.
func (m *deliveryMeter) end(start time.Time, n int, ok bool, prio core.Priority) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	m.inflight--
	if m.inflight <= 0 {
		m.inflight = 0
		m.busy += now.Sub(m.since)
	}
	if !ok || n <= 0 {
		return
	}
	m.bytes += int64(n)
	m.fetches++
	if d := now.Sub(start); d > 0 && prio != core.PriorityPrefetch {
		r := float64(n) / d.Seconds()
		if m.perFetch == 0 {
			m.perFetch = r
		} else {
			m.perFetch += perFetchAlpha * (r - m.perFetch)
		}
	}
}

func (m *deliveryMeter) perFetchBps() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.perFetch
}

func (m *deliveryMeter) snapshot() core.SourceDelivery {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	busy := m.busy
	if m.inflight > 0 {
		busy += now.Sub(m.since)
	}
	return core.SourceDelivery{Bytes: m.bytes, Fetches: m.fetches, Busy: busy, InFlight: m.inflight, PerFetchBps: m.perFetch}
}

// SourceDelivery implements core.SourceDeliveryReporter.
func (s *source) SourceDelivery() core.SourceDelivery {
	d := s.meter.snapshot()
	d.Parallel = s.raLimit()
	if p, ok := s.e.fetcher.(interface{ PipelineDepth() int }); ok {
		d.PipelineDepth = p.PipelineDepth()
	}
	return d
}

// raLimit is how many read-ahead fetches this source may run now: enough to
// deliver adaptiveHeadroom times the media bitrate at the measured per-fetch
// throughput, bounded by provider capacity even when that falls below the configured base.
func (s *source) raLimit() int {
	base := s.e.opts.ReadAheadParallel
	rate := s.playRate.Load()
	per := s.meter.perFetchBps()
	if rate <= 0 || per <= 0 {
		return adaptiveParallel(base, base, s.e.connectionCapacity(), s.e.activeStreams())
	}
	want := int(math.Min(maxAdaptiveParallel, math.Ceil(adaptiveHeadroom*float64(rate)/per))) + adaptiveMargin
	return adaptiveParallel(base, want, s.e.connectionCapacity(), s.e.activeStreams())
}

// adaptiveParallel bounds a wanted concurrency by the base floor, the hard
// ceiling and, when the providers' capacity is known, an equal share of it
// across the playing streams.
func adaptiveParallel(base, want, capacity int, streams int64) int {
	base = max(1, min(base, maxAdaptiveParallel))
	ceiling := maxAdaptiveParallel
	if capacity <= 0 {
		return base
	}
	if capacity > 0 {
		share := int(float64(capacity) * streamConnShare / float64(max(1, streams)))
		ceiling = min(ceiling, max(1, share))
	}
	return min(max(base, want), ceiling)
}

// connectionCapacity is the providers' total connection limit on this node,
// or 0 when the fetcher does not say.
func (e *Engine) connectionCapacity() int {
	if c, ok := e.fetcher.(interface{ MaxConnections() int }); ok {
		return c.MaxConnections()
	}
	return 0
}

// activeStreams counts recent readers and sources with outstanding fetches.
func (e *Engine) activeStreams() int64 {
	cutoff := time.Now().Add(-activeStreamWindow).UnixNano()
	var n int64
	e.sources.Range(func(k, _ any) bool {
		s := k.(*source)
		if s.lastRead.Load() > cutoff || s.meter.snapshot().InFlight > 0 {
			n++
		}
		return true
	})
	return n
}

// acquire shares one speculative fetch budget across near and disk read-ahead.
// Demand reads bypass it. Background work may use at most half the budget so
// it leaves room for near read-ahead when the budget exceeds one.
// Shrinks drain naturally; already transmitted work is not preempted.
func (m *deliveryMeter) acquire(ctx context.Context, prio core.Priority) (func(), error) {
	if m == nil || m.owner == nil || prio == core.PriorityLive {
		return func() {}, nil
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limit := m.owner.raLimit()
		m.budgetMu.Lock()
		if m.changed == nil {
			m.changed = make(chan struct{})
		}
		if m.speculative < limit && (prio != core.PriorityPrefetch || m.prefetch < max(1, limit/2)) {
			m.speculative++
			if prio == core.PriorityPrefetch {
				m.prefetch++
			}
			m.budgetMu.Unlock()
			return func() {
				m.budgetMu.Lock()
				m.speculative--
				if prio == core.PriorityPrefetch {
					m.prefetch--
				}
				close(m.changed)
				m.changed = make(chan struct{})
				m.budgetMu.Unlock()
			}, nil
		}
		changed := m.changed
		m.budgetMu.Unlock()
		timer := time.NewTimer(100 * time.Millisecond) // recheck changing viewer shares
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-changed:
			timer.Stop()
		case <-timer.C:
		}
	}
}
