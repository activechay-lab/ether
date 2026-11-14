package sessions

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/kit/caps"
)

// Captured bundled jellyfin-web Chrome profile. Output predicates must see
// what the encoder emits; source predicates must still see the original file.
func TestJellyfinWebRowPlaybackProfile(t *testing.T) {
	data, err := os.ReadFile("testdata/jellyfin-web-rowplay.json")
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ DeviceProfile caps.DeviceProfile }
	if err = json.Unmarshal(data, &body); err != nil {
		t.Fatal(err)
	}
	client, err := (caps.Builder{}).Build(context.Background(), caps.DeviceInfo{Protocol: "jellyfin", Product: "Jellyfin Web"}, body.DeviceProfile, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	yes, no := true, false
	info := core.MediaInfo{Container: "mkv", BitrateKbps: 26000, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "hevc", Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FrameRate: 23.976, BitDepth: 10, ColorTransfer: "smpte2084", DolbyVision: true, DolbyVisionProfile: 8, DolbyVisionCompatibilityID: 1, DolbyVisionConfigurationPresent: &yes, ProbedInterlaced: &no},
		{Index: 1, Type: "audio", Codec: "eac3", Channels: 6, SampleRate: 48000, Language: "rus", Default: true},
		{Index: 2, Type: "audio", Codec: "eac3", Channels: 6, SampleRate: 48000, Language: "eng"},
	}}
	b := &Broker{deps: BrokerDeps{Transcoder: profileTranscoder{}}}
	for _, audio := range []int{1, 2} {
		for _, copyVideo := range []bool{true, false} {
			req := frontend.PlanRequest{Caps: client, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: copyVideo, AudioStream: audio, SubtitleStream: -1}}}
			decision, err := b.decideProfile(&core.PlaySession{Info: info}, req)
			if err != nil {
				t.Fatalf("audio=%d copy=%v: %v", audio, copyVideo, err)
			}
			if decision.AudioStream != audio || decision.AudioCopy || decision.AudioCodec != "aac" {
				t.Fatalf("wrong audio: %+v", decision)
			}
			if copyVideo {
				if !decision.VideoCopy || !decision.StripDolbyVision || decision.VideoCodec != "hevc" {
					t.Fatalf("HDR10 base not preserved: %+v", decision)
				}
			} else if decision.VideoCopy || decision.VideoCodec != "h264" {
				t.Fatalf("missing H264 fallback: %+v", decision)
			}
			if _, ok := b.hlsOutputDecision(&core.PlaySession{Info: info}, req.Caps, req.Intent.Options, nil, decision); !ok {
				t.Fatalf("planned output cannot start: %+v", decision)
			}
			if profileDirectAllowed(client.Profiles, info, decision) {
				t.Fatal("source predicates were widened")
			}
		}
	}
	if len(info.Streams) != 3 || info.Streams[0].Codec != "hevc" || !info.Streams[0].DolbyVision || info.Streams[2].Codec != "eac3" {
		t.Fatal("output validation mutated source")
	}
}
