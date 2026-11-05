package transcode

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// Real media verifies the generated-source/segmented-session contract; fakes
// alone cannot catch a loop seek past EOF, missing audio or invalid muxing.
func TestIntegrationOffAirSlate(t *testing.T) {
	e := newRealEnv(t, WithoutHWDetection())
	info := core.MediaInfo{Container: "mp4", Duration: 13 * time.Second, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264", Width: 320, Height: 180, FrameRate: 25},
		{Index: 1, Type: "audio", Codec: "aac", Channels: 2, SampleRate: 48000},
	}}
	for _, proto := range []string{core.ProtocolHLS, core.ProtocolDASH} {
		req := core.HLSRequest{SessionID: "slate-" + proto, SlateText: []string{"Off air", "Test channel", "Back at 6:00 PM"}, Info: info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: ContainerMPEGTS, AudioStream: 1, SubtitleStream: -1}, StartOffset: 8 * time.Second, SegmentDuration: 4 * time.Second, Protocol: proto}
		hs, err := e.m.StartHLS(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { hs.Close() })
		rec := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil)
		if proto == core.ProtocolDASH {
			ds := hs.(core.DASHSession)
			ds.ServeMedia(rec, r, 0, 2)
		} else {
			hs.ServeSegment(rec, r, 0)
		}
		if rec.Code != 200 || rec.Body.Len() == 0 {
			t.Fatalf("%s segment: %d %s", proto, rec.Code, rec.Body.String())
		}
		// The short final segment and DASH audio must also be deliverable.
		tail := httptest.NewRecorder()
		if proto == core.ProtocolDASH {
			ds := hs.(core.DASHSession)
			ds.ServeMedia(tail, r, 1, 3)
		} else {
			hs.ServeSegment(tail, r, 1)
		}
		if tail.Code != 200 || tail.Body.Len() == 0 {
			t.Fatalf("%s final/audio segment: %d", proto, tail.Code)
		}
		// The cached input must contain real video and silent audio, not metadata.
		source := hs.(*Session).input
		streams := e.probeFile(t, source)
		if len(streams) != 2 || streams[0].CodecName != "h264" || streams[1].CodecName != "aac" {
			t.Fatalf("slate streams: %+v", streams)
		}
		hs.Close()
	}
	entries, err := os.ReadDir(filepath.Join(e.m.tempDir, "slates"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("same text/profile rendered %d cache entries", len(entries))
	}
}
