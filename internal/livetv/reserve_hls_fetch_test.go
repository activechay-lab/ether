package livetv

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A provider segment that fails goes straight back to ffmpeg, so its HLS
// demuxer skips it while the reserve plays: no refusal pacing on the
// segment, and none inherited by the playlist reload that follows.
func TestRelayFailsHLSSegmentFast(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			io.WriteString(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:1\n#EXTINF:4,\ns1.ts\n#EXTINF:4,\ns2.ts\n#EXTINF:4,\ns3.ts\n")
		default:
			http.Error(w, "busy", http.StatusServiceUnavailable)
		}
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "steady-h", Channel: Channel{ID: "h", Kind: "iptv"}}}
	if err := s.grantSteadyInput(ss, StreamSource{ID: "a", URL: up.URL + "/live.m3u8"}, http.Header{}); err != nil {
		t.Fatal(err)
	}
	defer s.revokeInput(ss)
	input, _ := s.relayInput(ss)
	get := func(u string) (int, string, time.Duration) {
		start := time.Now()
		res, err := http.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b), time.Since(start)
	}
	_, playlist, _ := get(input)
	segs := regexp.MustCompile(`http://\S+`).FindAllString(playlist, -1)
	if len(segs) == 0 {
		t.Fatalf("no segments in %q", playlist)
	}
	for range 2 {
		code, _, took := get(segs[0])
		if code < 500 || took > 500*time.Millisecond {
			t.Fatalf("failed segment: %d after %v", code, took)
		}
	}
	if _, body, took := get(input); !strings.Contains(body, "#EXTM3U") || took > 500*time.Millisecond {
		t.Fatalf("playlist reload after failed segments took %v", took)
	}
}
