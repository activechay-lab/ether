// Package signal produces live, deterministic signal screens without clip loops
// or per-tick allocations. The clock is 30 ticks per second, audio is 48 kHz.
package signal

import "math"

func hash(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	return x ^ (x >> 31)
}
func noise(seed, index int64) float64 {
	return float64(int32(hash(uint64(seed)^uint64(index)*0x9e3779b97f4a7c15)>>32)) / 2147483648
}

// Overlay is a pre-rendered luma/alpha ID, positioned in the full working frame.
// Construct it off the playout path; applying it never allocates.
type Overlay struct {
	X, Y, Width, Height int
	Luma, Alpha         []byte
}

func ApplyOverlay(dst []byte, w, h int, o Overlay) {
	if len(dst) < w*h*3/2 || len(o.Luma) < o.Width*o.Height || len(o.Alpha) < o.Width*o.Height {
		return
	}
	for y := 0; y < o.Height; y++ {
		yy := o.Y + y
		if yy < 0 || yy >= h {
			continue
		}
		for x := 0; x < o.Width; x++ {
			xx := o.X + x
			if xx < 0 || xx >= w {
				continue
			}
			i := y*o.Width + x
			a := int(o.Alpha[i])
			j := yy*w + xx
			dst[j] = byte((int(dst[j])*(255-a) + int(o.Luma[i])*a + 127) / 255)
		}
	}
}

// A symmetric 11-tap low-pass FIR limits the white noise to about 8 kHz. The
// absolute sample index supplies its history, so adjacent ticks have no seam.
func hiss(seed, n int64) float64 {
	return -.0068*(noise(seed, n-5)+noise(seed, n+5)) - .0131*(noise(seed, n-4)+noise(seed, n+4)) + .0508*(noise(seed, n-2)+noise(seed, n+2)) + .2632*(noise(seed, n-1)+noise(seed, n+1)) + .4118*noise(seed, n)
}

// StaticAudio writes interleaved stereo s16 PCM: approximately -30 dBFS RMS
// band-limited hiss and -42 dBFS RMS 60 Hz hum, with slow non-periodic wander.
// tick is the absolute 30 Hz channel tick, not a looping clip-relative index.
func StaticAudio(dst []int16, tick int64, samplesThisTick int, seed int64) {
	if samplesThisTick < 0 || len(dst) < samplesThisTick*2 {
		panic("signal: invalid PCM buffer")
	}
	for i := 0; i < samplesThisTick; i++ {
		n := tick*1600 + int64(i)
		t := float64(n) / 48000
		phase := 2*math.Pi*60*t + .25*math.Sin(t*.037*2*math.Pi+float64(seed%1000)) + .11*math.Sin(t*.017*2*math.Pi*math.Sqrt2)
		v := hiss(seed, n)*.0988 + math.Sin(phase)*.011233
		if n < 480 {
			v *= float64(n) / 480
		}
		q := int16(math.Round(max(-1, min(1, v)) * 32767))
		dst[2*i] = q
		dst[2*i+1] = q
	}
}
