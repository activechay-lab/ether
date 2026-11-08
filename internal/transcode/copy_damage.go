package transcode

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// copyDamage samples before publishing the immutable VOD playlist or init.
// Only positive decoder evidence changes eligibility. Slow/unreadable samples
// retain copy: a timeout is not evidence of damaged media. This is deliberately
// not a full-title integrity check and cannot detect later, unsampled damage.
func (m *Manager) copyDamage(ctx context.Context, req core.HLSRequest, d core.Decision, life *encoderLifetime) (bool, bool) {
	v := req.Info.Video()
	if req.LiveInput || !d.VideoCopy || v == nil || v.Codec != "h264" || encodedRange(v) != rangeSDR || req.Resume != nil || req.Ramp.Version != 0 || ValidCopyTimeline(req) {
		return false, false
	}
	headers, err := encoderScopeHeaders(req.InputLease, req.InputURL)
	if err != nil {
		return false, false
	}
	spec := runSpec{InputHeaders: req.InputHeaders, InputScopeHeaders: headers}
	ctx, cancel := context.WithTimeout(ctx, 350*time.Millisecond)
	defer cancel()
	points := []time.Duration{max(0, req.StartOffset)}
	if req.Info.Duration > points[0]+4*time.Second {
		points = append(points, points[0]+(req.Info.Duration-points[0])/2)
	}
	for _, at := range points {
		if ctx.Err() != nil {
			return false, false
		}
		args := []string{"-hide_banner", "-v", "warning", "-nostdin", "-threads", "1", "-probesize", "1048576", "-analyzeduration", "500000"}
		args = append(args, inputSafetyArgs(req.InputURL, false)...)
		args = append(args, httpInputArgs(req.InputURL, false)...)
		args = appendInputHeaders(args, spec.commandHeaders())
		args = append(args, "-ss", secs(at), "-i", req.InputURL, "-map", fmt.Sprintf("0:%d", v.Index), "-an", "-sn", "-frames:v", "24", "-threads", "1", "-f", "null", "-")
		evidence := &copyDamageEvidence{}
		work, err := life.reserve(1)
		if err != nil {
			return false, false
		}
		life.markStarted()
		err = m.runner.Run(ctx, Command{Path: m.ffmpeg, Args: args, Stderr: evidence, Stdout: io.Discard, Background: true})
		work[0].finish()
		// Failed opens/probes and aborted decodes never trigger conversion.
		if err == nil && ctx.Err() == nil && (evidence.damaged() || (req.Info.Container == "mpegts" && evidence.clockJump())) {
			return true, req.Info.Container == "mpegts" && evidence.clockJump()
		}
	}
	return false, false
}

// Keep bounded decoder diagnostics, never source URLs or errors in logs.
// Generic demux warnings, timestamp jumps and timeout text aren't corruption.
type copyDamageEvidence struct{ text string }

func (b *copyDamageEvidence) Write(p []byte) (int, error) {
	n := len(p)
	if len(b.text) < 16384 {
		b.text += string(p[:min(len(p), 16384-len(b.text))])
	}
	return n, nil
}
func (b *copyDamageEvidence) damaged() bool {
	return strings.Contains(b.text, "error while decoding MB") &&
		(strings.Contains(b.text, "corrupted macroblock") || strings.Contains(b.text, "corrupt decoded frame"))
}

// ffmpeg's MPEG-TS demuxer explicitly diagnoses clock corrections larger
// than its normal ten-second discontinuity threshold. Preserve this evidence
// separately from damaged pictures; encoding alone would retain -copyts gaps.
func (b *copyDamageEvidence) clockJump() bool {
	for _, line := range strings.Split(b.text, "\n") {
		if !strings.Contains(line, "timestamp discontinuity (stream id=") {
			continue
		}
		_, rest, ok := strings.Cut(line, "): ")
		if !ok {
			continue
		}
		value, _, ok := strings.Cut(rest, ", new offset=")
		if !ok {
			continue
		}
		delta, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err == nil && (delta > 10000000 || delta < -10000000) {
			return true
		}
	}
	return false
}
