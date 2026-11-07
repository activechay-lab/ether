package livetv

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// A Steady reader's window starts at the reserve depth without the
// provider's oldest segment, and stays a valid continuation of the
// provider's playlist: media sequence of its first segment, discontinuities
// it dropped carried into the discontinuity sequence, and the key and map
// in force for its first segment kept.
func TestHLSReserveWindow(t *testing.T) {
	base, _ := url.Parse("https://p.example/live/1/index.m3u8")
	playlist := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-VERSION:6",
		"#EXT-X-TARGETDURATION:6",
		"#EXT-X-MEDIA-SEQUENCE:100",
		"#EXT-X-DISCONTINUITY-SEQUENCE:3",
		"#EXT-X-KEY:METHOD=AES-128,URI=\"k1\"",
		"#EXT-X-MAP:URI=\"init.mp4\"",
		"#EXTINF:6.0,",
		"s100.m4s",
		"#EXT-X-DISCONTINUITY",
		"#EXTINF:6.0,",
		"s101.m4s",
		"#EXT-X-KEY:METHOD=AES-128,URI=\"k2\"",
		"#EXTINF:6.0,",
		"s102.m4s",
		"#EXTINF:6.0,",
		"s103.m4s",
		"#EXTINF:6.0,",
		"s104.m4s",
		"",
	}, "\n")
	m := parseHLSMedia([]byte(playlist), base)
	if !m.live() || len(m.segs) != 5 || m.segs[0].seq != 100 || m.segs[4].url != "https://p.example/live/1/s104.m4s" || m.target != 6*time.Second {
		t.Fatalf("parsed %+v", m)
	}
	// 12 s of reserve: start two segments from the end.
	if i := m.startFor(12 * time.Second); i != 3 {
		t.Fatalf("start for 12 s = %d, want 3", i)
	}
	// More reserve than the backlog: everything but the oldest segment.
	if i := m.startFor(time.Minute); i != 1 {
		t.Fatalf("start for 60 s = %d, want 1", i)
	}
	out := string(m.window(102))
	for _, want := range []string{
		"#EXT-X-MEDIA-SEQUENCE:102",
		"#EXT-X-DISCONTINUITY-SEQUENCE:4",
		"#EXT-X-MAP:URI=\"init.mp4\"",
		"#EXT-X-KEY:METHOD=AES-128,URI=\"k2\"",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("window lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "s100") || strings.Contains(out, "s101") || strings.Contains(out, "k1") || strings.Count(out, "#EXT-X-DISCONTINUITY\n") != 0 {
		t.Fatalf("window keeps dropped segments or their tags:\n%s", out)
	}
	again := parseHLSMedia([]byte(out), base)
	if len(again.segs) != 3 || again.segs[0].seq != 102 || again.disc != 4 {
		t.Fatalf("window does not reparse as the same playlist: %+v", again)
	}
	// The window follows the reader: one segment behind what it fetched.
	h := &hlsInput{g: &inputGrant{reserve: 12 * time.Second}, lists: map[string]*hlsList{}, segs: map[string]int64{}, polling: map[string]bool{"x": true}}
	first, _ := h.media("x", []byte(playlist), base)
	if !strings.Contains(string(first), "#EXT-X-MEDIA-SEQUENCE:103") {
		t.Fatalf("first window:\n%s", first)
	}
	h.servedSegment("https://p.example/live/1/s104.m4s")
	next, _ := h.media("x", []byte(playlist), base)
	if !strings.Contains(string(next), "#EXT-X-MEDIA-SEQUENCE:103") || strings.Contains(string(next), "s102") {
		t.Fatalf("window after serving 104:\n%s", next)
	}
	if d := h.health().ReserveDepth; d != 0 {
		t.Fatalf("depth after the newest segment was served = %v", d)
	}
}
