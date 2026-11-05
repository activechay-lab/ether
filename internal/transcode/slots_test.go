package transcode

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/config"
	"testing"
	"time"
)

// Priority, newest-holder preemption, exact accounting, and viewer precedence
// are one shared admission invariant (including late releases after preemption).
func TestSlotPriority(t *testing.T) {
	m := newTestManager(t, newFakeRunner(), config.Transcode{MaxTranscodes: 2})
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	old, oldRelease, err := m.AcquireTranscodeSlot(ctx, SlotBackground)
	if err != nil {
		t.Fatal(err)
	}
	defer oldRelease()
	newest, newRelease, err := m.AcquireTranscodeSlot(ctx, SlotBackground)
	if err != nil {
		t.Fatal(err)
	}
	defer newRelease()
	viewer, viewerRelease, err := m.AcquireTranscodeSlot(ctx, SlotViewer)
	if err != nil {
		t.Fatal(err)
	}
	defer viewerRelease()
	if !errors.Is(newest.Err(), context.Canceled) || old.Err() != nil || viewer.Err() != nil {
		t.Fatal("viewer did not preempt only newest background holder")
	}
	newRelease()
	newRelease()
	m.mu.Lock()
	count := m.encodingLocked("")
	m.mu.Unlock()
	if count != 2 {
		t.Fatalf("late release changed budget: %d", count)
	}
	// Fill with viewers, then queue a viewer and background behind them.
	_, otherRelease, err := m.AcquireTranscodeSlot(ctx, SlotViewer)
	if err != nil {
		t.Fatal(err)
	}
	defer otherRelease()
	if old.Err() != context.Canceled {
		t.Fatal("second viewer did not preempt older background")
	}
	viewerAcquired := make(chan func(), 1)
	go func() {
		_, r, e := m.AcquireTranscodeSlot(ctx, SlotViewer)
		if e == nil {
			viewerAcquired <- r
		}
	}()
	for {
		m.mu.Lock()
		waiting := m.slotViewers
		m.mu.Unlock()
		if waiting > 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		default:
			time.Sleep(time.Millisecond)
		}
	}
	backgroundAcquired := make(chan func(), 1)
	go func() {
		_, r, e := m.AcquireTranscodeSlot(ctx, SlotBackground)
		if e == nil {
			backgroundAcquired <- r
		}
	}()
	viewerRelease()
	var queuedRelease func()
	select {
	case queuedRelease = <-viewerAcquired:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case release := <-backgroundAcquired:
		release()
		t.Fatal("background overtook waiting viewer")
	default:
	}
	queuedRelease()
	select {
	case release := <-backgroundAcquired:
		release()
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
