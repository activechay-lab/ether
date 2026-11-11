package frontendadapter

import (
	"sync"
	"time"
)

// limiters is a per-viewer token bucket for one adapter (spec §6.5:
// defaults 600 req/min/viewer). It bounds how much work one viewer can
// make one adapter do without letting a busy viewer starve another.
type limiters struct {
	mu       sync.Mutex
	buckets  map[int64]*bucket
	capacity float64
	refill   float64 // tokens per second
	now      func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// newLimiters builds the default budget for one adapter declaration.
func newLimiters(decl Declaration) *limiters {
	return newLimitersWithRate(DefaultRequestsPerMinute)
}

// newLimitersWithRate builds a limiter with an explicit per-minute rate.
func newLimitersWithRate(perMinute int) *limiters {
	if perMinute <= 0 {
		perMinute = DefaultRequestsPerMinute
	}
	capacity := float64(perMinute)
	return &limiters{
		buckets:  make(map[int64]*bucket),
		capacity: capacity,
		refill:   capacity / 60.0,
		now:      time.Now,
	}
}

// allow reports whether viewerID may make one more request. It refills the
// bucket by elapsed time so a steady caller at or below the rate is never
// throttled.
func (l *limiters) allow(viewerID int64, now time.Time) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.buckets[viewerID]
	if b == nil {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[viewerID] = b
	}
	if elapsed := now.Sub(b.last).Seconds(); elapsed > 0 {
		b.tokens += elapsed * l.refill
		if b.tokens > l.capacity {
			b.tokens = l.capacity
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
