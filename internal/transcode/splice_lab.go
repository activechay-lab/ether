//go:build splicelab

package transcode

import (
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

// SpliceLabOptions selects a literal negotiated plan in the offline lab.
type SpliceLabOptions struct {
	H264Level                  int
	H264FrameRate              float64
	BitrateKbps, AudioChannels int
	ResumeFirst                int
	ReadRate                   float64
}

func spliceLabDecision(container string, copyVideo, copyAudio bool, o SpliceLabOptions) core.Decision {
	return core.Decision{Kind: core.DecisionTranscode, VideoCopy: copyVideo, AudioCopy: copyAudio, AudioChannels: o.AudioChannels, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, Container: container, MaxHeight: 360, BitrateKbps: o.BitrateKbps, SubtitleStream: -1, H264Level: o.H264Level, H264FrameRate: o.H264FrameRate}
}

// SpliceLabArgs exposes the shipping argument builder only in the offline lab.
func SpliceLabArgs(input, dir string, info core.MediaInfo, container string, first int, copyVideo, copyAudio bool, o SpliceLabOptions) []string {
	return buildArgs(runSpec{Input: input, Dir: dir, Info: info, Decision: spliceLabDecision(container, copyVideo, copyAudio, o), Start: time.Duration(first) * 4 * time.Second, StartSeg: first, SegDur: 4 * time.Second, Playlist: "index.m3u8", InitName: "init.mp4", Protocol: core.ProtocolHLS})
}
