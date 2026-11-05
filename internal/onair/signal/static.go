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

// walk is a seeded random walk in 32-step bridges between unique epoch anchors.
// The bridge correction makes epoch boundaries continuous without replaying any
// historical state. Evaluation has constant cost, including after days on air.
func walk(seed, tick int64) float64 {
	epoch := tick / 960
	position := float64(tick%960) / 30
	step := int(position)
	fraction := position - float64(step)
	start, end := noise(seed, epoch), noise(seed, epoch+1)
	sum, total := 0.0, 0.0
	for i := 0; i < 32; i++ {
		v := noise(seed^int64(hash(uint64(epoch))), int64(i))
		total += v
		if i < step {
			sum += v
		} else if i == step {
			sum += fraction * v
		}
	}
	return start + (end-start)*position/32 + (sum-total*position/32)*.08
}

// StaticVideo fills tightly packed I420. Noise is evaluated on a one-third-size
// working grid in 2x2 grains (6x6 destination pixels), changing at 15 Hz. The
// rolling bar and tears follow the full 30 Hz clock and never use a clip phase.
// Composite an optional pre-rendered ID with ApplyOverlay after this call.
func StaticVideo(dst []byte, w, h int, seed, tick int64) {
	if w < 2 || h < 2 || w%2 != 0 || h%2 != 0 || len(dst) < w*h*3/2 {
		panic("signal: invalid I420 buffer")
	}
	yplane := dst[:w*h]
	for i := w * h; i < w*h*3/2; i++ {
		dst[i] = 128
	}
	t := float64(tick) / 30
	bar := int((.5 + .24*math.Sin(t*.173) + .13*math.Sin(t*.071*math.Sqrt2) + .08*walk(seed, tick)) * float64(h))
	barHeight := max(12, h/11)
	update := tick / 2
	for gy, y := 0, 0; y < h; gy, y = gy+1, y+6 {
		row := yplane[y*w : (y+1)*w]
		for gx, x := 0, 0; x < w; gx, x = gx+1, x+6 {
			v := byte(52 + int(hash(uint64(seed)^uint64(update)*0xd1342543de82ef95^uint64(gy)*0x9e3779b97f4a7c15^uint64(gx))>>57)*1)
			for xx, end := x, min(w, x+6); xx < end; xx++ {
				row[xx] = v
			}
		}
		for dy := 1; dy < min(6, h-y); dy++ {
			copy(yplane[(y+dy)*w:(y+dy+1)*w], row)
		}
		for dy := 0; dy < min(6, h-y); dy++ {
			target := yplane[(y+dy)*w : (y+dy+1)*w]
			distance := abs(y + dy - bar)
			offset := 0
			if distance < barHeight {
				offset = -(barHeight - distance) * 28 / barHeight
			}
			if (y+dy)%3 == 0 {
				offset -= 3
			}
			for x, v := range target {
				target[x] = byte(max(16, min(235, int(v)+offset)))
			}
		}
	}
	// Rare horizontal tracking tears; each event uses its own seed and position.
	event := tick / 3
	bits := hash(uint64(seed) ^ uint64(event)*0xa0761d6478bd642f)
	if bits%29 == 0 {
		line := int((bits >> 16) % uint64(h))
		for y := line; y < min(h, line+max(1, h/270)); y++ {
			row := yplane[y*w : (y+1)*w]
			for x := range row {
				row[x] = byte(145 + hash(bits^uint64(x/12))%60)
			}
		}
	}
}
func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
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
