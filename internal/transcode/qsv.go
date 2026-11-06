package transcode

import (
	"context"
	"strings"

	"github.com/braedonsaunders/ether/internal/core"
)

const (
	qsvLA  = "la_icq"
	qsvICQ = "icq"
	qsvVBR = "vbr"
)

func qsvModes(s runSpec, enc string) []string {
	if s.Rate.Quality {
		if modes := s.Accel.QSVQualityModes[enc]; len(modes) > 0 {
			return modes
		}
		// Unprobed callers must not assume look-ahead support.
		return []string{qsvICQ, qsvVBR}
	}
	if modes := s.Accel.QSVBitrateModes[enc]; len(modes) > 0 {
		return modes
	}
	return []string{qsvVBR}
}

func qsvMode(s runSpec, enc string) string {
	if s.QSVMode != "" {
		return s.QSVMode
	}
	return qsvModes(s, enc)[0]
}

func qsvRateArgs(mode, bitrate, maxrate, buffer string) []string {
	args := []string{"-preset", "veryfast"}
	if mode == qsvVBR {
		args = append(args, "-b:v", bitrate)
	} else {
		args = append(args, "-global_quality", "23")
	}
	args = append(args, "-maxrate", maxrate, "-bufsize", buffer)
	if mode == qsvLA {
		args = append(args, "-look_ahead", "1")
	}
	return args
}

// Use the playback encoder planner, including profile, colour tags and VBV,
// rather than a second approximation of its rate-control flags. The synthetic
// picture fixes only input size/rate; real media can still need a runtime retry.
func qsvProbeArgs(device, enc, mode string) []string {
	codec := strings.TrimSuffix(enc, "_qsv")
	spec := runSpec{QSVMode: mode, Decision: core.Decision{VideoCodec: codec},
		Rate: rateControl{Quality: mode != qsvVBR, TargetKbps: 1800, MaxrateKbps: 2400, BufsizeKbps: 19200}}
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	if device != "" {
		args = append(args, "-qsv_device", device)
	}
	args = append(args, "-f", "lavfi", "-i", "testsrc=size=1280x720:rate=24", "-frames:v", "1", "-vf", "format=nv12")
	args = append(args, planEncodeArgs(spec, picturePlan{w: 1280, h: 720}, enc, codec, false)...)
	return append(args, "-f", "null", "-")
}

func (d hwDetector) detectQSV(ctx context.Context, device string, f features) core.HWAccel {
	hw := core.HWAccel{Name: AccelQSV, Device: device,
		QSVQualityModes: map[string][]string{}, QSVBitrateModes: map[string][]string{}}
	for _, enc := range []string{"h264_qsv", "hevc_qsv", "av1_qsv"} {
		if !f.hasEncoder(enc) {
			continue
		}
		for _, mode := range []string{qsvLA, qsvICQ, qsvVBR} {
			if err := d.testRun(ctx, qsvProbeArgs(device, enc, mode)); err == nil {
				hw.QSVQualityModes[enc] = append(hw.QSVQualityModes[enc], mode)
			} else {
				d.log.Debug("QSV rate control probe failed", "device", device, "encoder", enc, "mode", mode, "err", err)
			}
		}
		modes := hw.QSVQualityModes[enc]
		if len(modes) == 0 {
			continue
		}
		// Explicit bitrate requests prefer capped VBR. Keep only modes that
		// passed the same planner above, with quality modes as a last resort.
		for _, mode := range []string{qsvVBR, qsvICQ, qsvLA} {
			if contains(modes, mode) {
				hw.QSVBitrateModes[enc] = append(hw.QSVBitrateModes[enc], mode)
			}
		}
		hw.Encoders = append(hw.Encoders, enc)
		d.log.Info("QSV rate control mode selected", "device", device, "encoder", enc, "qualityMode", modes[0], "bitrateMode", hw.QSVBitrateModes[enc][0])
	}
	return hw
}

func qsvParameterError(stderr string) bool {
	l := strings.ToLower(stderr)
	for _, marker := range []string{"invalid video parameters", "unsupported rate control", "unsupported video parameter", "encoding parameters are not supported", "ratecontrol mode is unsupported", "ratecontrol mode is not supported", "error while opening encoder", "error initializing the encoder", "invalid parameter"} {
		if strings.Contains(l, marker) {
			return true
		}
	}
	return false
}

// Called under the session lock, before both live and VOD hardware fallback.
// One retry per session bounds startup time, including later seeks/restarts.
func (s *Session) retryQSVLocked(r *run) bool {
	if s.qsvRetried || s.forceCPU || r.stalled || r.produced != 0 || !r.hw || r.spec.Accel.Name != AccelQSV || !qsvParameterError(r.stderr.String()) {
		return false
	}
	enc, _ := videoEncoder(r.spec.Decision.VideoCodec, r.spec.Accel, r.spec.Features)
	modes := qsvModes(r.spec, enc)
	current := qsvMode(r.spec, enc)
	next := ""
	for i, mode := range modes {
		if mode == current && i+1 < len(modes) {
			next = modes[i+1]
			break
		}
	}
	// A driver can reject the startup sample but accept a real stream (or
	// the reverse). Always try a simpler QSV mode before leaving quality
	// encoding for software, even if the sample had rejected that mode.
	if next == "" {
		switch current {
		case qsvLA:
			next = qsvICQ
		case qsvICQ:
			next = qsvVBR
		}
	}
	if next == "" {
		return false
	}
	s.qsvRetried = true
	s.qsvMode = next
	s.qsvCodec = r.spec.Decision.VideoCodec
	s.qsvQuality = r.spec.Rate.Quality
	s.log.Warn("QSV parameter failure; retrying rate control", "encoder", enc, "from", current, "mode", next)
	if err := s.startRunForLocked(r.startSeg, r.spec.BigProbe, "QSV rate control retry"); err != nil {
		s.failErr = err
	}
	return true
}
