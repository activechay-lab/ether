package stream

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Overlap must be wall time, failed work must contribute time but no bytes,
// and cache reads must not re-count an already delivered article.
func TestDeliveryAccounting(t *testing.T) {
	var m deliveryMeter
	a := m.begin()
	b := m.begin()
	m.end(a, 100, true, core.PriorityReadahead)
	mid := m.snapshot()
	if mid.Bytes != 100 || mid.Fetches != 1 || mid.InFlight != 1 {
		t.Fatalf("mid: %+v", mid)
	}
	m.end(b, 100, false, core.PriorityLive)
	end := m.snapshot()
	if end.Bytes != 100 || end.Fetches != 1 || end.InFlight != 0 || end.Busy < mid.Busy || end.Busy > time.Since(a) {
		t.Fatalf("end: %+v", end)
	}
	f := newLaneFetcher()
	close(f.gates[core.PriorityLive])
	e, rel, pf := flightFixture(t, f)
	fo := fetchOpts{meter: &m}
	if _, err := e.segment(context.Background(), rel, pf, 0, core.PriorityLive, fo); err != nil {
		t.Fatal(err)
	}
	first := m.snapshot()
	if first.Bytes != 1124 || first.Fetches != 2 {
		t.Fatalf("fetch: %+v", first)
	}
	if _, err := e.segment(context.Background(), rel, pf, 0, core.PriorityLive, fo); err != nil {
		t.Fatal(err)
	}
	if got := m.snapshot(); got.Bytes != first.Bytes || got.Fetches != first.Fetches {
		t.Fatalf("cache counted: %+v", got)
	}
}

func TestDeliveryBudgetAndCancellation(t *testing.T) {
	e := newTestEngine(t, newFakeFetcher(), nil, Options{ReadAheadParallel: 2})
	s := &source{e: e}
	m := &s.meter
	m.owner = s
	bg, err := m.acquire(context.Background(), core.PriorityPrefetch)
	if err != nil {
		t.Fatal(err)
	}
	ra, err := m.acquire(context.Background(), core.PriorityReadahead)
	if err != nil {
		t.Fatal(err)
	}
	live, err := m.acquire(context.Background(), core.PriorityLive)
	if err != nil {
		t.Fatal(err)
	}
	live()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		release, err := m.acquire(ctx, core.PriorityReadahead)
		if release != nil {
			release()
		}
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("budget wait did not cancel")
	}
	bg()
	ra()
	if m.speculative != 0 || m.prefetch != 0 {
		t.Fatal("budget leaked")
	}
	for _, tc := range []struct {
		base, want, capacity int
		streams              int64
		expected             int
	}{
		{8, 64, 8, 2, 3}, {8, 64, 100, 2, 37}, {8, 64, 0, 1, 8}, {128, 128, 1000, 1, 64}, {8, 64, 1, 10, 1},
	} {
		if got := adaptiveParallel(tc.base, tc.want, tc.capacity, tc.streams); got != tc.expected {
			t.Fatalf("%+v: %d", tc, got)
		}
	}
}
