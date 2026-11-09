package transcode

import (
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

// A live playlist slides: it lists the newest liveWindow segments with the
// lengths ffmpeg actually cut, and its discontinuity sequence counts the
// discontinuities that slid out, so a consumer can follow it for hours.
func TestLivePlaylistSlidesWithRealDurations(t *testing.T) {
	m := newTestManager(t, newFakeRunner(), config.Transcode{})
	req := remuxRequest(t, "live-window", 0)
	req.LiveInput = true
	s := startSession(t, m, req)
	s.mu.Lock()
	for n := 0; n < 15; n++ {
		s.done[0][n] = true
		s.noteLiveDurationLocked(n, 2*time.Second+time.Duration(n)*100*time.Millisecond)
	}
	s.liveDiscontinuities = map[int]bool{3: true, 12: true}
	body := string(s.renderPlaylistLocked(0))
	s.mu.Unlock()
	for _, want := range []string{"#EXT-X-MEDIA-SEQUENCE:5\n", "#EXT-X-DISCONTINUITY-SEQUENCE:1\n", "#EXT-X-TARGETDURATION:4\n", "#EXTINF:3.400000,\n"} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q:\n%s", want, body)
		}
	}
	if strings.Count(body, "#EXTINF") != liveWindow || strings.Count(body, "#EXT-X-DISCONTINUITY\n") != 1 || strings.Contains(body, "PLAYLIST-TYPE") {
		t.Fatalf("live window wrong:\n%s", body)
	}
}
