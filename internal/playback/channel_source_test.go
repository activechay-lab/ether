package playback

import (
	"testing"
	"time"
)

// Tail metadata reads map to duration, but a channel has its own programme
// clock and no HLS swap target. Only that clock may replace its decoder.
func TestChannelTailProbeDoesNotOwnRecovery(t *testing.T) {
	h := newHarness(t, []*fakeRelease{mkv("A", 100_000, 100*time.Second), mkv("B", 100_000, 100*time.Second)}, []string{"A", "B"})
	ps := h.prepare(owner, movieKey, "channel")
	s := h.m.lookup(ps.ID)
	s.caps.BroadcastBitrateKbps = 6000
	g := s.current()
	h.m.backgroundFailover(s, g, 100*time.Second)
	if g.backgroundFailover.Load() || s.current() != g {
		t.Fatal("tail metadata read initiated channel recovery")
	}
}
