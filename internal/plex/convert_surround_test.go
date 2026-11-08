package plex

import (
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// PR #395: a profile-extra direct-play profile and HLS target must not put
// E-AC-3 back after the owner's "convert surround audio" rule removed it.
func TestConvertSurroundAudioSurvivesProfileExtra(t *testing.T) {
	extra := "add-direct-play-profile(type=videoProfile&container=mkv&videoCodec=h264,hevc&audioCodec=aac,eac3,ac3,dts,flac)+add-transcode-target(type=videoProfile&protocol=hls&container=mkv&videoCodec=h264,hevc&audioCodec=mp3,aac,ac3,eac3,dts,flac,vorbis)"
	info := core.MediaInfo{Container: "mkv", BitrateKbps: 5000, Streams: []core.StreamInfo{
		{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
		{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
	}}
	for _, convert := range []bool{true, false} {
		q := config.DefaultQuality()
		q.Rules = []config.QualityRule{{Protocol: "plex", Product: "Plex for Android", Profile: "4K", ConvertSurroundAudio: convert}}
		pr := &Request{Product: "Plex for Android (Mobile)", Platform: "Android", Device: "Pixel 9 Pro", ProfileExtra: extra}
		p := &playRequest{caps: resolver.ApplyClientRulesForMedia(q, pr.ClientCaps(0), nil, core.MediaTV), protocol: "hls",
			opts: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: 1, SubtitleStream: -1}}
		ps := &core.PlaySession{Info: info, Resolution: &core.Resolution{Release: core.Release{ID: "nzb-123", Protocol: "usenet"}}}
		if err := (&Server{}).applyDeviceProfile(pr, &core.Title{Key: 4002133750010001, Type: "episode"}, ps, p); err != nil {
			t.Fatal(err)
		}
		d := transcode.Decide(info, p.caps, p.opts, transcode.Policy{})
		if !convert {
			if !p.opts.DirectPlay || d.Kind != core.DecisionDirectPlay {
				t.Fatalf("rule off: want direct play, got %s (%s)", d.Kind, d.Reason)
			}
			continue
		}
		if p.opts.DirectPlay || slices.Contains(p.caps.AudioCodecs, "eac3") || slices.Contains(p.caps.TranscodeAudioCodecs, "eac3") {
			t.Fatalf("rule on: eac3 re-admitted: direct=%v audio=%v target=%v", p.opts.DirectPlay, p.caps.AudioCodecs, p.caps.TranscodeAudioCodecs)
		}
		if d.Kind != core.DecisionRemux || !d.VideoCopy || d.AudioCopy || d.AudioCodec != "aac" {
			t.Fatalf("rule on: want video copy + AAC, got %+v", d)
		}
	}
}
