package transcode

import (
	"math"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// boundHLSRateLocked applies after ramp, clamp and rung decisions are resolved.
// The manifest peak includes audio and mux overhead; it cannot also be the
// video's maxrate plus an unrestricted eight-second reservoir. Fit both the
// nominal video rate and reservoir into the published peak measurement window.
func (s *Session) boundHLSRateLocked(rp *runPlan) {
	if s.proto != core.ProtocolHLS || s.liveInput || rp.decision.VideoCopy ||
		(s.container != ContainerMPEGTS && s.container != ContainerFMP4) || s.wantTrack < 0 || s.wantTrack >= len(s.rungs) {
		return
	}
	rp.rate = boundedHLSRate(s.info, rp.decision, rp.rate, s.rungPeakKbpsLocked(s.wantTrack), s.hlsPeakWindowLocked())
}

func boundedHLSRate(info core.MediaInfo, d core.Decision, rate rateControl, peak int, window time.Duration) rateControl {
	if peak <= 16 || window <= 0 {
		return rate
	}
	// The same 10% mux and 16kbit/s allowance used by encodedHLSPeakKbps.
	payload := float64(peak-16)*10/11 - float64(decisionAudioKbps(info, d))
	if payload <= 0 {
		return rate
	}
	video := videoBitrate(info, d)
	if rate.MaxrateKbps > 0 {
		video = min(video, rate.MaxrateKbps)
	}
	fps := 24.0
	if v := info.Video(); v != nil && v.FrameRate > 0 {
		fps = v.FrameRate
	}
	// x264 must have room for at least one frame. If necessary lower the
	// maxrate within the same budget instead of letting it enlarge the buffer.
	video = min(video, int(math.Floor(payload/(1+1/(fps*window.Seconds())))))
	if video <= 0 {
		return rate
	}
	buffer := int(math.Floor((payload - float64(video)) * window.Seconds()))
	existingBuffer := rate.BufsizeKbps
	if existingBuffer <= 0 {
		existingBuffer = videoBitrate(info, d) * 2
	}
	buffer = min(buffer, existingBuffer)
	if buffer <= 0 {
		return rate
	}
	rate.TargetKbps, rate.MaxrateKbps, rate.BufsizeKbps = video, video, buffer
	return rate
}
