package splicecheck

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"os"
)

func joinedAudio(ctx context.Context, segs []Segment, o Options, dir string) ([]float32, int, error) {
	f, err := os.CreateTemp(dir, "pcm-joined-*")
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	for i, s := range segs {
		if i == 0 && s.InitPath != "" {
			b, e := os.ReadFile(s.InitPath)
			if e != nil {
				return nil, 0, e
			}
			if _, e = f.Write(b); e != nil {
				return nil, 0, e
			}
		}
		b, e := os.ReadFile(s.Path)
		if e != nil {
			return nil, 0, e
		}
		if _, e = f.Write(b); e != nil {
			return nil, 0, e
		}
	}
	if err = f.Close(); err != nil {
		return nil, 0, err
	}
	b, err := invoke(ctx, o.FFprobe, "-v", "error", "-select_streams", "a:0", "-show_entries", "stream=sample_rate", "-of", "json", f.Name())
	if err != nil {
		return nil, 0, err
	}
	var probe struct {
		Streams []struct {
			Rate string `json:"sample_rate"`
		}
	}
	if err = json.Unmarshal(b, &probe); err != nil || len(probe.Streams) == 0 {
		return nil, 0, fmt.Errorf("PCM sample rate unavailable")
	}
	rate, err := number(probe.Streams[0].Rate)
	if err != nil {
		return nil, 0, err
	}
	// Use the same explicit stereo projection for both streams. No async
	// resampling, timestamp rebasing, lag fitting or silence insertion is allowed.
	b, err = invoke(ctx, o.FFmpeg, "-v", "error", "-xerror", "-i", f.Name(), "-map", "0:a:0", "-ac", "2", "-f", "f32le", "pipe:1")
	if err != nil {
		return nil, 0, err
	}
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[i*4:]))
		if math.IsNaN(float64(out[i])) || math.IsInf(float64(out[i]), 0) {
			return nil, 0, fmt.Errorf("non-finite decoded PCM sample")
		}
	}
	return out, int(rate), nil
}
func audioClock(x observation) ([]float64, []float64) {
	var pts, end []float64
	for _, p := range x.packets {
		if p.CodecType == "audio" {
			t, _ := number(p.PTS)
			d, _ := number(p.Duration)
			pts = append(pts, t)
			end = append(end, t+d)
		}
	}
	return pts, end
}
func checkPCM(ctx context.Context, segs, refs []Segment, obs, reference []observation, o Options, dir string, r *Report) error {
	r.PCMContinuous = Result{true, "zero missing/overlapping samples; SNR >=30dB; no boundary click"}
	candidate, rate, err := joinedAudio(ctx, segs, o, dir)
	if err != nil {
		return err
	}
	ref, refRate, err := joinedAudio(ctx, refs, o, dir)
	if err != nil {
		return err
	}
	if rate != refRate || rate <= 0 || len(obs) != len(reference) {
		return fmt.Errorf("PCM reference clock mismatch")
	}
	origin, _ := audioClock(reference[0])
	if len(origin) == 0 {
		return fmt.Errorf("PCM reference has no audio")
	}
	fail := func(s string) { r.PCMContinuous = Result{false, s} }
	// Every AU timestamp must equal the uninterrupted run's timestamp in the
	// decoded sample domain, including the AU immediately following the splice.
	for i := range obs {
		a, _ := audioClock(obs[i])
		b, _ := audioClock(reference[i])
		if len(a) != len(b) {
			fail(fmt.Sprintf("segment %d AAC frame count %d vs %d", segs[i].Index, len(a), len(b)))
		}
		for j := 0; j < min(len(a), len(b)); j++ {
			if math.Abs(a[j]-b[j])*float64(rate) > 0.5 {
				fail(fmt.Sprintf("segment %d AU %d differs by %.3f samples", segs[i].Index, j, (a[j]-b[j])*float64(rate)))
			}
		}
		if i == 0 || segs[i].Generation == segs[i-1].Generation {
			continue
		}
		_, prev := audioClock(obs[i-1])
		if len(a) == 0 || len(prev) == 0 {
			return fmt.Errorf("missing boundary audio")
		}
		delta := int(math.Round((a[0] - prev[len(prev)-1]) * float64(rate)))
		m := PCMMeasurement{Index: segs[i].Index, MissingSamples: max(0, delta), OverlappingSamples: max(0, -delta)}
		if delta != 0 {
			fail(fmt.Sprintf("boundary %d missing %d overlapping %d samples", m.Index, m.MissingSamples, m.OverlappingSamples))
		}
		center := int(math.Round((a[0] - origin[0]) * float64(rate)))
		lo, hi := center-rate/10, center+rate/10
		if lo < 1 || hi*2 > len(candidate) || hi*2 > len(ref) {
			return fmt.Errorf("PCM boundary window unavailable at %d", m.Index)
		}
		var signal, noise float64
		for n := lo; n < hi; n++ {
			for ch := 0; ch < 2; ch++ {
				j := n*2 + ch
				x, y := float64(candidate[j]), float64(ref[j])
				signal += y * y
				noise += (x - y) * (x - y)
				m.ReferenceMaxStep = math.Max(m.ReferenceMaxStep, math.Abs(y-float64(ref[j-2])))
			}
		}
		m.SNRDB = 120
		if noise > 0 {
			m.SNRDB = 10 * math.Log10(math.Max(signal, 1e-30)/noise)
		}
		for ch := 0; ch < 2; ch++ {
			j := center*2 + ch
			m.BoundaryStep = math.Max(m.BoundaryStep, math.Abs(float64(candidate[j]-candidate[j-2])))
		}
		if m.SNRDB < 30 || m.BoundaryStep > m.ReferenceMaxStep {
			fail(fmt.Sprintf("boundary %d SNR %.3fdB step %.9f reference max %.9f", m.Index, m.SNRDB, m.BoundaryStep, m.ReferenceMaxStep))
		}
		r.PCMBoundaries = append(r.PCMBoundaries, m)
	}
	return nil
}
