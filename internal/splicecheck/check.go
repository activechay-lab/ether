// Package splicecheck verifies offline H.264/AAC HLS recovery artifacts using
// explicit FFmpeg tools. It never grants runtime publication authority.
package splicecheck

import (
 "context"
 "fmt"
 "time"
)

type Segment struct {
 Index int
 Path string
 Generation uint64
 // InitPath is required for fragmented MP4; each generation may use its own init.
 InitPath string `json:",omitempty"`
}
type Options struct {
 SegmentDuration time.Duration
 Reference []Segment
 FFprobe, FFmpeg string
}
type Result struct {
 Pass bool
 Details string
}
type Boundary struct {
 Index int
 PreviousGeneration, Generation uint64
}
type Report struct {
 IDRStart, ClosedGOP, PTSContinuity, FrameCountVsReference Result
 CleanDecode, AudioAUExact, AudioContinuity, GridDurations Result
 Boundaries []Boundary
 Segments, VideoFrames, AudioAUs int
}

func Check(ctx context.Context, segs []Segment, o Options) (Report, error) {
 return check(ctx, segs, o)
}

func check(ctx context.Context, segs []Segment, o Options) (Report, error) {
 return Report{}, fmt.Errorf("splicecheck: verification implementation pending")
}
