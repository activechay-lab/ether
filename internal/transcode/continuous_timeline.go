package transcode

import "time"

// Short opening segments reduce cold tune time without disabling mbtree or
// lookahead. All cuts are integer frames: 1,1,2, then4 seconds at25/30fps;
// 30000/1001 gives1.001,1.001,2.002, then4.004 seconds.
func continuousSegmentFrames(f RawFormat, segment time.Duration, seq int64) (start, frames int64) {
	if segment <= 0 {
		segment = 4 * time.Second
	}
	g := (int64(segment)*int64(f.FPSNum) + int64(time.Second)*int64(f.FPSDen)/2) / (int64(time.Second) * int64(f.FPSDen))
	quarter := g / 4
	switch seq {
	case 0:
		return 0, quarter
	case 1:
		return quarter, quarter
	case 2:
		return 2 * quarter, g - 2*quarter
	default:
		return g + (seq-3)*g, g
	}
}
func continuousFrameTime(f RawFormat, frames int64) time.Duration {
	return time.Duration(frames*int64(f.FPSDen)) * time.Second / time.Duration(f.FPSNum)
}
func (s *continuousSegmentIndex) segment(seq int64) Segment {
	start, frames := continuousSegmentFrames(s.enc.req.Source, s.enc.req.SegmentDuration, seq)
	return Segment{Seq: seq, Start: s.start.Add(continuousFrameTime(s.enc.req.Source, start)), Duration: continuousFrameTime(s.enc.req.Source, frames)}
}
