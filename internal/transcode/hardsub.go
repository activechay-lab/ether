package transcode

import (
	"bytes"
	"context"
	"fmt"
	"time"
)

const subWidth, subHeight = 640, 360

// DetectHardSubs samples four scenes, at most one decoder per manager, with
// single-thread decoding and an eight-second wall budget. Inconclusive reads
// remain unknown. No OCR, subtitle service, or media upload is involved.
func (m *Manager) DetectHardSubs(ctx context.Context, input string, duration time.Duration) (bool, bool) {
	if duration <= 0 || !m.subtitleMu.TryLock() {
		return false, false
	}
	defer m.subtitleMu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	var frames [][]byte
	for _, f := range []float64{.10, .30, .55, .80} {
		at := min(duration.Seconds()*f, 300*f)
		var out bytes.Buffer
		args := []string{"-v", "error", "-nostdin", "-threads", "1", "-filter_threads", "1", "-ss", fmt.Sprintf("%.3f", at)}
		args = append(args, inputSafetyArgs(input, false)...)
		args = append(args, "-rw_timeout", "1500000", "-i", input, "-map", "0:v:0", "-frames:v", "1", "-an", "-sn", "-vf", "scale=640:360,format=gray", "-f", "rawvideo", "pipe:1")
		if err := m.runner.Run(ctx, Command{Path: m.ffmpeg, Args: args, Stdout: &out}); err != nil || out.Len() != subWidth*subHeight {
			return false, false
		}
		frames = append(frames, out.Bytes())
	}
	return burnedText(frames), true
}

// textMask finds glyph-sized connected high-contrast components in the top
// and bottom quarters. Long edges, borders and large objects are discarded.
func textMask(p []byte) []bool {
	mask := make([]bool, len(p))
	seen := make([]bool, len(p))
	for y := 1; y < subHeight-1; y++ {
		if y > 90 && y < 250 {
			continue
		}
		for x := 40; x < 600; x++ {
			i := y*subWidth + x
			if p[i] >= 145 {
				mask[i] = true
			}
		}
	}
	rows := make([]int, subHeight)
	for i, on := range mask {
		if !on || seen[i] {
			continue
		}
		seen[i] = true
		q := []int{i}
		minX, maxX, minY, maxY := i%subWidth, i%subWidth, i/subWidth, i/subWidth
		for n := 0; n < len(q); n++ {
			j := q[n]
			x, y := j%subWidth, j/subWidth
			minX = min(minX, x)
			maxX = max(maxX, x)
			minY = min(minY, y)
			maxY = max(maxY, y)
			for _, d := range []int{-1, 1, -subWidth, subWidth} {
				k := j + d
				if k >= 0 && k < len(mask) && mask[k] && !seen[k] {
					seen[k] = true
					q = append(q, k)
				}
			}
		}
		w, h := maxX-minX+1, maxY-minY+1
		dark, edge := 0, 0
		for _, j := range q {
			for _, d := range []int{-1, 1, -subWidth, subWidth} {
				k := j + d
				if k >= 0 && k < len(mask) && !mask[k] {
					edge++
					if p[k] < 100 {
						dark++
					}
				}
			}
		}
		if w >= 2 && w <= 32 && h >= 6 && h <= 32 && len(q) >= 8 && edge > 0 && dark*100/edge >= 55 {
			rows[(minY+maxY)/2]++
		} else {
			for _, j := range q {
				mask[j] = false
			}
		}
	}
	enough := false
	for y := 0; y < subHeight-11; y++ {
		n := 0
		for _, v := range rows[y : y+12] {
			n += v
		}
		if n >= 8 {
			enough = true
			break
		}
	}
	if !enough {
		clear(mask)
	}
	return mask
}
func burnedText(frames [][]byte) bool {
	if len(frames) < 4 {
		return false
	}
	masks := make([][]bool, len(frames))
	hits := 0
	for i, f := range frames {
		if len(f) != subWidth*subHeight {
			return false
		}
		masks[i] = textMask(f)
		for _, v := range masks[i] {
			if v {
				hits++
				break
			}
		}
	}
	if hits < 3 {
		return false
	}
	// Static watermark glyphs survive scene changes at exactly the same pixels.
	// Subtitles must change shape across at least two different scenes.
	for i := 1; i < len(frames); i++ {
		diff, union, scene := 0, 0, 0
		for j, a := range masks[0] {
			b := masks[i][j]
			if a || b {
				union++
			}
			if a != b {
				diff++
			}
		}
		for y := 110; y < 240; y++ {
			for x := 60; x < 580; x++ {
				j := y*subWidth + x
				d := int(frames[0][j]) - int(frames[i][j])
				if d < 0 {
					d = -d
				}
				scene += d
			}
		}
		if union > 0 && diff*100/union > 35 && scene/(130*520) > 5 {
			return true
		}
	}
	return false
}
