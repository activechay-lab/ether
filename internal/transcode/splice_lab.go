//go:build splicelab

package transcode

import (
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

// SpliceLabArgs exposes the shipping argument builder only in the offline lab.
func SpliceLabArgs(input, dir string, info core.MediaInfo, container string, first int, copyVideo bool) []string {
	return buildArgs(runSpec{FixedGrid: true, Input: input, Dir: dir, Info: info, Decision: core.Decision{Kind: core.DecisionTranscode, VideoCopy: copyVideo, AudioCopy: true, VideoCodec: "h264", AudioCodec: "aac", AudioStream: 1, Container: container, MaxHeight: 360, BitrateKbps: 1200, SubtitleStream: -1}, Start: time.Duration(first) * 4 * time.Second, StartSeg: first, SegDur: 4 * time.Second, Playlist: "index.m3u8", InitName: "init.mp4", Protocol: core.ProtocolHLS})
}
