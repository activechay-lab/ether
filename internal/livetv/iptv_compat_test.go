package livetv

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type compatibilityTranscoder struct {
	fakeTranscoder
	info   core.MediaInfo
	probes int
}

func (f *compatibilityTranscoder) Probe(context.Context, string) (*core.MediaInfo, error) {
	f.probes++
	info := f.info
	return &info, nil
}

func (f *compatibilityTranscoder) Decide(info core.MediaInfo, caps core.ClientCaps, opts core.DecideOptions) core.Decision {
	return transcode.Decide(info, caps, opts, transcode.Policy{})
}

func TestIPTVHLSChecksDecoderCompatibilityBeforePassthrough(t *testing.T) {
	for _, tt := range []struct {
		name, video, audio string
		relay, videoCopy   bool
	}{
		{"MPEG2 and MP2 need encoding", "mpeg2video", "mp2", true, false},
		{"compatible H264 AAC stays upstream", "h264", "aac", false, true},
		{"only unsupported audio is encoded", "h264", "mp2", true, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				fmt.Fprint(w, "#EXTM3U\n#EXTINF:4,\npart.ts\n")
			}))
			defer up.Close()
			tc := &compatibilityTranscoder{info: core.MediaInfo{Container: "hls", Duration: time.Hour, BitrateKbps: 5000, Streams: []core.StreamInfo{
				{Index: 3, Type: "video", Codec: tt.video, CodecString: "source-video", Width: 1280, Height: 720, FrameRate: 59.94},
				{Index: 5, Type: "audio", Codec: tt.audio, CodecString: "source-audio", Channels: 2, Default: true, SampleRate: 44100},
				{Index: 7, Type: "audio", Codec: "aac", Channels: 6},
			}}}
			s := testService(t, Options{HTTPClient: up.Client(), Transcoder: tc})
			channel, err := s.SaveChannel(context.Background(), Channel{Name: "Fixture", Number: "1", Kind: "iptv", Enabled: true, StreamURL: up.URL})
			if err != nil {
				t.Fatal(err)
			}
			u := &core.User{ID: 1}
			info, err := s.Tune(context.Background(), u, channel.ID, core.ClientCaps{ClientID: "apple-tv", Platform: "tvOS", VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac", "ac3", "eac3"}, HLSContainers: []string{"mkv"}})
			if err != nil {
				t.Fatal(err)
			}
			if tc.probes != 1 {
				t.Fatalf("probes = %d", tc.probes)
			}
			if !tt.relay {
				if len(tc.requests) != 0 || info.MediaInfo.Video().Codec != tt.video {
					t.Fatal("compatible source unnecessarily converted")
				}
				return
			}
			if len(tc.requests) != 1 {
				t.Fatalf("relay requests = %d", len(tc.requests))
			}
			r := tc.requests[0]
			if !r.LiveInput {
				t.Fatal("owner-configured IPTV relay must explicitly permit live playlist input")
			}
			if r.Info.Duration != 0 || r.Info.Video().Codec != tt.video || r.Decision.VideoCopy != tt.videoCopy || r.Decision.AudioCopy || r.Decision.AudioStream != 5 || r.Decision.Container != "mpegts" || r.Decision.VideoCodec != "h264" || r.Decision.AudioCodec != "aac" {
				t.Fatalf("wrong conversion plan: %+v", r.Decision)
			}
			for _, snapshot := range []SessionInfo{info, mustLiveSession(t, s, info.ID, u)} {
				media := snapshot.MediaInfo
				if media.Duration != 0 || media.Container != "mpegts" || len(media.Streams) != 2 || media.Streams[0].Index != 0 || media.Streams[1].Index != 1 || media.Streams[0].Codec != "h264" || media.Streams[1].Codec != "aac" || media.Streams[1].Channels != 2 || media.Streams[1].CodecString != "" {
					t.Fatalf("tune describes source rather than output: %+v", media)
				}
				if media.Streams[1].SampleRate != 48000 {
					t.Fatalf("audio analysis retained source sample rate: %d", media.Streams[1].SampleRate)
				}
			}
			if tc.info.Duration != time.Hour || tc.info.Streams[0].Index != 3 || tc.info.Streams[1].Codec != tt.audio {
				t.Fatal("source analysis mutated")
			}
			rec := httptest.NewRecorder()
			s.ServePlaylist(rec, httptest.NewRequest("GET", "/", nil), info.ID, u)
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/relay/0.ts") {
				t.Fatalf("relay playlist: %d %s", rec.Code, rec.Body.String())
			}
			if err := s.Stop(context.Background(), info.ID, u); err != nil {
				t.Fatal(err)
			}
			if !tc.hls[0].closed {
				t.Fatal("live encoder survived stop")
			}
		})
	}
}

