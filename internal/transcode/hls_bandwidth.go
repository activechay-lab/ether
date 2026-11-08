package transcode

import (
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// HLS BANDWIDTH is the variant's peak segment bit rate (RFC 8216 4.3.4.2);
// AVPlayer fails a segment that exceeds it (-12318). A video copy's segments
// carry the source's own bit rate swings, so the container average is not
// a bound.
//
// A copied segment holds whole GOPs (keyframe-planned or chunked runs), so
// its bit rate is at most that of the densest run of GOPs about a segment
// long. With a keyframe index covering the title that run is measured from
// the index's byte offsets (audio and container overhead included); MPEG-TS
// packetisation adds up to about a tenth on top. Without one (or with a
// truncated one) a GOP can be longer than a segment and a segment inside it
// repeats the GOP, so the advertisement keeps a generous margin over the
// average.
const (
	copyPeakWindowMin = 2 * time.Second
	defaultHLSSegment = 4 * time.Second
)

// HLSCopyPeakKbps is the BANDWIDTH, in kbit/s, of an HLS variant copying the
// source video, for segments of segDur (0: the default). 0 when the source
// bit rate is unknown.
func HLSCopyPeakKbps(info core.MediaInfo, segDur time.Duration) int {
	avg := info.BitrateKbps
	if avg <= 0 {
		return 0
	}
	if segDur <= 0 {
		segDur = defaultHLSSegment
	}
	fallback := avg*3/2 + 64
	peak, ok := indexedPeakKbps(info, max(copyPeakWindowMin, segDur/2))
	if !ok {
		return fallback
	}
	return max(peak, avg)*11/10 + 16
}

// indexedPeakKbps is the highest bit rate over runs of consecutive GOPs
// spanning at least window, from a keyframe index that covers the title.
func indexedPeakKbps(info core.MediaInfo, window time.Duration) (int, bool) {
	kf := info.Keyframes
	if len(kf) < 2 || info.Duration <= 0 {
		return 0, false
	}
	// The index must reach the title's last GOPs; a truncated one says
	// nothing about the rest.
	if info.Duration-kf[len(kf)-1].Time > max(2*window, 10*time.Second) {
		return 0, false
	}
	// Bytes after the last keyframe may hold an index (an MP4 moov at the
	// end), so only spans between keyframes are measured.
	points := kf
	peak := 0.0
	j := 0
	for i := 0; i < len(points)-1; i++ {
		if j <= i {
			j = i + 1
		}
		for j < len(points)-1 && points[j].Time-points[i].Time < window {
			j++
		}
		span := points[j].Time - points[i].Time
		bytes := points[j].Offset - points[i].Offset
		if span <= 0 || bytes <= 0 {
			continue
		}
		if kbps := float64(bytes) * 8 / span.Seconds() / 1000; kbps > peak {
			peak = kbps
		}
	}
	if peak <= 0 {
		return 0, false
	}
	return int(peak + 0.5), true
}
