package caps

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"slices"
	"testing"
)

func TestBuildLayers(t *testing.T) {
	b := Builder{Learn: func(_ context.Context, c core.ClientCaps) (core.ClientCaps, error) { c.MaxHeight = 1080; return c, nil }, Rules: func(_ context.Context, c core.ClientCaps) (core.ClientCaps, error) {
		if c.MaxHeight != 1080 {
			t.Fatal("learning was not first")
		}
		c.MaxHeight = 720
		return c, nil
	}}
	c, err := b.Build(context.Background(), DeviceInfo{VideoCodecs: []string{"H264"}}, DeviceProfile{MaxStreamingBitrate: 8000000, DirectPlayProfiles: []DirectPlayProfile{{Container: "mp4,mkv", VideoCodec: "h264,hevc", AudioCodec: "aac"}}}, []string{"http-get:*:video/mp2t:*"}, "App/1")
	if err != nil || c.Product != "App/1" || c.MaxHeight != 720 || c.MaxBitrateKbps != 8000 || !c.SupportsVideo("hevc") || !c.SupportsContainer("mpegts") || len(c.VideoCodecs) != 2 {
		t.Fatal(c, err)
	}
}

// Real clients disagree with the Jellyfin schema about JSON types:
// Litefin's Tizen/WebOS profiles send MaxAudioChannels as a number, other
// clients quote numbers and booleans. One such field must never fail the
// whole profile (issue #404).
func TestDeviceProfileFlexibleJSONTypes(t *testing.T) {
	var p DeviceProfile
	input := `{"MaxStreamingBitrate":"120000000","MaxStaticBitrate":12000000.0,"AlwaysUseMostCompatibleTranscodingProfile":"true","TranscodingProfiles":[{"Type":"Video","Container":"ts","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls","MaxAudioChannels":6,"MinSegments":"2","SegmentLength":"6","BreakOnNonKeyFrames":"true","EstimateContentLength":1,"CopyTimestamps":"false","EnableAudioVbrEncoding":0,"Conditions":[{"Condition":"LessThanEqual","Property":"VideoLevel","Value":52,"IsRequired":"false"}]}],"DirectPlayProfiles":[{"Type":"Video","Container":"mkv","VideoCodec":"hevc","AudioCodec":"aac"}]}`
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatal(err)
	}
	if p.MaxStreamingBitrate != 120000000 || p.MaxStaticBitrate != 12000000 || !p.AlwaysUseMostCompatibleTranscodingProfile {
		t.Fatal(p)
	}
	if len(p.TranscodingProfiles) != 1 {
		t.Fatal(p.TranscodingProfiles)
	}
	tp := p.TranscodingProfiles[0]
	if tp.MaxAudioChannels != "6" || tp.MinSegments != 2 || tp.SegmentLength != 6 {
		t.Fatal(tp)
	}
	if tp.BreakOnNonKeyFrames == nil || !*tp.BreakOnNonKeyFrames || tp.EnableAudioVbrEncoding == nil || *tp.EnableAudioVbrEncoding {
		t.Fatal(tp)
	}
	if !tp.EstimateContentLength || tp.CopyTimestamps {
		t.Fatal(tp)
	}
	if len(tp.Conditions) != 1 || tp.Conditions[0].Value != "52" || tp.Conditions[0].IsRequired == nil || *tp.Conditions[0].IsRequired {
		t.Fatal(tp.Conditions)
	}
	got, err := (Builder{}).Build(context.Background(), DeviceInfo{}, p, nil, "")
	if err != nil || got.Profiles == nil || len(got.Profiles.Transcoding) != 1 || got.Profiles.Transcoding[0].MaxAudioChannels != "6" {
		t.Fatal(got, err)
	}
	// A value of neither type degrades to zero instead of failing decode.
	var odd DeviceProfile
	if err := json.Unmarshal([]byte(`{"MaxStreamingBitrate":"fast","TranscodingProfiles":[{"MaxAudioChannels":true,"MinSegments":{}}]}`), &odd); err != nil {
		t.Fatal(err)
	}
	if odd.MaxStreamingBitrate != 0 || len(odd.TranscodingProfiles) != 1 || odd.TranscodingProfiles[0].MinSegments != 0 {
		t.Fatal(odd)
	}
}

func TestLayerFailure(t *testing.T) {
	want := errors.New("failed")
	b := Builder{Learn: func(context.Context, core.ClientCaps) (core.ClientCaps, error) { return core.ClientCaps{}, want }, Rules: func(context.Context, core.ClientCaps) (core.ClientCaps, error) {
		t.Fatal("rules called")
		return core.ClientCaps{}, nil
	}}
	_, err := b.Build(context.Background(), DeviceInfo{}, DeviceProfile{}, nil, "")
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestDeviceProfilesPreservePairsAndConditions(t *testing.T) {
	var p DeviceProfile
	input := `{"DirectPlayProfiles":[{"Type":"Video","Container":"mkv","VideoCodec":"hevc","AudioCodec":"aac,ac3"},{"Type":"Video","Container":"mp4","VideoCodec":"h264","AudioCodec":"aac"}],"TranscodingProfiles":[{"Type":"Video","Container":"ts","VideoCodec":"h264","AudioCodec":"aac","Protocol":"hls","SegmentLength":6,"Conditions":[{"Condition":"Equals","Property":"FutureCodecLimit","Value":"x"}]}],"SubtitleProfiles":[{"Format":"srt","Method":"External","Language":"en","Container":"mp4"}],"CodecProfiles":[{"Type":"Video","Codec":"h264","Conditions":[{"Condition":"LessThanEqual","Property":"VideoLevel","Value":"52","IsRequired":false}]}],"ContainerProfiles":[],"MaxStaticBitrate":12000000}`
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		t.Fatal(err)
	}
	got, err := (Builder{}).Build(context.Background(), DeviceInfo{}, p, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if got.Profiles == nil || len(got.Profiles.DirectPlay) != 2 {
		t.Fatal(got.Profiles)
	}
	first, second := got.Profiles.DirectPlay[0], got.Profiles.DirectPlay[1]
	if !slices.Equal(first.Containers, []string{"mkv"}) || !slices.Equal(first.VideoCodecs, []string{"hevc"}) || !slices.Equal(second.Containers, []string{"mp4"}) || !slices.Equal(second.VideoCodecs, []string{"h264"}) {
		t.Fatal(first, second)
	}
	if len(got.Profiles.Transcoding) != 1 || got.Profiles.Transcoding[0].Delivery != "hls" || got.Profiles.Transcoding[0].SegmentLength != 6 || !got.Profiles.Transcoding[0].Conditions[0].Required {
		t.Fatal(got.Profiles.Transcoding)
	}
	if got.Profiles.Codecs[0].Conditions[0].Required || got.Profiles.Subtitles[0].Method != "external" || got.Profiles.Containers == nil || got.Profiles.MaxStaticBitrate != 12000000 {
		t.Fatal(got.Profiles)
	}
	empty, err := (Builder{}).Build(context.Background(), DeviceInfo{}, DeviceProfile{}, nil, "")
	if err != nil || empty.Profiles != nil {
		t.Fatal(empty, err)
	}
}
