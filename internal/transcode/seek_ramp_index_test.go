package transcode

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestSeekRampIndexRequiresProvenHandover(t *testing.T) {
	data := []byte(`{"format":{"start_time":"10"},"packets":[{"pts_time":"30","pos":"100","flags":"K_"},{"pts_time":"34","pos":"200","flags":"K_"},{"pts_time":"38","pos":"300","flags":"K_"}]}`)
	points := parseSeekRampIndex(data, 22500*time.Millisecond, 4*time.Second)
	if len(points) != 3 || points[2].Time != 28*time.Second {
		t.Fatalf("points: %+v", points)
	}
	plan := buildTimeline(time.Minute, 4*time.Second, timelineOptions{steadyMode: ModeCopy, fastStart: true, rampSegs: 2, rampDur: 2 * time.Second, keyframes: points, base: 22500 * time.Millisecond, snap: true})
	if got := plan.Start(plan.SwitchSeg); got != 5500*time.Millisecond {
		t.Fatalf("ramp handover %v, want 28s-22.5s", got)
	}
	if got := parseSeekRampIndex(data, 35*time.Second, 4*time.Second); len(got) != 0 {
		t.Fatalf("no future keyframe: %+v", got)
	}
}

func TestSampledCopyPeakCountsRepeatedGOPBytes(t *testing.T) {
	points := []core.KeyframePoint{{Time: 0, Offset: 0}, {Time: 10 * time.Second, Offset: 1000000}, {Time: 20 * time.Second, Offset: 2000000}}
	// A nominal four-second alias carries a whole ten-second GOP. Its
	// served rate is2Mbit/s, not the source's800kbit/s average.
	if got := sampledCopyPeakKbps(points, 4*time.Second); got != 2216 {
		t.Fatalf("alias peak %d", got)
	}
}

func TestCopySampleTailIsNotAHandoverKeyframe(t *testing.T) {
	data := []byte(`{"format":{"start_time":"0"},"packets":[{"pts_time":"0","duration_time":"0.04","size":"100","pos":"0","flags":"K_"},{"pts_time":"9.96","duration_time":"0.04","size":"10000","pos":"900000","flags":"__"}]}`)
	raw := parseCopyPacketIndex(data)
	peak := copySamplePeakPoints(data, core.MediaInfo{Duration: 10 * time.Second})
	if len(raw) != 1 || len(peak) != 2 {
		t.Fatalf("raw=%+v peak=%+v", raw, peak)
	}
	if hasSampledRampBoundary(raw, time.Second, 4*time.Second) {
		t.Fatal("invented keyframe at EOF")
	}
	if got := sampledCopyPeakKbps(peak, 4*time.Second); got != 2018 {
		t.Fatalf("tail peak %d", got)
	}
}
