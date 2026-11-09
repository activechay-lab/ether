package playback

import (
	"testing"
	"time"
)

// Issue #410: a client that reported "stopped" kept its Ether session alive
// because segment reads landing after the stop cleared the pending stop.
// Media delivery is not a comeback; only a new play or a playing/paused
// timeline cancels a client stop.
func TestStoppedSessionEndsDespiteLaterMediaReads(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10_000, time.Minute)}, []string{"A"},
		func(h *harness, o *Options) { o.IdleTimeout = 10 * time.Minute; o.StoppedGrace = time.Minute })
	ps := h.prepare(owner, movieKey, "c1")
	h.m.Timeline(owner.ID, movieKey, "stopped")
	h.clock.Advance(30 * time.Second)
	h.m.BeginMediaRead(ps.ID)() // the dead player drains a burst of segments
	h.clock.Advance(31 * time.Second)
	h.m.expire()
	if _, ok := h.m.Session(ps.ID); ok {
		t.Fatal("media reads after a client stop revived the session")
	}
}

func TestStoppedSessionSurvivesOnPlayingTimeline(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 10_000, time.Minute)}, []string{"A"},
		func(h *harness, o *Options) { o.IdleTimeout = 10 * time.Minute; o.StoppedGrace = time.Minute })
	ps := h.prepare(owner, movieKey, "c1")
	h.m.Timeline(owner.ID, movieKey, "stopped")
	h.clock.Advance(30 * time.Second)
	h.m.Timeline(owner.ID, movieKey, "playing") // the viewer pressed play again
	h.clock.Advance(2 * time.Minute)
	h.m.expire()
	if _, ok := h.m.Session(ps.ID); !ok {
		t.Fatal("a playing timeline after a stop did not keep the session")
	}
}