func mustLiveSession(t *testing.T, s *Service, id string, u *core.User) SessionInfo {
	t.Helper()
	info, err := s.Session(context.Background(), id, u)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

type startingLiveRelay struct {
	core.HLSSession
	mu    sync.Mutex
	polls int
	fail  bool
}

// The encoder finishes its first segment after a few playlist polls.
func (f *startingLiveRelay) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.polls++
	ready := !f.fail && f.polls > 3
	f.mu.Unlock()
	fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:4\n")
	if ready {
		fmt.Fprint(w, "#EXTINF:4,\n00000.ts\n")
	}
}
func (f *startingLiveRelay) ServeSegment(w http.ResponseWriter, r *http.Request, n int) {
	fmt.Fprint(w, "segment bytes")
}
func (f *startingLiveRelay) Close() error { return nil }

func ownedRelayFixture(t *testing.T, s *Service, ss *session, hls core.HLSSession, dec core.Decision, cut time.Duration) {
	t.Helper()
	if ss.ctx == nil {
		ss.ctx = t.Context()
	}
	p := s.newRelayProducer(ss, core.MediaInfo{}, core.ClientCaps{}, dec, cut)
	if err := p.adopt("relay-fixture", hls, func() {}, core.MediaInfo{}, dec, cut); err != nil {
		t.Fatal(err)
	}
	ss.owned = s.runRelay(ss, p)
	t.Cleanup(ss.owned.close)
}

// The first owned playlist waits for the encoder's first segment rather
// than handing a player an empty live playlist; a producer that never
// delivers ends that wait with an error, not an empty playlist.
func TestRelayFirstPlaylistWaitsForMediaAndPropagatesFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		s := testService(t, Options{})
		h := &startingLiveRelay{fail: fail}
		ss := &session{info: SessionInfo{ID: "live-fixture", Channel: Channel{ID: "c"}}}
		ownedRelayFixture(t, s, ss, h, core.Decision{Container: "mpegts"}, relaySegment)
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		rec := httptest.NewRecorder()
		s.serveOwnedPlaylist(rec, httptest.NewRequest("GET", "/", nil).WithContext(ctx), ss)
		cancel()
		if fail {
			if rec.Code != 502 {
				t.Fatalf("producer failure returned %d", rec.Code)
			}
			continue
		}
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "/relay/0.ts") || strings.Contains(rec.Body.String(), "segment bytes") {
			t.Fatalf("first playlist unusable: %d %s", rec.Code, rec.Body.String())
		}
	}
}

// One viewer's override must never become a channel-wide setting or affect
// another viewer tuning the same upstream at the same time.
func TestViewerCompatibilityOverrideIsolation(t *testing.T) {
	ctx := context.Background()
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:4,\npart.ts\n")
	}))
	defer up.Close()
	tc := &compatibilityTranscoder{info: core.MediaInfo{Container: "hls", Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264", Width: 1280, Height: 720, FrameRate: 24},
		{Index: 1, Type: "audio", Codec: "aac", Channels: 2, Default: true, SampleRate: 48000},
	}}}
	s := testService(t, Options{HTTPClient: up.Client(), Transcoder: tc})
	channel, err := s.SaveChannel(ctx, Channel{Name: "Fixture", Number: "1", Kind: "iptv", Enabled: true, StreamURL: up.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePolicy(ctx, Policy{UserID: 1, Transcode: true}); err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		user   int64
		forced bool
	}{{1, true}, {2, false}, {1, false}} {
		if step.user == 1 && !step.forced {
			if err = s.SavePolicy(ctx, Policy{UserID: 1}); err != nil {
				t.Fatal(err)
			}
		}
		u := &core.User{ID: step.user}
		info, err := s.Tune(ctx, u, channel.ID, core.ClientCaps{ClientID: fmt.Sprint(step.user), VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, HLSContainers: []string{"mpegts"}})
		if err != nil {
			t.Fatal(err)
		}
		if info.Channel.Transcode != step.forced {
			t.Fatalf("user %d transcode = %v", step.user, info.Channel.Transcode)
		}
		if len(tc.requests) != 1 || tc.requests[0].Decision.VideoCopy || tc.requests[0].Decision.Kind != core.DecisionTranscode {
			t.Fatalf("conversion escaped viewer policy: %+v", tc.requests)
		}
	}
	stored, err := s.Channel(ctx, channel.ID)
	if err != nil || stored.Transcode {
		t.Fatalf("viewer override changed saved channel: %+v %v", stored, err)
	}
	stored.Transcode = true
	if _, err = s.SaveChannel(ctx, stored); err != nil {
		t.Fatal(err)
	}
	info, err := s.Tune(ctx, &core.User{ID: 2}, channel.ID, core.ClientCaps{ClientID: "2", VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}})
	if err != nil || !info.Channel.Transcode || len(tc.requests) != 2 {
		t.Fatalf("channel setting lost: %+v %v", info, err)
	}
}
