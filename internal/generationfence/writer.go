// Package generationfence checks execution authority at the output consumer.
package generationfence

import (
	"errors"
	"net/http"
	"sync/atomic"
	"time"
)

var ErrStale = errors.New("execution generation is no longer authoritative")

const MaxStaleness = time.Second

// Writer admits headers with a durable Check. Streaming authority is rechecked
// at most MaxStaleness after the last check started, independently of byte count.
// LocalCheck runs on every operation and must only inspect local cancellation,
// sealing and monotonic lease deadlines. Watch may invalidate cached authority
// from notification callbacks; it must not do database work. Call Close on exit.
// ResponseWriter operations, Validate and Close belong to the handler goroutine;
// only the Watch callback may run concurrently. No ReaderFrom/Unwrap bypass exists.
// An already admitted socket write cannot be recalled.
type Writer struct {
	http.ResponseWriter
	Check             func() error
	LocalCheck        func() error
	Watch             func(invalidate func()) (unsubscribe func())
	Interval          time.Duration // zero defaults to MaxStaleness; larger values are capped
	committed, denied bool
	next              time.Time
	epoch             atomic.Uint64
	checked           uint64
	unwatch           func()
	watching          bool
	now               func() time.Time // deterministic package tests; production uses monotonic time.Now
}

func (w *Writer) clock() time.Time {
	if w.now != nil {
		return w.now()
	}
	return time.Now()
}
func (w *Writer) Close() {
	if w.unwatch != nil {
		w.unwatch()
		w.unwatch = nil
	}
}

// Validate shares the same budget with readers that check around a blocked read.
func (w *Writer) Validate() error {
	if w.denied {
		return ErrStale
	}
	if !w.watching {
		w.watching = true
		if w.Watch != nil {
			w.unwatch = w.Watch(func() { w.epoch.Add(1) })
		}
	}
	if w.LocalCheck != nil {
		if err := w.LocalCheck(); err != nil {
			w.denied = true
			return ErrStale
		}
	}
	now := w.clock()
	epoch := w.epoch.Load()
	if w.next.IsZero() || !now.Before(w.next) || epoch != w.checked {
		if w.Check == nil || w.Check() != nil {
			w.denied = true
			return ErrStale
		}
		interval := w.Interval
		if interval <= 0 || interval > MaxStaleness {
			interval = MaxStaleness
		}
		w.next = now.Add(interval)
		w.checked = epoch
		// Never publish if a lease expired or local sealing raced the durable check.
		if w.LocalCheck != nil && w.LocalCheck() != nil {
			w.denied = true
			return ErrStale
		}
		// A notification racing Check invalidates this result. Refuse this writer
		// rather than publishing with an observation that may predate the event.
		if !w.clock().Before(w.next) || w.epoch.Load() != epoch {
			w.denied = true
			return ErrStale
		}
	}
	return nil
}
func (w *Writer) valid() bool {
	if w.Validate() == nil {
		return true
	}
	if !w.committed {
		for k := range w.Header() {
			delete(w.Header(), k)
		}
		http.Error(w.ResponseWriter, "execution unavailable", http.StatusConflict)
		w.committed = true
	}
	return false
}
func (w *Writer) WriteHeader(status int) {
	if w.committed || !w.valid() {
		return
	}
	w.committed = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *Writer) Write(b []byte) (int, error) {
	if !w.valid() {
		return 0, ErrStale
	}
	if !w.committed {
		w.committed = true
		w.ResponseWriter.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}
func (w *Writer) Flush() {
	if !w.valid() {
		return
	}
	if !w.committed {
		w.WriteHeader(http.StatusOK)
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
