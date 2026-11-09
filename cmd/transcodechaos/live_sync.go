package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"math"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
)

// Lip-sync measurement for rows on the "sync" fixture. Every whole second of
// fixture time a white flash fills a black box in the bottom-right corner and
// a 3 kHz beep starts over the 440 Hz tone. The judge finds both onsets in
// the published media, on the media's own timestamps, and reports the
// audio-minus-video offset of every pulse. ITU-R BT.1359 puts detectability
// at audio 45 ms early or 125 ms late; outside that a row fails.

const (
	syncAudioEarlyMs = 45.0
	syncAudioLateMs  = 125.0
)

type syncSample struct {
	t, v float64
}

type avPulse struct {
	Seq int64   `json:"seq"`
	At  float64 `json:"at"` // judge time the segment was listed
	Ms  float64 `json:"offsetMs"`
}

type avSyncReport struct {
	Pulses   int     `json:"pulses"`
	Unpaired int     `json:"unpairedFlashes"`
	MedianMs float64 `json:"medianMs"`
	MinMs    float64 `json:"minMs"`
	MaxMs    float64 `json:"maxMs"`
	// Cuts are flash intervals that are not a whole number of seconds: the
	// published content skipped or repeated fixture time there (a freeze or
	// screen that later rejoins on time only drops whole pulses).
	Cuts    []string  `json:"cuts,omitempty"`
	Offsets []avPulse `json:"offsets,omitempty"`
}

var (
	rePtsTime = regexp.MustCompile(`pts_time:([-0-9.e+]+)`)
	reYAvg    = regexp.MustCompile(`lavfi\.signalstats\.YAVG=([-0-9.]+)`)
	reRMS     = regexp.MustCompile(`lavfi\.astats\.Overall\.RMS_level=([-0-9.a-z]+)`)
)

// analyseSync returns per-frame brightness of the pulse box and per-10 ms
// 3 kHz band level, each on the segment's own (absolute) timestamps.
func analyseSync(ctx context.Context, ff, file string) (video, audio []syncSample) {
	video = metadataSeries(ctx, ff, file, []string{"-map", "0:v:0", "-vf", "crop=iw/16:ih/16:iw*29/32:ih*29/32,signalstats,metadata=print:key=lavfi.signalstats.YAVG"}, reYAvg)
	audio = metadataSeries(ctx, ff, file, []string{"-map", "0:a:0", "-af", "highpass=f=2000,highpass=f=2000,lowpass=f=4500,asetnsamples=n=480:p=0,astats=metadata=1:reset=1:measure_perchannel=none:measure_overall=RMS_level,ametadata=print:key=lavfi.astats.Overall.RMS_level"}, reRMS)
	return
}

func metadataSeries(ctx context.Context, ff, file string, filter []string, value *regexp.Regexp) []syncSample {
	args := append([]string{"-hide_banner", "-nostats", "-loglevel", "info", "-copyts", "-i", file}, filter...)
	args = append(args, "-f", "null", "-")
	cmd := exec.CommandContext(ctx, ff, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if cmd.Run() != nil {
		return nil
	}
	var out []syncSample
	t := math.NaN()
	sc := bufio.NewScanner(&stderr)
	for sc.Scan() {
		line := sc.Text()
		if m := rePtsTime.FindStringSubmatch(line); m != nil {
			t, _ = strconv.ParseFloat(m[1], 64)
			continue
		}
		if m := value.FindStringSubmatch(line); m != nil && !math.IsNaN(t) {
			v, err := strconv.ParseFloat(m[1], 64)
			if err != nil || math.IsInf(v, 0) {
				v = -120 // astats prints -inf for digital silence
			}
			out = append(out, syncSample{t, v})
			t = math.NaN()
		}
	}
	return out
}

// onsets finds rising edges: the first sample above lo right after one at or
// below lo (no more than gap apart, so a timestamp jump is not an edge) that
// reaches hi within the next few samples. A window straddling the edge sits
// between lo and hi, so it counts as the onset rather than hiding it.
func onsets(s []syncSample, lo, hi, gap float64) []float64 {
	var out []float64
	for i := 1; i < len(s); i++ {
		if s[i].v <= lo || s[i-1].v > lo || s[i].t <= s[i-1].t || s[i].t-s[i-1].t > gap {
			continue
		}
		for j := i; j < len(s) && j <= i+3 && s[j].t-s[i].t <= 4*gap; j++ {
			if s[j].v >= hi {
				out = append(out, s[i].t)
				break
			}
		}
	}
	return out
}

// avSync pairs each flash with the nearest beep within half a second.
func avSync(segs []*judgedSegment) *avSyncReport {
	var video, audio []syncSample
	type span struct {
		lo, hi float64
		seq    int64
		at     float64
	}
	var spans []span
	for _, s := range segs {
		video = append(video, s.syncVideo...)
		audio = append(audio, s.syncAudio...)
		if len(s.syncVideo) > 0 {
			spans = append(spans, span{s.syncVideo[0].t, s.syncVideo[len(s.syncVideo)-1].t, s.Seq, s.SeenAt})
		}
	}
	seqAt := func(t float64) (int64, float64) {
		for _, sp := range spans {
			if t >= sp.lo && t <= sp.hi+0.05 {
				return sp.seq, sp.at
			}
		}
		return -1, 0
	}
	flashes := onsets(video, 80, 150, 0.1)
	beeps := onsets(audio, -35, -25, 0.05)
	sort.Float64s(beeps)
	r := &avSyncReport{}
	for i := 1; i < len(flashes); i++ {
		d := flashes[i] - flashes[i-1]
		if d > 0 && d < 10 && math.Abs(d-math.Round(d)) > 0.1 {
			seq, at := seqAt(flashes[i])
			r.Cuts = append(r.Cuts, fmt.Sprintf("seq %d (%.0fs): pulses %.2fs apart", seq, at, d))
		}
	}
	var ms []float64
	for _, f := range flashes {
		i := sort.SearchFloat64s(beeps, f)
		best := math.Inf(1)
		for _, j := range []int{i - 1, i} {
			if j >= 0 && j < len(beeps) && math.Abs(beeps[j]-f) < math.Abs(best) {
				best = beeps[j] - f
			}
		}
		if math.Abs(best) > 0.5 {
			r.Unpaired++
			continue
		}
		seq, at := seqAt(f)
		v := math.Round(best*1000*10) / 10
		r.Offsets = append(r.Offsets, avPulse{Seq: seq, At: math.Round(at*10) / 10, Ms: v})
		ms = append(ms, v)
	}
	r.Pulses = len(ms)
	if len(ms) == 0 {
		return r
	}
	sorted := append([]float64(nil), ms...)
	sort.Float64s(sorted)
	r.MedianMs, r.MinMs, r.MaxMs = sorted[len(sorted)/2], sorted[0], sorted[len(sorted)-1]
	return r
}

// syncVerdict is the failure reason for a sync row, or "".
func syncVerdict(r *avSyncReport) string {
	if r == nil || r.Pulses == 0 {
		return "no lip-sync pulses measured in the published media"
	}
	for _, p := range r.Offsets {
		if p.Ms < -syncAudioEarlyMs || p.Ms > syncAudioLateMs {
			side := "late"
			if p.Ms < 0 {
				side = "early"
			}
			return fmt.Sprintf("A/V sync: audio %.0f ms %s at seq %d (%.0fs); limits %.0f ms early / %.0f ms late", math.Abs(p.Ms), side, p.Seq, p.At, syncAudioEarlyMs, syncAudioLateMs)
		}
	}
	return ""
}
