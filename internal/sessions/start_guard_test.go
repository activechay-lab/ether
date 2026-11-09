package sessions

import (
	"fmt"
	"testing"
	"time"
)

// The guard must never end a slow start, only a start whose player already
// holds the media and does not advance; it fires once and stays bounded.
func TestStartGuardFiresOnlyAfterDeliveredMediaStalls(t *testing.T) {
	var g StartGuard
	t0 := time.Unix(1000, 0)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	sec := func(s int) time.Duration { return time.Duration(s) * time.Second }

	// Cold start: little media for a minute, then normal playback.
	for s := 0; s <= 60; s += 2 {
		if g.Observe("slow", "buffering", 0, sec(4), at(s)) {
			t.Fatalf("slow start ended at %ds", s)
		}
	}
	for s := 62; s <= 120; s += 2 {
		if g.Observe("slow", "playing", sec(s-62), sec(40), at(s)) {
			t.Fatalf("playing session ended at %ds", s)
		}
	}

	// Delivered but stuck: fires once, startStallWindow after the client
	// first held startStallAhead of media.
	fired := []int{}
	for s := 0; s <= 120; s++ {
		if g.Observe("stuck", "buffering", 0, sec(s), at(s)) {
			fired = append(fired, s)
		}
	}
	if want := int((startStallAhead + startStallWindow) / time.Second); len(fired) != 1 || fired[0] != want {
		t.Fatalf("stuck start fired at %v, want once at %ds", fired, want)
	}

	// A resume jump is not playback, and a pause re-arms the window.
	g.Observe("resume", "buffering", 0, sec(60), at(0))
	g.Observe("resume", "buffering", sec(1800), sec(60), at(1))
	if g.Observe("resume", "paused", sec(1800), sec(60), at(24)) {
		t.Fatal("paused start ended")
	}
	for s := 30; s < 50; s++ {
		if g.Observe("resume", "buffering", sec(1800), sec(60), at(s)) {
			t.Fatalf("re-armed window ended early at %ds", s)
		}
	}
	if !g.Observe("resume", "buffering", sec(1800), sec(60), at(50)) {
		t.Fatal("re-armed stalled start never ended")
	}

	// Real playback disarms the watch, so a later rebuffer is untouched.
	g.Observe("played", "playing", 0, sec(60), at(0))
	g.Observe("played", "playing", sec(3), sec(60), at(3))
	for s := 4; s < 100; s++ {
		if g.Observe("played", "buffering", sec(3), sec(60), at(s)) {
			t.Fatal("rebuffer after playback ended the session")
		}
	}

	// Bounded watches and fallbacks.
	var b StartGuard
	for i := 0; i < startWatchMax+10; i++ {
		b.Observe(fmt.Sprint(i), "buffering", 0, 0, at(0))
	}
	if len(b.watches) != startWatchMax {
		t.Fatalf("watches = %d", len(b.watches))
	}
	b.Expire(at(0).Add(startWatchTTL + time.Second))
	if len(b.watches) != 0 {
		t.Fatalf("stale watches kept: %d", len(b.watches))
	}
	for i := 0; i < dvBaseHintMax+10; i++ {
		b.Remember(StartHintKey{UserID: int64(i)}, at(i))
	}
	newest := StartHintKey{UserID: dvBaseHintMax + 9}
	if len(b.hints) != dvBaseHintMax || b.BaseOnly(StartHintKey{UserID: 0}, at(600)) || !b.BaseOnly(newest, at(600)) {
		t.Fatalf("hints = %d", len(b.hints))
	}
	if b.BaseOnly(newest, at(dvBaseHintMax+9).Add(dvBaseHintTTL)) {
		t.Fatal("fallback outlived its TTL")
	}
}
