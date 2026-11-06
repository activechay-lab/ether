package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

// Governance fixture only; real media proof lives in transcode's pinned test.
func copyRecoveryRequest() core.HLSRequest {
	p := &core.CopyTimeline{Version: 1, Origin: 127920, Step: 172800, End: 172800 * 30, VideoStream: 0, AudioStream: 1, VideoAUs: 48 * 30, AudioAUs: 90 * 30, PacketSHA256: [32]byte{1}}
	q := core.HLSRequest{Ramp: core.HLSRamp{Version: 1}, SegmentDuration: 1920 * time.Millisecond, Info: core.MediaInfo{Container: "mpegts", Duration: 57600 * time.Millisecond, CopyTimeline: p, Streams: []core.StreamInfo{{Index: 0, Type: "video", Codec: "h264", FrameRate: 25, Height: 720}, {Index: 1, Type: "audio", Codec: "aac", Profile: "LC", SampleRate: 48000, Channels: 2}}}, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, SubtitleStream: -1, Container: "mpegts"}}
	for i := 0; i < 30; i++ {
		q.Info.Keyframes = append(q.Info.Keyframes, core.KeyframePoint{Offset: int64(i) * 1880, Time: time.Duration(i) * 1920 * time.Millisecond})
	}
	return q
}

type copyRecoveryProber struct{ *fixtureEncoder }

func (p copyRecoveryProber) Probe(context.Context, string) (*core.MediaInfo, error) {
	q := copyRecoveryRequest()
	return &q.Info, nil
}
func TestCopyLossRecoveryInvariant(t *testing.T) { testEncoderLossRecoveryInvariant(t, false, true) }
func TestCopyRecoveryEligibility(t *testing.T) {
	q := copyRecoveryRequest()
	if !encoderRecoverySupported(q, "cpu") {
		t.Fatal("verified copy rejected")
	}
	for _, mutate := range []func(*core.HLSRequest){func(q *core.HLSRequest) { q.Info.CopyTimeline = nil }, func(q *core.HLSRequest) { q.Info.CopyTimeline.Step++ }, func(q *core.HLSRequest) { q.Info.Keyframes[1].Time++ }, func(q *core.HLSRequest) { q.Decision.AudioCopy = false }, func(q *core.HLSRequest) { q.Ramp.Segments = 2 }} {
		bad := q
		bad.Info = core.CloneMediaInfo(q.Info)
		mutate(&bad)
		if encoderRecoverySupported(bad, "cpu") {
			t.Fatal("unknown copy admitted")
		}
	}
}
