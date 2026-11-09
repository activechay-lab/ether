package transcode

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// Issue #410: after an explicit client stop, segment fetches from the dead
// player kept touching the transcode session, so the idle reaper never
// closed it and its (throttle-suspended) ffmpeg lingered. A stop is
// terminal for idle purposes: later fetches do not revive the session, and
// the reaper closes it, which kills the suspended ffmpeg through its
// guardian like any other close.
func TestStoppedSessionReapedDespiteLaterFetches(t *testing.T) {
	fr := newFakeRunner()
	m := newTestManager(t, fr, config.Transcode{})
	s := startSession(t, m, remuxRequest(t, "stop-reap", time.Hour))
	s.PlaybackState("stopped")
	s.Touch() // a segment fetch from the player that already stopped
	m.reapIdle(time.Now())
	if _, ok := m.Session(s.ID()); ok {
		t.Fatal("stopped session survived the reaper because a fetch touched it")
	}
}
