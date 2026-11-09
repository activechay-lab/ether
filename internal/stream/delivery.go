package stream

import (
	"context"
	"math"
	"slices"
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
	readaheadWait         int
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

// wantedParallel is how many concurrent read-ahead fetches this source wants
// before applying multi-stream provider connection sharing.
func (s *source) wantedParallel() int {
	base := max(1, min(s.e.opts.ReadAheadParallel, maxAdaptiveParallel))
	rate := s.playRate.Load()
	per := s.meter.perFetchBps()
	if rate <= 0 || per <= 0 {
		return base
	}
	want := int(math.Min(maxAdaptiveParallel, math.Ceil(adaptiveHeadroom*float64(rate)/per))) + adaptiveMargin
	return min(max(base, want), maxAdaptiveParallel)
}

// raLimit is how many read-ahead fetches this source may run now: enough to
// deliver adaptiveHeadroom times the media bitrate at the measured per-fetch
// throughput, bounded by this stream's water-filled share of usable provider
// read-ahead connections (scaling up into free slots and down when other
// streams need connections).
func (s *source) raLimit() int {
	limit, _ := s.raBudget()
	return limit
}

// raBudget returns (limit, streamAllowance): limit is the source's target
// read-ahead concurrency; streamAllowance is its water-filled connection
// ceiling (0 when the fetcher reports no connection limit).
func (s *source) raBudget() (limit int, share int) {
	base := s.e.opts.ReadAheadParallel
	want := s.wantedParallel()
	allowance, known := s.e.readAheadAllowance()
	if !known {
		return min(max(1, max(base, want)), maxAdaptiveParallel), 0
	}
	if allowance <= 0 {
		return 1, 1
	}
	peers := s.e.peerWantedParallel(s)
	share = waterFillCeiling(allowance, peers)
	return min(max(1, max(base, want)), share), share
}

// waterFillCeiling computes this stream's fair connection ceiling out of
// allowance given the wanted concurrencies of other active streams. Peers that
// need fewer connections than an equal split take only what they want, leaving
// the remaining allowance for high-bitrate streams; when another high-bitrate
// stream starts, the ceiling drops immediately so active fetches drain down.
func waterFillCeiling(allowance int, peerWants []int) int {
	if allowance <= 0 {
		return maxAdaptiveParallel
	}
	participants := 1 + len(peerWants)
	if allowance <= participants {
		return 1
	}
	if len(peerWants) == 0 {
		return min(maxAdaptiveParallel, allowance)
	}
	sorted := slices.Clone(peerWants)
	slices.Sort(sorted)
	remaining := allowance
	for _, pw := range sorted {
		pw = max(1, pw)
		fair := remaining / participants
		if pw >= fair {
			break
		}
		remaining -= pw
		participants--
	}
	return min(maxAdaptiveParallel, max(1, remaining/participants))
}

// adaptiveParallel bounds a wanted concurrency by the base floor, the hard
// ceiling and, when the providers' read-ahead allowance is known, an equal
// share across equal-demand playing streams.
func adaptiveParallel(base, want, allowance int, streams int64) int {
	base = max(1, min(base, maxAdaptiveParallel))
	if allowance <= 0 {
		return base
	}
	n := int(max(1, streams))
	share := max(1, allowance/n)
	return min(max(base, want), min(maxAdaptiveParallel, share))
}

// readAheadAllowance returns the providers' usable read-ahead connection
// allowance on this node and whether the fetcher reports connection limits.
func (e *Engine) readAheadAllowance() (int, bool) {
	if c, ok := e.fetcher.(interface{ ReadAheadConnections() int }); ok {
		return c.ReadAheadConnections(), true
	}
	if c, ok := e.fetcher.(interface{ MaxConnections() int }); ok {
		n := c.MaxConnections()
		if n <= 0 {
			return 0, false
		}
		if n >= 2 {
			return n - 1, true
		}
		return 1, true
	}
	return 0, false
}

func (s *source) isActiveAt(cutoff int64) bool {
	return s.lastRead.Load() > cutoff || s.meter.snapshot().InFlight > 0
}

// peerWantedParallel collects wantedParallel() for every other active source.
func (e *Engine) peerWantedParallel(self *source) []int {
	cutoff := time.Now().Add(-activeStreamWindow).UnixNano()
	var peers []int
	e.sources.Range(func(k, _ any) bool {
		s := k.(*source)
		if s != self && s.isActiveAt(cutoff) {
			peers = append(peers, s.wantedParallel())
		}
		return true
	})
	return peers
}

// activeStreams counts recent readers and sources with outstanding fetches.
func (e *Engine) activeStreams() int64 {
	cutoff := time.Now().Add(-activeStreamWindow).UnixNano()
	var n int64
	e.sources.Range(func(k, _ any) bool {
		if k.(*source).isActiveAt(cutoff) {
			n++
		}
		return true
	})
	return n
}

// acquire coordinates speculative fetches across near read-ahead and disk
// play-ahead. Demand reads bypass it. Disk play-ahead (PriorityPrefetch) is
// capped at PrefetchParallel (and at most half of limit), yields whenever near
// read-ahead is waiting, and uses spare stream connection allowance above limit
// when available so near read-ahead keeps its full bitrate target.
// Shrinks drain naturally; already transmitted work is not preempted.
func (m *deliveryMeter) acquire(ctx context.Context, prio core.Priority) (func(), error) {
	if m == nil || m.owner == nil || prio == core.PriorityLive {
		return func() {}, nil
	}
	waitedReadahead := false
	defer func() {
		if waitedReadahead {
			m.budgetMu.Lock()
			m.readaheadWait--
			m.budgetMu.Unlock()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		limit, share := m.owner.raBudget()
		prefetchCap := max(1, min(m.owner.e.opts.PrefetchParallel, limit/2))
		totalCap := limit
		if share > limit {
			totalCap = min(share, limit+prefetchCap)
		}
		m.budgetMu.Lock()
		if m.changed == nil {
			m.changed = make(chan struct{})
		}
		canTake := false
		if prio == core.PriorityPrefetch {
			canTake = m.readaheadWait == 0 && m.prefetch < prefetchCap && m.speculative < totalCap
		} else {
			nearInFlight := m.speculative - m.prefetch
			canTake = nearInFlight < limit && m.speculative < totalCap
		}
		if canTake {
			m.speculative++
			if prio == core.PriorityPrefetch {
				m.prefetch++
			}
			if waitedReadahead {
				m.readaheadWait--
				waitedReadahead = false
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
		if prio != core.PriorityPrefetch && !waitedReadahead {
			m.readaheadWait++
			waitedReadahead = true
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
