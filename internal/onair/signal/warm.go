package signal

import "math"

// WarmGenerator owns the approved bandwidth-limited phosphor field and bloom.
// Frame is allocation-free; Engine samples it smoothly at the output resolution.
type WarmGenerator struct {
	w, h, gw, gh          int
	seed                  uint64
	state                 uint32
	ema, blur, temp, gain []float32
}

func NewWarmGenerator(w, h int, seed int64) *WarmGenerator {
	g := &WarmGenerator{w: w, h: h, gw: w / 2, gh: h / 2, seed: uint64(seed), state: uint32(seed) | 1}
	g.ema = make([]float32, g.gw*g.gh)
	g.blur = make([]float32, len(g.ema))
	g.temp = make([]float32, len(g.ema))
	g.gain = make([]float32, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			nx, ny := (float64(x)-float64(w)/2)/(float64(w)/2), (float64(y)-float64(h)/2)/(float64(h)/2)
			r := nx*nx*.56 + ny*ny*.48
			g.gain[y*w+x] = float32(.40 + .60*math.Pow(max(0, 1-r), .7))
		}
	}
	return g
}

// Frame produces warm, bandwidth-limited snow. The white-noise excitation is
// temporally integrated, then spatially bloomed before upscaling. No pixel can
// reach hard black or white; curved edge falloff and slow motion suggest a CRT.
func (g *WarmGenerator) Frame(dst []byte, tick int64) {
	if len(dst) < g.w*g.h*3/2 {
		panic("signal: invalid warm frame")
	}
	alpha, amplitude := float32(.035), float32(68)
	barDepth := 1.8
	if tick%2 == 0 {
		for i := range g.ema {
			g.state ^= g.state << 13
			g.state ^= g.state >> 17
			g.state ^= g.state << 5
			n := float32(int32(g.state)) / 2147483648
			g.ema[i] += alpha * (n - g.ema[i])
		}
		// Separable [1,2,1] Gaussian approximation on the source's fine detail.
		for y := 0; y < g.gh; y++ {
			for x := 0; x < g.gw; x++ {
				i := y*g.gw + x
				g.temp[i] = (g.ema[y*g.gw+max(0, x-1)] + 2*g.ema[i] + g.ema[y*g.gw+min(g.gw-1, x+1)]) * .25
			}
		}
		for y := 0; y < g.gh; y++ {
			for x := 0; x < g.gw; x++ {
				i := y*g.gw + x
				g.blur[i] = (g.temp[max(0, y-1)*g.gw+x] + 2*g.temp[i] + g.temp[min(g.gh-1, y+1)*g.gw+x]) * .25
			}
		}
	}
	t := float64(tick) / 30
	amplitude *= 1 + .12*float32(math.Sin(t*.23))
	bar := float64(g.h) * (.5 + .33*math.Sin(t*.043) + .06*math.Sin(t*.017*math.Sqrt2))
	for y := 0; y < g.h; y++ {
		fy := float64(y) / 2
		sy := y / 2
		dy := float32(fy - float64(sy))
		barY := (float64(y) - bar) / (float64(g.h) * .13)
		roll := -barDepth * math.Exp(-barY*barY)
		line := .7*math.Sin(float64(y)*math.Pi/2) + .35*math.Sin(t*.31+float64(y)*.11)
		for x := 0; x < g.w; x++ {
			sx := x / 2
			dx := float32(x%2) * .5
			i := sy*g.gw + sx
			right := min(g.gw-1, sx+1)
			bottom := min(g.gh-1, sy+1)
			a := g.blur[i]*(1-dx) + g.blur[sy*g.gw+right]*dx
			b := g.blur[bottom*g.gw+sx]*(1-dx) + g.blur[bottom*g.gw+right]*dx
			n := a*(1-dy) + b*dy
			gain := float64(g.gain[y*g.w+x])
			v := float64(139+n*amplitude)*gain + roll + line
			dst[y*g.w+x] = byte(max(24, min(205, int(v+.5))))
		}
	}
	// A rare, low-contrast tracking disturbance lasts only a few ticks.
	event := hash(g.seed ^ uint64(tick/6)*0x9e3779b97f4a7c15)
	if event%397 == 0 {
		line := int(event>>16) % g.h
		row := dst[line*g.w : (line+1)*g.w]
		for x, v := range row {
			row[x] = byte(min(205, int(v)+2+int((event>>32)%3)))
		}
	}
	// Warm cream/amber phosphor, with a very soft chroma falloff at the edges.
	for y := 0; y < g.h/2; y++ {
		for x := 0; x < g.w/2; x++ {
			gain := float64(g.gain[(y*2)*g.w+x*2])
			i := y*(g.w/2) + x
			dst[g.w*g.h+i] = byte(128 - 8*gain)
			dst[g.w*g.h+g.w*g.h/4+i] = byte(128 + 9*gain)
		}
	}
}

// WarmAudio supplies comfortable low-passed hiss and hum, with a slow tuning
// swell. It follows the continuous channel clock without a loop seam.
func WarmAudio(dst []int16, tick int64, seed int64) {
	StaticAudio(dst, tick, 1600, seed)
	gain := .53 + .06*math.Sin(float64(tick)/30*.071) + .03*math.Sin(float64(tick)/30*.029*math.Sqrt2)
	for i, v := range dst {
		dst[i] = int16(float64(v) * gain)
	}
}
