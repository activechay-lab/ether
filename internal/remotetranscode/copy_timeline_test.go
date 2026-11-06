package remotetranscode

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestCopyTimelineNegotiation(t *testing.T) {
	q := core.HLSRequest{Protocol: core.ProtocolHLS, SegmentDuration: 1920 * time.Millisecond, Resume: &core.HLSResume{FirstSegment: 1, Generation: 2}, Info: core.MediaInfo{Container: "mpegts", Duration: 3840 * time.Millisecond, CopyTimeline: &core.CopyTimeline{Version: 1, Origin: 127920, Step: 172800, End: 345600, VideoStream: 0, AudioStream: 1, VideoAUs: 96, AudioAUs: 180, PacketSHA256: [32]byte{1}}, Keyframes: []core.KeyframePoint{{Offset: 188, Time: 0}, {Offset: 18800, Time: 1920 * time.Millisecond}}, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", FrameRate: 25}, {Index: 1, Type: "audio", Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2}}}, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioCodec: "aac", Container: "mpegts", AudioStream: 1, SubtitleStream: -1}}
	caps := Supported()
	caps.Keyframes = true
	if SupportsRequest(q, caps) {
		t.Fatal("unnegotiated copy recovery admitted")
	}
	caps.CopyTimeline = true
	if !SupportsRequest(q, caps) {
		t.Fatal("verified copy resume rejected")
	}
	q.Info.CopyTimeline = nil
	if SupportsRequest(q, caps) {
		t.Fatal("unproven copy resume admitted")
	}
}
