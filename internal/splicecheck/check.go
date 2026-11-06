// Package splicecheck verifies offline H.264/AAC HLS recovery artifacts using
// explicit FFmpeg tools. It never grants runtime publication authority.
package splicecheck

import (
	"context"
	"time"
)

type Segment struct {
	Index      int
	Path       string
	Generation uint64
	// InitPath is required for fragmented MP4; each generation may use its own init.
	InitPath string `json:",omitempty"`
}
type Options struct {
	// Timeline contains title-time boundaries, indexed by Segment.Index, including
	// the terminal end boundary. When present it replaces SegmentDuration.
	ReencodedAudio  bool // verify PCM continuity instead of compressed AU equality
	Timeline        []time.Duration
	SegmentDuration time.Duration
	Reference       []Segment
	FFprobe, FFmpeg string
}
type Result struct {
	Pass    bool
	Details string
}
type Boundary struct {
	Index                          int
	PreviousGeneration, Generation uint64
}
type Report struct {
	IDRStart, ClosedGOP, PTSContinuity, FrameCountVsReference Result
	CleanDecode, AudioAUExact, AudioContinuity, GridDurations Result
	PCMContinuous                                             Result
	PCMBoundaries                                             []PCMMeasurement
	Boundaries                                                []Boundary
	Segments, VideoFrames, AudioAUs                           int
}

func Check(ctx context.Context, segs []Segment, o Options) (Report, error) {
	return check(ctx, segs, o)
}

// PCMMeasurement reports decoded audio at an execution handoff. Samples count
// per channel; AAC priming is represented by packet PTS, never a fitted shift.
type PCMMeasurement struct {
	Index                                 int
	MissingSamples, OverlappingSamples    int
	SNRDB, BoundaryStep, ReferenceMaxStep float64
}
