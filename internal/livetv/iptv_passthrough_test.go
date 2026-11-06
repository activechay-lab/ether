package livetv

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func TestIPTVHEVCProviderDecision(t *testing.T) {
	for _, tt := range []struct {
		name         string
		codecs       []string
		height, rate int
		force, copy  bool
		container    string
	}{
		{name: "HEVC HLS", codecs: []string{"h264", "hevc"}, copy: true, container: "fmp4"},
		{name: "HEVC TS", codecs: []string{"h264", "hevc"}, copy: true, container: "mpegts"},
		{name: "H264 client", codecs: []string{"h264"}, container: "mpegts"},
		{name: "device ceiling", codecs: []string{"h264", "hevc"}, height: 1080, container: "fmp4"},
		{name: "owner bitrate", codecs: []string{"h264", "hevc"}, rate: 4000, container: "fmp4"},
		{name: "owner transcode", codecs: []string{"h264", "hevc"}, force: true, container: "fmp4"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p := make([]byte, 188)
				p[0] = 0x47
				w.Write(p)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			}))
			defer up.Close()
			tc := &compatibilityTranscoder{info: core.MediaInfo{Container: "mpegts", BitrateKbps: 20000, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "hevc", Width: 3840, Height: 2160}, {Index: 1, Type: "audio", Codec: "aac", Default: true, Channels: 2}}}}
			s := testService(t, Options{HTTPClient: up.Client(), Transcoder: tc})
			c, err := s.SaveChannel(context.Background(), Channel{Name: "4K", Number: "1", Kind: "iptv", Enabled: true, Transcode: tt.force, StreamURL: up.URL})
			if err != nil {
				t.Fatal(err)
			}
			caps := core.ClientCaps{VideoCodecs: tt.codecs, AudioCodecs: []string{"aac"}, MaxHeight: tt.height, MaxBitrateKbps: tt.rate}
			caps.HLSContainers = []string{tt.container}
			u := &core.User{ID: 1}
			info, err := s.Tune(context.Background(), u, c.ID, caps)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Stop(context.Background(), info.ID, u)
			if len(tc.requests) != 1 {
				t.Fatalf("requests=%d", len(tc.requests))
			}
			r := tc.requests[0]
			if r.Decision.VideoCopy != tt.copy || r.Decision.Container != tt.container {
				t.Fatalf("decision=%+v", r.Decision)
			}
			if tt.copy && (r.SegmentDuration != 2*time.Second || info.MediaInfo.Video().Codec != "hevc") {
				t.Fatal("copy metadata or segment latency lost")
			}
		})
	}
}

type fragmentedLiveFixture struct{ fakeHLS }

func (f *fragmentedLiveFixture) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	fmt.Fprint(w, "#EXTM3U\n#EXT-X-MAP:URI=\"init.mp4\"\n#EXTINF:2,\n00000.m4s\n")
}
func TestIPTVFragmentedRelayRoutes(t *testing.T) {
	s := testService(t, Options{})
	u := &core.User{ID: 1}
	c, err := s.SaveChannel(context.Background(), Channel{Name: "fragmented", Number: "1", Kind: "iptv", Enabled: true, StreamURL: "http://example.invalid/live.ts"})
	if err != nil {
		t.Fatal(err)
	}
	ss := &session{info: SessionInfo{ID: "fragmented", UserID: 1, Channel: c}, user: *u, ready: true, resources: map[string]string{}, relay: &fragmentedLiveFixture{}, relayInfo: &core.MediaInfo{Container: "fmp4"}}
	s.sessions[ss.info.ID] = ss
	w := httptest.NewRecorder()
	s.serveRelayPlaylist(w, httptest.NewRequest("GET", "/", nil), ss)
	for _, want := range []string{"/relay/init.mp4", "/relay/0.m4s"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %s: %s", want, w.Body.String())
		}
	}
	for _, n := range []int{transcode.InitSegment, 0} {
		w = httptest.NewRecorder()
		s.ServeRelaySegment(w, httptest.NewRequest("GET", "/", nil), ss.info.ID, n, u)
		if w.Code != 200 {
			t.Fatalf("segment %d: %d", n, w.Code)
		}
	}
}

// An upstream HLS wrapper does not prove its HEVC segments use the viewer's
// required container; normalize that path by copy as well.
func TestIPTVHEVCHLSUsesFragmentedOutput(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "#EXTM3U\n#EXTINF:2,\nsegment.ts\n") }))
	defer up.Close()
	tc := &compatibilityTranscoder{info: core.MediaInfo{Container: "hls", Streams: []core.StreamInfo{{Type: "video", Codec: "hevc", Width: 3840, Height: 2160}, {Index: 1, Type: "audio", Codec: "aac", Default: true, Channels: 2}}}}
	s := testService(t, Options{HTTPClient: up.Client(), Transcoder: tc})
	c, err := s.SaveChannel(context.Background(), Channel{Name: "HEVC HLS", Number: "1", Kind: "iptv", Enabled: true, StreamURL: up.URL})
	if err != nil {
		t.Fatal(err)
	}
	u := &core.User{ID: 1}
	info, err := s.Tune(context.Background(), u, c.ID, core.ClientCaps{VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"fmp4"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background(), info.ID, u)
	if len(tc.requests) != 1 || !tc.requests[0].Decision.VideoCopy || tc.requests[0].Decision.Container != "fmp4" {
		t.Fatalf("requests=%+v", tc.requests)
	}
}

func TestPreviewHEVCProfileDeclaration(t *testing.T) {
	main := previewClientCaps(httptest.NewRequest("POST", "/?hevc=1", nil))
	if main.MaxHeight != 2160 || main.VideoLevels["hevc"] != 153 || len(main.VideoProfiles["hevc"]) != 1 {
		t.Fatalf("main caps=%+v", main)
	}
	ten := previewClientCaps(httptest.NewRequest("POST", "/?hevc=1&hevc10=1", nil))
	if len(ten.VideoProfiles["hevc"]) != 2 {
		t.Fatal("Main 10 declaration lost")
	}
	fallback := previewClientCaps(httptest.NewRequest("POST", "/?hevc10=1", nil))
	if fallback.SupportsVideo("hevc") || fallback.MaxHeight != 0 {
		t.Fatal("unsupported browser or default quality capped")
	}
}
