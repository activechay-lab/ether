package transcode

import (
	"testing"
	"time"
)

func TestLiveRecoveryReplenishesAfterDelivery(t *testing.T) {
	now := time.Now()
	s := &Session{segDur: 2 * time.Second}
	r := &run{started: now.Add(-8 * time.Second)}
	for i := 0; i < 7; i++ {
		delay, ok := s.liveRecoveryDelay(now, r)
		if !ok || delay < 250*time.Millisecond || delay > 4*time.Second {
			t.Fatalf("attempt %d: %s %v", i, delay, ok)
		}
	}
	if _, ok := s.liveRecoveryDelay(now, r); ok {
		t.Fatal("empty stream retried without a bound")
	}
	s.deliveredCount = 2
	r.produced = 2
	s.deliveredSeg = r.startSeg - 1
	if _, ok := s.liveRecoveryDelay(now, r); ok {
		t.Fatal("cached delivery from an old run replenished retries")
	}
	s.deliveredSeg = r.startSeg + 1
	for i := 0; i < 21; i++ {
		if _, ok := s.liveRecoveryDelay(now, r); !ok {
			t.Fatalf("healthy attempt %d refused", i)
		}
	}
	if _, ok := s.liveRecoveryDelay(now, r); ok {
		t.Fatal("delivery bypassed the burst ceiling")
	}
	if delay, ok := s.liveRecoveryDelay(now.Add(31*time.Second), r); !ok || delay != 250*time.Millisecond {
		t.Fatalf("replenish: %s %v", delay, ok)
	}
}
