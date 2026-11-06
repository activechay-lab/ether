package clusterplayback

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playbackbuffer"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

func TestReencodedRecoveryEligibility(t *testing.T) {
	base := core.HLSRequest{Info: core.MediaInfo{Duration: time.Minute, Streams: []core.StreamInfo{{Type: "video", Codec: "h264", FrameRate: 24}, {Index: 1, Type: "audio", Codec: "ac3", SampleRate: 48000, DecodedPadding: 256}}}, Decision: core.Decision{Kind: core.DecisionTranscode, Container: "mpegts", VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, AudioChannels: 2, SubtitleStream: -1}}
	for _, tc := range []struct {
		name    string
		change  func(*core.HLSRequest)
		encoder string
		want    bool
	}{
		{"steady", func(q *core.HLSRequest) {}, "cpu", true},
		{"resume", func(q *core.HLSRequest) { q.Resume = &core.HLSResume{FirstSegment: 2, Generation: 3} }, "cpu", true},
		{"explicit-steady", func(q *core.HLSRequest) { q.Ramp = core.HLSRamp{Version: 1} }, "cpu", true},
		{"ramp", func(q *core.HLSRequest) { q.Ramp = core.HLSRamp{Version: 1, Segments: 2} }, "cpu", false},
		{"gpu", func(q *core.HLSRequest) {}, "h264_nvenc", false},
		{"live", func(q *core.HLSRequest) { q.LiveInput = true }, "cpu", false},
		{"seek", func(q *core.HLSRequest) { q.StartOffset = time.Second }, "cpu", false},
		{"subtitle", func(q *core.HLSRequest) { i := 2; q.SoftSubtitleStream = &i }, "cpu", false},
		{"dash", func(q *core.HLSRequest) { q.Protocol = core.ProtocolDASH }, "cpu", false},
		{"copy-video", func(q *core.HLSRequest) { q.Decision.VideoCopy = true }, "cpu", false},
		{"hevc", func(q *core.HLSRequest) { q.Decision.VideoCodec = "hevc" }, "cpu", false},
		{"opus-output", func(q *core.HLSRequest) { q.Decision.AudioCodec = "opus" }, "cpu", false},
		{"unknown-rate", func(q *core.HLSRequest) { q.Info.Streams[1].SampleRate = 0 }, "cpu", false},
		{"bad-padding", func(q *core.HLSRequest) { q.Info.Streams[1].DecodedPadding = -1 }, "cpu", false},
		{"unproven-source", func(q *core.HLSRequest) { q.Info.Streams[1].Codec = "opus" }, "cpu", false},
		{"output-rate", func(q *core.HLSRequest) { q.Decision.AudioSampleRate = 44100 }, "cpu", false},
		{"filter", func(q *core.HLSRequest) { q.Decision.AudioFilters = []string{"loudnorm"} }, "cpu", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := cloneAdmissionRequest(base)
			tc.change(&q)
			if got := encoderRecoverySupported(q, tc.encoder); got != tc.want {
				t.Fatalf("supported=%v want %v", got, tc.want)
			}
			b, err := playbackbuffer.New(playbackbuffer.Limits{Global: playbackbuffer.Budget{Bytes: 1 << 20, Segments: 16, Inits: 1}, PerSession: playbackbuffer.Budget{Bytes: 1 << 20, Segments: 16, Inits: 1}, Sessions: 1})
			if err != nil {
				t.Fatal(err)
			}
			r := &Runtime{automaticOutput: true, o: Options{OutputBuffer: b}}
			route := Route{Fence: placement.Fence{Session: tc.name, Generation: 1}, Encoder: tc.encoder}
			if got := r.newBufferedSession(route, q, &remotetranscode.Session{}) != nil; got != tc.want {
				t.Fatalf("automatic buffer=%v want %v", got, tc.want)
			}
		})
	}
}
