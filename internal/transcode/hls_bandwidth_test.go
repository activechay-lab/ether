package transcode

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A copied segment is a union of whole GOPs, so the densest run of GOPs
// about a segment long bounds every segment; without a full index the
// average gets a margin that covers segments repeating a long GOP.
func TestHLSCopyPeakKbps(t *testing.T) {
	// 2 s GOPs at 1000 kbit/s, except 4 s around 20 s at 3000 kbit/s.
	var kf []core.KeyframePoint
	off := int64(0)
	for at := time.Duration(0); at <= 60*time.Second; at += 2 * time.Second {
		kf = append(kf, core.KeyframePoint{Offset: off, Time: at})
		rate := int64(1000)
		if at >= 20*time.Second && at < 24*time.Second {
			rate = 3000
		}
		off += rate * 1000 / 8 * 2
	}
	info := core.MediaInfo{BitrateKbps: 1100, Duration: 61 * time.Second, Keyframes: kf}
	// 4 s segments: windows of two GOPs; the dense pair is 3000 kbit/s.
	if got, want := HLSCopyPeakKbps(info, 4*time.Second), 3000*11/10+16; got != want {
		t.Fatalf("indexed peak %d, want %d", got, want)
	}
	truncated := info
	truncated.Keyframes = kf[:10]
	if got, want := HLSCopyPeakKbps(truncated, 4*time.Second), 1100*3/2+64; got != want {
		t.Fatalf("truncated index %d, want the average margin %d", got, want)
	}
	// An unindexed TS (the Apple bipbop excerpt: 262 kbit/s average,
	// served 5 s segments measured at up to 287 kbit/s).
	if got := HLSCopyPeakKbps(core.MediaInfo{BitrateKbps: 262, Duration: 30 * time.Second}, 0); got < 287 {
		t.Fatalf("unindexed peak %d under the measured 287 kbit/s segments", got)
	}
}
