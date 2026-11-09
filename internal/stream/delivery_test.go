package stream

import (
	"context"
	"errors"
	"sync/atomic"
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
		base, want, allowance int
		streams               int64
		expected              int
	}{
		{8, 64, 8, 2, 4}, {8, 64, 100, 2, 50}, {8, 64, 0, 1, 8}, {128, 128, 1000, 1, 64}, {8, 64, 1, 10, 1},
	} {
		if got := adaptiveParallel(tc.base, tc.want, tc.allowance, tc.streams); got != tc.expected {
			t.Fatalf("%+v: %d", tc, got)
		}
	}
}

type allowanceFetcher struct {
	*fakeFetcher
	allowance atomic.Int32
}

func (f *allowanceFetcher) ReadAheadConnections() int { return int(f.allowance.Load()) }

// A high-bitrate 4K stream scales up into free provider connections even while
// a low-bitrate stream is active, and scales down immediately when another
// high-bitrate stream starts or the learned provider allowance shrinks.
func TestWaterFillAdaptiveShareAndScaleDown(t *testing.T) {
	f := &allowanceFetcher{fakeFetcher: newFakeFetcher()}
	f.allowance.Store(49) // e.g. 50-connection primary pool minus 1 reserved live slot
	e := newTestEngine(t, f, nil, Options{ReadAheadParallel: 8, PrefetchParallel: 4})
	docA := post(t, f.fakeFetcher, 256<<10, postFile{name: "a.mkv", data: mkvBytes(71, 4<<20)})
	docB := post(t, f.fakeFetcher, 256<<10, postFile{name: "b.mkv", data: mkvBytes(72, 4<<20)})
	a := openSource(t, e, "a", docA).(*source)
	b := openSource(t, e, "b", docB).(*source)

	now := time.Now().UnixNano()
	a.lastRead.Store(now)
	a.playRate.Store(10_000_000) // 80 Mbit/s 4K remux
	a.meter.mu.Lock()
	a.meter.perFetch = 550_000 // ~0.55 MB/s per connection => wants ceil(1.5*10/0.55)+2 = 30
	a.meter.mu.Unlock()

	// Alone on a 49-slot allowance, stream A gets its full wanted 30 fetches.
	if got := a.raLimit(); got != 30 {
		t.Fatalf("single 4K stream raLimit = %d, want 30", got)
	}

	// Low-bitrate stream B starts (1.5 MB/s at 0.55 MB/s/conn => wants base 8):
	// water-filling leaves 49 - 8 = 41 slots for A, so A still gets 30.
	b.lastRead.Store(now)
	b.playRate.Store(1_500_000)
	b.meter.mu.Lock()
	b.meter.perFetch = 550_000
	b.meter.mu.Unlock()
	if gotA, gotB := a.raLimit(), b.raLimit(); gotA != 30 || gotB != 8 {
		t.Fatalf("4K + 1080p water-fill: A=%d B=%d, want 30 and 8", gotA, gotB)
	}

	// Stream B switches to a 4K remux too (wants 30): both scale down to 24.
	b.playRate.Store(10_000_000)
	if gotA, gotB := a.raLimit(), b.raLimit(); gotA != 24 || gotB != 24 {
		t.Fatalf("two 4K streams: A=%d B=%d, want 24 and 24", gotA, gotB)
	}

	// Provider learns a lower 20-connection server cap (19 read-ahead slots):
	// both streams scale down to 9; once B goes idle, A scales to 19.
	f.allowance.Store(19)
	if gotA, gotB := a.raLimit(), b.raLimit(); gotA != 9 || gotB != 9 {
		t.Fatalf("learned 20-conn cap with 2 streams: A=%d B=%d, want 9 and 9", gotA, gotB)
	}
	b.lastRead.Store(time.Now().Add(-2 * activeStreamWindow).UnixNano())
	if gotA := a.raLimit(); gotA != 19 {
		t.Fatalf("learned 20-conn cap alone: A=%d, want 19", gotA)
	}

	// Spare allowance above limit lets disk play-ahead run without stealing
	// near read-ahead slots.
	f.allowance.Store(49)
	bgRels := make([]func(), 0, 4)
	for range 4 {
		rel, err := a.meter.acquire(context.Background(), core.PriorityPrefetch)
		if err != nil {
			t.Fatal(err)
		}
		bgRels = append(bgRels, rel)
	}
	raRels := make([]func(), 0, 30)
	for range 30 {
		rel, err := a.meter.acquire(context.Background(), core.PriorityReadahead)
		if err != nil {
			t.Fatalf("near read-ahead starved by prefetch: %v", err)
		}
		raRels = append(raRels, rel)
	}
	for _, rel := range raRels {
		rel()
	}
	for _, rel := range bgRels {
		rel()
	}
}
