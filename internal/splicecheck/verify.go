package splicecheck

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

type packet struct {
	CodecType string `json:"codec_type"`
	PTS       string `json:"pts_time"`
	DTS       string `json:"dts_time"`
	Duration  string `json:"duration_time"`
	Hash      string `json:"data_hash"`
}
type frame struct {
	PTS string `json:"best_effort_timestamp_time"`
	Key int    `json:"key_frame"`
}
type observation struct {
	packets     []packet
	frames      []frame
	audio       []string
	first, last float64
}

func invoke(ctx context.Context, bin string, args ...string) ([]byte, error) {
	c := exec.CommandContext(ctx, bin, args...)
	var errout bytes.Buffer
	c.Stderr = &errout
	b, e := c.Output()
	if e != nil {
		return nil, fmt.Errorf("%s: %w: %.2000s", bin, e, errout.String())
	}
	// Error-level decoder diagnostics are failures even when a tool exits zero.
	if errout.Len() != 0 {
		return nil, fmt.Errorf("%s diagnostics: %.2000s", bin, errout.String())
	}
	return b, nil
}
func number(s string) (float64, error) {
	v, e := strconv.ParseFloat(s, 64)
	if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, fmt.Errorf("invalid timestamp %q", s)
	}
	return v, nil
}
func materialize(s Segment, dir string) (string, error) {
	f, e := os.CreateTemp(dir, "segment-*")
	if e != nil {
		return "", e
	}
	defer f.Close()
	for _, p := range []string{s.InitPath, s.Path} {
		if p == "" {
			continue
		}
		in, e := os.Open(p)
		if e != nil {
			return "", e
		}
		_, e = io.Copy(f, in)
		in.Close()
		if e != nil {
			return "", e
		}
	}
	return f.Name(), f.Close()
}
func observe(ctx context.Context, path string, o Options) (observation, error) {
	var x observation
	b, e := invoke(ctx, o.FFprobe, "-v", "error", "-show_packets", "-show_data_hash", "sha256", "-show_entries", "packet=codec_type,pts_time,dts_time,duration_time,data_hash", "-of", "json", path)
	if e != nil {
		return x, e
	}
	var p struct{ Packets []packet }
	if e = json.Unmarshal(b, &p); e != nil {
		return x, e
	}
	x.packets = p.Packets
	b, e = invoke(ctx, o.FFprobe, "-v", "error", "-select_streams", "v:0", "-show_frames", "-show_entries", "frame=key_frame,best_effort_timestamp_time", "-of", "json", path)
	if e != nil {
		return x, e
	}
	var f struct{ Frames []frame }
	if e = json.Unmarshal(b, &f); e != nil {
		return x, e
	}
	x.frames = f.Frames
	if len(x.frames) == 0 {
		return x, fmt.Errorf("no video frames")
	}
	x.first, e = number(x.frames[0].PTS)
	if e != nil {
		return x, e
	}
	x.last, e = number(x.frames[len(x.frames)-1].PTS)
	if e != nil {
		return x, e
	}
	// ADTS is a transport envelope, not part of the AAC raw access unit.
	b, e = invoke(ctx, o.FFmpeg, "-v", "error", "-i", path, "-map", "0:a:0", "-c:a", "copy", "-f", "adts", "pipe:1")
	if e != nil {
		return x, e
	}
	x.audio, e = adtsHashes(b)
	return x, e
}
func firstIDR(ctx context.Context, path string, o Options) error {
	b, e := invoke(ctx, o.FFmpeg, "-v", "error", "-i", path, "-map", "0:v:0", "-c:v", "copy", "-bsf:v", "h264_mp4toannexb", "-f", "h264", "pipe:1")
	if e != nil {
		return e
	}
	for i := 0; i+4 < len(b); i++ {
		if b[i] != 0 || b[i+1] != 0 || b[i+2] != 1 {
			continue
		}
		typ := b[i+3] & 31
		if typ >= 1 && typ <= 5 {
			if typ != 5 {
				return fmt.Errorf("first VCL NAL type %d, want IDR (5)", typ)
			}
			return nil
		}
	}
	return fmt.Errorf("no H.264 VCL NAL")
}
func validate(ctx context.Context, segs []Segment, o Options, dir string, r *Report) ([]observation, []string, error) {
	var obs []observation
	var paths []string
	for i, s := range segs {
		if i > 0 && s.Index != segs[i-1].Index+1 {
			return nil, nil, fmt.Errorf("segment index gap at %d", s.Index)
		}
		if i > 0 && s.Generation != segs[i-1].Generation {
			r.Boundaries = append(r.Boundaries, Boundary{s.Index, segs[i-1].Generation, s.Generation})
		}
		path, e := materialize(s, dir)
		if e != nil {
			return nil, nil, e
		}
		paths = append(paths, path)
		x, e := observe(ctx, path, o)
		if e != nil {
			return nil, nil, fmt.Errorf("segment %d: %w", s.Index, e)
		}
		if e = firstIDR(ctx, path, o); e != nil {
			r.IDRStart = Result{false, fmt.Sprintf("segment %d: %v", s.Index, e)}
			r.ClosedGOP = r.IDRStart
		}
		if _, e = invoke(ctx, o.FFmpeg, "-v", "error", "-xerror", "-i", path, "-map", "0:v:0", "-map", "0:a:0", "-f", "null", "-"); e != nil {
			r.CleanDecode = Result{false, fmt.Sprintf("segment %d: %v", s.Index, e)}
			r.ClosedGOP = r.CleanDecode
		}
		r.VideoFrames += len(x.frames)
		r.AudioAUs += len(x.audio)
		obs = append(obs, x)
	}
	return obs, paths, nil
}
func timing(obs []observation, segs []Segment, r *Report, o Options) {
	var lastVideo, lastAudio float64
	var haveVideo, haveAudio bool
	var lastFrame float64
	var haveFrame bool
	for i, x := range obs {
		// Compare display order as well as packet decode order: a closed GOP may reorder internally.
		for _, f := range x.frames {
			t, e := number(f.PTS)
			if e != nil {
				r.PTSContinuity = Result{false, e.Error()}
				continue
			}
			if haveFrame && t <= lastFrame {
				r.PTSContinuity = Result{false, "duplicated or regressing video frame PTS"}
			}
			lastFrame = t
			haveFrame = true
		}
		for _, p := range x.packets {
			t, e := number(p.DTS)
			if e != nil {
				r.PTSContinuity = Result{false, e.Error()}
				continue
			}
			if p.CodecType == "video" {
				if haveVideo && t <= lastVideo {
					r.PTSContinuity = Result{false, "duplicated or regressing video DTS"}
				}
				lastVideo = t
				haveVideo = true
			}
			if p.CodecType == "audio" {
				pts, e := number(p.PTS)
				dur, de := number(p.Duration)
				if e != nil || de != nil || dur <= 0 {
					r.AudioContinuity = Result{false, "missing audio packet clock/duration"}
					continue
				}
				if haveAudio && math.Abs(pts-lastAudio) > 0.000025 {
					r.AudioContinuity = Result{false, fmt.Sprintf("audio gap/overlap %.9fs before segment %d", pts-lastAudio, i)}
				}
				lastAudio = pts + dur
				haveAudio = true
			}
		}
		if i > 0 {
			expected := o.SegmentDuration.Seconds()
			if len(o.Timeline) > 0 {
				expected = (o.Timeline[segs[i].Index] - o.Timeline[segs[i-1].Index]).Seconds()
			}
			delta := x.first - obs[i-1].first
			tol := frameTolerance(obs[i-1])
			if math.Abs(delta-expected) > tol {
				r.GridDurations = Result{false, fmt.Sprintf("segment %d spacing %.9fs, want %.9fs (+/- %.9f)", i, delta, expected, tol)}
			}
		}
	}
}
func frameTolerance(x observation) float64 {
	var d []float64
	for i := 1; i < len(x.frames); i++ {
		a, _ := number(x.frames[i-1].PTS)
		b, _ := number(x.frames[i].PTS)
		if b > a {
			d = append(d, b-a)
		}
	}
	if len(d) == 0 {
		return 0.04
	}
	sort.Float64s(d)
	return d[len(d)/2] + 0.000025
}
func compare(a, b []observation, r *Report) {
	var af, bf []frame
	var aa, ba []string
	var av, bv []packet
	for _, x := range a {
		af = append(af, x.frames...)
		aa = append(aa, x.audio...)
		for _, p := range x.packets {
			if p.CodecType == "video" {
				av = append(av, p)
			}
		}
	}
	for _, x := range b {
		bf = append(bf, x.frames...)
		ba = append(ba, x.audio...)
		for _, p := range x.packets {
			if p.CodecType == "video" {
				bv = append(bv, p)
			}
		}
	}
	if len(af) != len(bf) {
		r.FrameCountVsReference = Result{false, fmt.Sprintf("candidate %d frames, reference %d", len(af), len(bf))}
	}
	for i := 0; i < min(len(af), len(bf)); i++ {
		x, _ := number(af[i].PTS)
		y, _ := number(bf[i].PTS)
		if math.Abs(x-y) > 0.000025 {
			r.PTSContinuity = Result{false, fmt.Sprintf("frame %d timestamp %.9f vs reference %.9f", i, x, y)}
			break
		}
	}
	for i := 0; i < min(len(av), len(bv)); i++ {
		x, e := number(av[i].DTS)
		y, f := number(bv[i].DTS)
		if e != nil || f != nil || math.Abs(x-y) > frameTolerance(a[0]) {
			r.PTSContinuity = Result{false, fmt.Sprintf("video packet %d DTS differs from reference", i)}
			break
		}
	}
	if len(aa) != len(ba) {
		r.AudioAUExact = Result{false, fmt.Sprintf("candidate %d AUs, reference %d", len(aa), len(ba))}
	} else {
		for i := range aa {
			if aa[i] != ba[i] {
				r.AudioAUExact = Result{false, fmt.Sprintf("raw AAC AU %d differs", i)}
				break
			}
		}
	}
}
func check(ctx context.Context, segs []Segment, o Options) (Report, error) {
	good := Result{true, "verified"}
	r := Report{IDRStart: good, ClosedGOP: good, PTSContinuity: good, FrameCountVsReference: good, CleanDecode: good, AudioAUExact: good, AudioContinuity: good, GridDurations: good, PCMContinuous: Result{true, "not requested (copied audio)"}, Segments: len(segs)}
	if len(segs) == 0 {
		return r, fmt.Errorf("splicecheck: no segments")
	}
	if len(o.Timeline) > 0 {
		for i, t := range o.Timeline {
			if t < 0 || (i > 0 && t <= o.Timeline[i-1]) {
				return r, fmt.Errorf("splicecheck: timeline must increase strictly")
			}
		}
		for _, s := range segs {
			if s.Index < 0 || s.Index >= len(o.Timeline)-1 {
				return r, fmt.Errorf("splicecheck: segment %d outside expected timeline", s.Index)
			}
		}
	} else if o.SegmentDuration <= 0 {
		return r, fmt.Errorf("splicecheck: positive segment duration required")
	}
	if o.FFmpeg == "" {
		o.FFmpeg = "ffmpeg"
	}
	if o.FFprobe == "" {
		o.FFprobe = "ffprobe"
	}
	dir, e := os.MkdirTemp("", "splicecheck-")
	if e != nil {
		return r, e
	}
	defer os.RemoveAll(dir)
	obs, paths, e := validate(ctx, segs, o, dir, &r)
	if e != nil {
		return r, e
	}
	timing(obs, segs, &r, o)
	// Byte concatenation retains packet clocks. The concat demuxer would rebase
	// each segment and conceal exactly the discontinuity under test.
	joined, e := os.CreateTemp(dir, "joined-*")
	if e != nil {
		return r, e
	}
	for i, p := range paths {
		var in *os.File
		if i == 0 || segs[i].InitPath == "" {
			in, e = os.Open(p)
		} else {
			in, e = os.Open(segs[i].Path)
		}
		if e != nil {
			joined.Close()
			return r, e
		}
		_, e = io.Copy(joined, in)
		in.Close()
		if e != nil {
			joined.Close()
			return r, e
		}
	}
	joined.Close()
	if _, e = invoke(ctx, o.FFmpeg, "-v", "error", "-xerror", "-i", joined.Name(), "-map", "0:v:0", "-map", "0:a:0", "-f", "null", "-"); e != nil {
		r.CleanDecode = Result{false, e.Error()}
	}
	if len(o.Reference) > 0 {
		dummy := Report{}
		ref, _, e := validate(ctx, o.Reference, o, dir, &dummy)
		if e != nil {
			return r, fmt.Errorf("reference: %w", e)
		}
		compare(obs, ref, &r)
		if o.ReencodedAudio {
			r.AudioAUExact = Result{false, "not applicable: independently encoded AAC"}
			if e := checkPCM(ctx, segs, o.Reference, obs, ref, o, dir, &r); e != nil {
				return r, e
			}
		}
	} else {
		r.FrameCountVsReference = Result{false, "reference required to prove no missing frames"}
		r.AudioAUExact = Result{false, "reference required to prove AU identity"}
		r.PCMContinuous = Result{false, "reference required to prove sample continuity"}
	}
	var failed []string
	if o.ReencodedAudio && !r.PCMContinuous.Pass {
		failed = append(failed, "PCMContinuous")
	}
	for _, c := range []struct {
		name string
		r    Result
	}{{"IDRStart", r.IDRStart}, {"ClosedGOP", r.ClosedGOP}, {"PTSContinuity", r.PTSContinuity}, {"FrameCountVsReference", r.FrameCountVsReference}, {"CleanDecode", r.CleanDecode}, {"AudioAUExact", r.AudioAUExact}, {"AudioContinuity", r.AudioContinuity}, {"GridDurations", r.GridDurations}} {
		if c.name == "AudioAUExact" && o.ReencodedAudio {
			continue
		}
		if !c.r.Pass {
			failed = append(failed, c.name)
		}
	}
	if len(failed) > 0 {
		return r, fmt.Errorf("splicecheck: failed %s", strings.Join(failed, ", "))
	}
	return r, nil
}
