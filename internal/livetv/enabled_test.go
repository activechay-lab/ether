package livetv

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

// Both clock and timer spies stay silent after construction while disabled.
// This guards all worker families at their single lifetime admission point.
func TestDisabledStartsNoBackgroundWork(t *testing.T) {
	db, err := store.OpenMemory(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var clocks, timers atomic.Int64
	s, err := New(Options{DB: db, Showcase: true, Enabled: func() bool { return false }, Now: func() time.Time { clocks.Add(1); return time.Unix(0, 0) }, NewTimer: func(d time.Duration) *time.Timer { timers.Add(1); return time.NewTimer(d) }, NewTicker: func(d time.Duration) *time.Ticker { timers.Add(1); return time.NewTicker(d) }})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	initial := clocks.Load()
	s.ReconcileEnabled()
	if err := s.RefreshGuide(t.Context()); err != nil {
		t.Fatal(err)
	}
	if timers.Load() != 0 || clocks.Load() != initial || s.running {
		t.Fatal("disabled service admitted background work")
	}
	if s.VisibleTo(t.Context(), nil) {
		t.Fatal("disabled service visible")
	}
}
