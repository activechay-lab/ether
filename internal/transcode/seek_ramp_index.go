package transcode

import (
	"context"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// sampleCopyIndex samples packet headers, never decoded pictures, before the
// first playlist is emitted. A single sample serves peak estimates and seeks.
// Only a sample proven to cover the known title is a full-file index.
// A partial sample must not be used as a full-title peak bound.
func (m *Manager) sampleCopyIndex(ctx context.Context, req core.HLSRequest, at time.Duration, life *encoderLifetime) copyPacketSample {
	if m.ffprobe == "" || req.Info.Video() == nil {
		return copyPacketSample{}
	}
	headers, err := encoderScopeHeaders(req.InputLease, req.InputURL)
	if err != nil {
		return copyPacketSample{}
	}
	spec := runSpec{Input: req.InputURL, InputHeaders: req.InputHeaders, InputScopeHeaders: headers}
	budget := m.opts.copySamplePackets
	if budget <= 0 {
		budget = copySamplePackets
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-v", "error"}
	args = append(args, inputSafetyArgs(spec.Input, false)...)
	args = append(args, httpInputArgs(spec.Input, false)...)
	args = appendInputHeaders(args, spec.commandHeaders())
	args = append(args, "-select_streams", strconv.Itoa(req.Info.Video().Index), "-read_intervals", "+"+secs(at)+"%+#"+strconv.Itoa(budget), "-show_entries", "packet=pts_time,duration_time,size,pos,flags:format=start_time", "-of", "json=compact=1", "-i", spec.Input)
	var out cappedProbeBuffer
	if m.runEncoderPreflight(ctx, life, Command{Path: m.ffprobe, Args: args, Stdout: &out}) != nil || ctx.Err() != nil {
		return copyPacketSample{}
	}
	data := out.buf.Bytes()
	return parseCopyPacketSample(data, req.Info, budget)
}

func parseCopyPacketIndex(data []byte) []core.KeyframePoint {
	var report struct {
		Packets []struct {
			PTS   string `json:"pts_time"`
			Pos   string `json:"pos"`
			Flags string `json:"flags"`
		} `json:"packets"`
		Format struct {
			Start string `json:"start_time"`
		} `json:"format"`
	}
	if json.Unmarshal(data, &report) != nil {
		return nil
	}
	origin, _ := strconv.ParseFloat(report.Format.Start, 64)
	if math.IsNaN(origin) || math.IsInf(origin, 0) {
		return nil
	}
	var points []core.KeyframePoint
	for _, p := range report.Packets {
		if !strings.Contains(p.Flags, "K") {
			continue
		}
		pts, e1 := strconv.ParseFloat(p.PTS, 64)
		pos, e2 := strconv.ParseInt(p.Pos, 10, 64)
		if e1 != nil || e2 != nil || pos < 0 || math.IsNaN(pts) || math.IsInf(pts, 0) || math.Abs(pts-origin) > float64(math.MaxInt64)/float64(time.Second) {
			continue
		}
		t := time.Duration((pts - origin) * float64(time.Second))
		if t < 0 {
			continue
		}
		points = append(points, core.KeyframePoint{Time: t, Offset: pos})
	}
	if len(points) == 0 {
		return nil
	}
	sort.Slice(points, func(i, j int) bool { return points[i].Time < points[j].Time })
	for i := 1; i < len(points); i++ {
		if points[i].Time <= points[i-1].Time || points[i].Offset <= points[i-1].Offset {
			return nil
		}
	}
	return points
}

func hasSampledRampBoundary(points []core.KeyframePoint, at, nominal time.Duration) bool {
	for _, p := range points {
		if p.Time >= at+nominal && p.Time <= at+nominal+3*DefaultSegmentDuration {
			return true
		}
	}
	return false
}

func parseSeekRampIndex(data []byte, at, nominal time.Duration) []core.KeyframePoint {
	points := parseCopyPacketIndex(data)
	if !hasSampledRampBoundary(points, at, nominal) {
		return nil
	}
	return points
}

// sampledCopyPeakKbps estimates served rate from observed whole GOP byte
// spans. Long GOPs repeated in nominal slots divide by the slot duration,
// not the GOP duration. This is representative evidence, not a promise
// about unobserved later scenes (RFC8216 section4.3.4.2).
func sampledCopyPeakKbps(points []core.KeyframePoint, slot time.Duration) int {
	if slot <= 0 {
		slot = DefaultSegmentDuration
	}
	peak := 0.0
	for i := 0; i+1 < len(points); i++ {
		j := i + 1
		for j+1 < len(points) && points[j].Time-points[i].Time < slot {
			j++
		}
		bytes := points[j].Offset - points[i].Offset
		if bytes <= 0 {
			continue
		}
		rate := float64(bytes) * 8 / slot.Seconds() / 1000
		peak = max(peak, rate)
	}
	if peak == 0 {
		return 0
	}
	return int(math.Ceil(peak*11/10)) + 16
}

// copySamplePackets is the copy sample's packet budget: about 40 s of
// 24 fps video, so only short titles are indexed whole by it.
const copySamplePackets = 1024

type copyPacketSample struct {
	points     []core.KeyframePoint
	peakPoints []core.KeyframePoint
	complete   bool // verified beginning and known end within the packet budget
}

func parseCopyPacketSample(data []byte, info core.MediaInfo, budget int) copyPacketSample {
	sample := copyPacketSample{points: parseCopyPacketIndex(data), peakPoints: copySamplePeakPoints(data, info)}
	var report struct {
		Packets []struct {
			Flags string `json:"flags"`
		} `json:"packets"`
	}
	// An appended endpoint proves the observed tail, but is never itself
	// a keyframe. Require the opening keyframe and an unexhausted packet
	// budget too; near-tail samples cannot establish a complete index.
	if json.Unmarshal(data, &report) != nil {
		return sample
	}
	keyframes := 0
	for _, p := range report.Packets {
		if strings.Contains(p.Flags, "K") {
			keyframes++
		}
	}
	sample.complete = keyframes == len(sample.points) && len(report.Packets) < budget &&
		len(sample.points) > 0 && sample.points[0].Time <= 100*time.Millisecond &&
		len(sample.peakPoints) == len(sample.points)+1
	return sample
}

// A sample reaching the known tail may include its final GOP even though
// there is no following keyframe. The synthetic endpoint is ONLY for byte
// measurement, never a ramp-to-copy handover point.
func copySamplePeakPoints(data []byte, info core.MediaInfo) []core.KeyframePoint {
	points := parseCopyPacketIndex(data)
	if len(points) == 0 || info.Duration <= 0 {
		return points
	}
	var r struct {
		Packets []struct {
			PTS      string `json:"pts_time"`
			Duration string `json:"duration_time"`
			Pos      string `json:"pos"`
			Size     string `json:"size"`
		} `json:"packets"`
		Format struct {
			Start string `json:"start_time"`
		} `json:"format"`
	}
	if json.Unmarshal(data, &r) != nil {
		return points
	}
	origin, _ := strconv.ParseFloat(r.Format.Start, 64)
	end := 0.0
	var endOffset int64
	for _, p := range r.Packets {
		pts, e1 := strconv.ParseFloat(p.PTS, 64)
		dur, e2 := strconv.ParseFloat(p.Duration, 64)
		pos, e3 := strconv.ParseInt(p.Pos, 10, 64)
		size, e4 := strconv.ParseInt(p.Size, 10, 64)
		if e1 != nil || e2 != nil || e3 != nil || e4 != nil || pos < 0 || size <= 0 || pos > math.MaxInt64-size {
			continue
		}
		end = max(end, pts-origin+dur)
		endOffset = max(endOffset, pos+size)
	}
	if end >= info.Duration.Seconds()-0.1 && end <= info.Duration.Seconds()+1 && endOffset > points[len(points)-1].Offset && info.Duration > points[len(points)-1].Time {
		points = append(points, core.KeyframePoint{Time: info.Duration, Offset: endOffset})
	}
	return points
}
