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

// StaticVideo fills tightly packed I420. Noise is evaluated at half resolution,
// bilinearly upscaled with horizontal bandwidth smear, changing at 15 Hz. The
// rolling bar and tears follow the full 30 Hz clock and never use a clip phase.
// Composite an optional pre-rendered ID with ApplyOverlay after this call.
func StaticVideo(dst []byte, w, h int, seed, tick int64) {
	if w < 2 || h < 2 || w%2 != 0 || h%2 != 0 || len(dst) < w*h*3/2 {
		panic("signal: invalid I420 buffer")
	}

	yplane := dst[:w*h]
	// Reuse the U plane as half-resolution scratch; chroma is neutralized
	// afterwards. No heap buffer or mutable state survives a tick.
	sw, sh := w/2, h/2
	grid := dst[w*h : w*h+w*h/4]
	update := tick / 2
	for y := 0; y < sh; y++ {
		state := uint32(hash(uint64(seed) ^ uint64(update)*0xd1342543de82ef95 ^ uint64(y)*0x9e3779b97f4a7c15))
		if state == 0 {
			state = 1
		}
		for x := 0; x < sw; x++ {
			state ^= state << 13
			state ^= state >> 17
			state ^= state << 5
			grid[y*sw+x] = byte(52 + (state >> 25))
		}
	}
	t := float64(tick) / 30
	bar := int((.5 + .24*math.Sin(t*.173) + .13*math.Sin(t*.071*math.Sqrt2) + .08*walk(seed, tick)) * float64(h))
	barHeight := max(12, h/11)
	for y := 0; y < h; y++ {
		top := grid[(y/2)*sw : (y/2+1)*sw]
		bottom := grid[min(sh-1, y/2+1)*sw : (min(sh-1, y/2+1)+1)*sw]
		row := yplane[y*w : (y+1)*w]
		distance := abs(y - bar)
		offset := int(hash(uint64(seed)^uint64(update)*131071^uint64(y))%5) - 2
		if distance < barHeight {
			offset -= (barHeight - distance) * 28 / barHeight
		}
		if y%3 == 0 {
			offset -= 2
		}
		prev := int(top[0])
		if y%2 != 0 {
			prev = (prev + int(bottom[0]) + 1) / 2
		}
		for x := 0; x < sw; x++ {
			a, b := int(top[x]), int(top[min(sw-1, x+1)])
			if y%2 != 0 {
				a = (a + int(bottom[x]) + 1) / 2
				b = (b + int(bottom[min(sw-1, x+1)]) + 1) / 2
			}
			mid := (a + b + 1) / 2
			// Bilinear 2x upsampling, followed by a two-pixel horizontal smear.
			row[x*2] = byte(max(16, min(235, (prev+a+1)/2+offset)))
			row[x*2+1] = byte(max(16, min(235, (a+mid+1)/2+offset)))
			prev = mid
		}
	}
	for i := w * h; i < w*h*3/2; i++ {
		dst[i] = 128
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
