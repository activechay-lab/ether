package signal

import "math"

// WarmGenerator is an offline master source. It owns its low-resolution EMA and
// bloom buffers; Frame is allocation-free. Run at 320x180 and upscale smoothly
// when encoding a master. A long master removes this work from live playout.
type WarmGenerator struct {
	w, h, gw, gh          int
	seed                  uint64
	variant               string
	state                 uint32
	ema, blur, temp, gain []float32
	ghostPlate            []float32
}

func NewWarmGenerator(w, h int, seed int64, variant string) *WarmGenerator {
	g := &WarmGenerator{w: w, h: h, gw: w / 2, gh: h / 2, seed: uint64(seed), state: uint32(seed) | 1, variant: variant}
	g.ema = make([]float32, g.gw*g.gh)
	g.blur = make([]float32, len(g.ema))
	g.temp = make([]float32, len(g.ema))
	g.gain = make([]float32, w*h)
	if variant == "ghost" {
		g.ghostPlate = make([]float32, w*h)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			nx, ny := (float64(x)-float64(w)/2)/(float64(w)/2), (float64(y)-float64(h)/2)/(float64(h)/2)
			r := nx*nx*.56 + ny*ny*.48
			g.gain[y*w+x] = float32(.40 + .60*math.Pow(max(0, 1-r), .7))
			if variant == "ghost" {
				cx, cy := float64(x)-float64(w)/2, float64(y)-float64(h)/2
				radius := math.Sqrt(cx*cx + cy*cy)
				ring := math.Exp(-math.Pow((radius-float64(h)*.27)/1.3, 2))
				cross := math.Exp(-cx*cx/1.5)*math.Exp(-cy*cy/900) + math.Exp(-cy*cy/1.5)*math.Exp(-cx*cx/2200)
				g.ghostPlate[y*w+x] = float32(ring + cross)
			}
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
	alpha, amplitude := float32(.055), float32(46)
	barDepth := 4.0
	switch g.variant {
	case "shimmer":
		alpha = .035
		amplitude = 68
		barDepth = 1.8
	case "classic":
		alpha = .10
		amplitude = 112
		barDepth = 5
	case "ghost":
		alpha = .045
		amplitude = 88
		barDepth = 2.5
	}
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
	drift := int(2 * math.Sin(t*.09))
	ghost := 0.0
	if g.variant == "ghost" {
		ghost = 5 + 5*math.Sin(t*.11+math.Sin(t*.037))
	}
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
			if ghost > 0 {
				xx := max(0, min(g.w-1, x+drift))
				v += ghost * float64(g.ghostPlate[y*g.w+xx]) * gain
			}
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
// swell. The master builder handles the two-second loop crossfade separately.
func WarmAudio(dst []int16, tick int64, seed int64) {
	StaticAudio(dst, tick, 1600, seed)
	gain := .53 + .06*math.Sin(float64(tick)/30*.071) + .03*math.Sin(float64(tick)/30*.029*math.Sqrt2)
	for i, v := range dst {
		dst[i] = int16(float64(v) * gain)
	}
}
