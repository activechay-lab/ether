package signal

import (
	"image"
	"image/color"
	"image/draw"
	"math"
)

// Engine owns one rendition's live Signal state. Construct off the tick path.
// Render is allocation-free and its output belongs to the caller. Static uses
// a temporally integrated low-resolution phosphor field; plate styles keep a
// precomposited I420 plate and animate only cheap luma effects.
type Engine struct {
	width, height int
	style         string
	warm          *WarmGenerator
	source, plate []byte
	vignette      []uint16
	rowA, rowB    []uint16
	xIndex        []int
	xWeight       []uint16
	lastRow       int
	seed          int64
}

// NewStatic produces the approved warm phosphor static at the output resolution.
// The band-limited field is deliberate softness, not a reduced-resolution encode.
func NewStatic(width, height int, seed int64) *Engine {
	e := newEngine(width, height, "static", seed)
	e.warm = NewWarmGenerator(320, 180, seed)
	e.source = make([]byte, 320*180*3/2)
	e.rowA = make([]uint16, width)
	e.rowB = make([]uint16, width)
	e.xIndex = make([]int, width)
	e.xWeight = make([]uint16, width)
	for x := 0; x < width; x++ {
		position := float64(x) * 319 / float64(width-1)
		i := int(position)
		e.xIndex[x] = min(318, i)
		e.xWeight[x] = uint16(math.Round((position - float64(e.xIndex[x])) * 256))
	}
	return e
}
func newEngine(w, h int, style string, seed int64) *Engine {
	if w < 2 || h < 2 || w%2 != 0 || h%2 != 0 {
		panic("signal: invalid size")
	}
	return &Engine{width: w, height: h, style: style, seed: seed, lastRow: -1}
}

// NewPlate accepts straight/premultiplied image.Image plates (PNG decoding
// handles either) and a transparent text overlay. Text is composited only here,
// so replace the Engine when copy changes rather than rerasterizing per tick.
// style is bars, testcard, brb, or card. Every style is silent.
func NewPlate(width, height int, style string, plate, overlay image.Image, seed int64) *Engine {
	e := newEngine(width, height, style, seed)
	if plate == nil {
		panic("signal: plate required")
	}
	combined := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(combined, combined.Rect, plate, plate.Bounds().Min, draw.Src)
	if overlay != nil {
		draw.Draw(combined, combined.Rect, overlay, overlay.Bounds().Min, draw.Over)
	}
	e.plate = make([]byte, width*height*3/2)
	e.vignette = make([]uint16, width*height)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			nx, ny := (float64(x)/float64(width)-.5)*2, (float64(y)/float64(height)-.5)*2
			e.vignette[y*width+x] = uint16(256 - 20*(nx*nx+ny*ny)/2)
		}
	}
	for y := 0; y < height; y += 2 {
		for x := 0; x < width; x += 2 {
			r, g, b := 0, 0, 0
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					c := color.NRGBAModel.Convert(combined.At(x+dx, y+dy)).(color.NRGBA)
					rr, gg, bb := int(c.R), int(c.G), int(c.B)
					r += rr
					g += gg
					b += bb
					e.plate[(y+dy)*width+x+dx] = byte(16 + (47*rr+157*gg+16*bb+128)/256)
				}
			}
			r /= 4
			g /= 4
			b /= 4
			i := y/2*(width/2) + x/2
			e.plate[width*height+i] = byte(128 + (-26*r-87*g+112*b+128)/256)
			e.plate[width*height+width*height/4+i] = byte(128 + (112*r-102*g-10*b+128)/256)
		}
	}
	return e
}

// Render supplies tightly packed I420 and stereo 48 kHz PCM for one 30 Hz tick.
func (e *Engine) Render(video []byte, pcm []int16, tick int64) {
	w, h := e.width, e.height
	if len(video) < w*h*3/2 || len(pcm) < 3200 {
		panic("signal: undersized tick buffers")
	}
	if e.warm != nil {
		e.warm.Frame(e.source, tick)
		e.upscale(video)
		WarmAudio(pcm, tick, e.seed)
		return
	}
	copy(video, e.plate)
	t := float64(tick) / 30
	breath := .006*math.Sin(t*.21) + .003*math.Sin(t*.083*math.Sqrt2)
	// A single 256-value lookup per row applies scanline drift and brightness
	// wander plus tube vignette. Decorative bloom is baked into W6 plates;
	// the typography overlay remains sharp and is never rasterized per tick.
	for y := 0; y < h; y++ {
		gain := 1 + breath + .003*math.Sin(float64(y)*math.Pi/3-t*.14)
		var lut [256]byte
		for v := range lut {
			lut[v] = byte(max(16, min(235, 16+int(float64(v-16)*gain))))
		}
		row := video[y*w : (y+1)*w]
		for x, v := range row {
			row[x] = byte(16 + (int(lut[v])-16)*int(e.vignette[y*w+x])/256)
		}
	}
	// Every signal screen is silent (owner decision: no bars or test-card tone).
	clear(pcm)
}
func (e *Engine) upscale(dst []byte) {
	w, h := e.width, e.height
	e.lastRow = -1
	for y := 0; y < h; y++ {
		position := float64(y) * 179 / float64(h-1)
		sy := min(178, int(position))
		weight := int(math.Round((position - float64(sy)) * 256))
		if sy != e.lastRow {
			for x := 0; x < w; x++ {
				sx, wx := e.xIndex[x], int(e.xWeight[x])
				a := int(e.source[sy*320+sx])*(256-wx) + int(e.source[sy*320+sx+1])*wx
				b := int(e.source[(sy+1)*320+sx])*(256-wx) + int(e.source[(sy+1)*320+sx+1])*wx
				e.rowA[x] = uint16(a)
				e.rowB[x] = uint16(b)
			}
			e.lastRow = sy
		}
		row := dst[y*w : (y+1)*w]
		for x := 0; x < w; x++ {
			row[x] = byte((int(e.rowA[x])*(256-weight) + int(e.rowB[x])*weight + 32768) >> 16)
		}
	}
	// Warm phosphor chroma varies only at the low source resolution.
	for y := 0; y < h/2; y++ {
		sy := min(89, y*180/h)
		for x := 0; x < w/2; x++ {
			sx := min(159, x*320/w)
			i := y*(w/2) + x
			dst[w*h+i] = e.source[320*180+sy*160+sx]
			dst[w*h+w*h/4+i] = e.source[320*180+320*180/4+sy*160+sx]
		}
	}
}
