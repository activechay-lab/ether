package livetv

import (
	"context"
	"fmt"
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

// A Steady grant's listed segments are fetched ahead of the decoder, so a
// segment the provider is slow to answer is ready by the time the decoder
// reaches it in its reserve.
func TestSteadyPrefetchesSlowHLSSegment(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:1\n")
			for i := 1; i <= 10; i++ {
				fmt.Fprintf(&b, "#EXTINF:2,\ns%d.ts\n", i)
			}
			io.WriteString(w, b.String())
		case "/s7.ts":
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
				return
			}
			fallthrough
		default:
			w.Header().Set("Content-Type", "video/mp2t")
			w.Write(make([]byte, 188*100))
		}
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client(), Stability: func() string { return stabilityBalanced }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "steady-pf", Channel: Channel{ID: "pf", Kind: "iptv"}}}
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
	// 12 s of reserve in 2 s segments: the window starts at s5.
	if len(segs) != 6 || !strings.Contains(playlist, "#EXT-X-MEDIA-SEQUENCE:5") {
		t.Fatalf("window:\n%s", playlist)
	}
	if code, _, _ := get(segs[0]); code != 200 {
		t.Fatalf("s5: %d", code)
	}
	time.Sleep(3500 * time.Millisecond) // the decoder plays s5 and s6
	get(segs[1])
	code, body, took := get(segs[2]) // s7, which the provider answers after 3 s
	if code != 200 || len(body) != 188*100 || took > 500*time.Millisecond {
		t.Fatalf("slow segment: %d, %d bytes after %v", code, len(body), took)
	}
}

// When the decoder reaches a segment still in flight and a later one is
// ready, the stuck one is skipped quickly instead of starving the decoder.
func TestSteadySkipsStuckHLSSegment(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u8":
			var b strings.Builder
			b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:2\n#EXT-X-MEDIA-SEQUENCE:1\n")
			for i := 1; i <= 10; i++ {
				fmt.Fprintf(&b, "#EXTINF:2,\ns%d.ts\n", i)
			}
			io.WriteString(w, b.String())
		case "/s6.ts":
			<-r.Context().Done() // never answers
		default:
			w.Header().Set("Content-Type", "video/mp2t")
			w.Write(make([]byte, 188*100))
		}
	}))
	defer up.Close()
	s := testService(t, Options{HTTPClient: up.Client(), Stability: func() string { return stabilityBalanced }})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ss := &session{ctx: ctx, cancel: cancel, info: SessionInfo{ID: "steady-skip", Channel: Channel{ID: "skip", Kind: "iptv"}}}
	if err := s.grantSteadyInput(ss, StreamSource{ID: "a", URL: up.URL + "/live.m3u8"}, http.Header{}); err != nil {
		t.Fatal(err)
	}
	defer s.revokeInput(ss)
	input, _ := s.relayInput(ss)
	res, err := http.Get(input)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	segs := regexp.MustCompile(`http://\S+`).FindAllString(string(b), -1)
	if r, err := http.Get(segs[0]); err != nil || r.StatusCode != 200 {
		t.Fatalf("s5: %v", err)
	} else {
		io.Copy(io.Discard, r.Body)
		r.Body.Close()
	}
	start := time.Now()
	r, err := http.Get(segs[1]) // s6, stuck
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if took := time.Since(start); r.StatusCode != http.StatusBadGateway || took > 2500*time.Millisecond {
		t.Fatalf("stuck segment: %d after %v", r.StatusCode, took)
	}
	if r, err := http.Get(segs[2]); err != nil || r.StatusCode != 200 {
		t.Fatalf("s7 after the skip: %v", err)
	} else {
		r.Body.Close()
	}
}
