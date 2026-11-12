package raster

import (
	"runtime"
	"sync"

	"github.com/braedonsaunders/ether/internal/onair/scene"
	"image"
	"image/color"
	"math"
)

// parallelLines runs fn over disjoint [lo,hi) ranges of n lines. Pixel
// work in this file is per-line independent and exact: splitting lines
// across cores changes no output byte. Small frames stay on one goroutine.
// lineSlots bounds extra raster worker goroutines across every concurrent
// render (graphics masters, live moments, previews). Without a shared bound
// each call fanned out to eight workers and a watched channel's renders took
// most of the machine, starving its own frame clock.
var lineSlots = make(chan struct{}, max(1, runtime.NumCPU()/4-1))

func parallelLines(n int, fn func(lo, hi int)) {
	workers := min(runtime.GOMAXPROCS(0), max(2, runtime.NumCPU()/4))
	if workers <= 1 || n < 256 {
		fn(0, n)
		return
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for lo := 0; lo < n; lo += chunk {
		hi := min(n, lo+chunk)
		select {
		case lineSlots <- struct{}{}:
			wg.Add(1)
			go func() {
				defer func() { <-lineSlots; wg.Done() }()
				fn(lo, hi)
			}()
		default:
			// No free slot: do this chunk on the calling goroutine.
			fn(lo, hi)
		}
	}
	wg.Wait()
}

// stroke expands a flattened path into consistently wound polygons.
func stroke(cs []contour, width float64, join, cap string) []contour {
	var out []contour
	r := width / 2
	if r <= 0 {
		return nil
	}
	circle := func(p point) {
		c := contour{closed: true}
		for j := 0; j < 24; j++ {
			a := -float64(j) * math.Pi / 12
			c.p = append(c.p, point{p.x + math.Cos(a)*r, p.y + math.Sin(a)*r})
		}
		out = append(out, c)
	}
	for _, c := range cs {
		n := len(c.p)
		if n < 2 {
			continue
		}
		segs := n - 1
		if c.closed {
			segs = n
		}
		for j := 0; j < segs; j++ {
			a, b := c.p[j], c.p[(j+1)%n]
			dx, dy := b.x-a.x, b.y-a.y
			length := math.Hypot(dx, dy)
			if length == 0 {
				continue
			}
			nx, ny := -dy/length*r, dx/length*r
			if cap == "square" && !c.closed {
				if j == 0 {
					a.x -= dx / length * r
					a.y -= dy / length * r
				}
				if j == segs-1 {
					b.x += dx / length * r
					b.y += dy / length * r
				}
			}
			out = append(out, contour{[]point{{a.x + nx, a.y + ny}, {b.x + nx, b.y + ny}, {b.x - nx, b.y - ny}, {a.x - nx, a.y - ny}}, true})
			if j > 0 || c.closed {
				p := c.p[(j+n-1)%n]
				ux, uy := a.x-p.x, a.y-p.y
				ul := math.Hypot(ux, uy)
				if ul > 0 {
					px, py := -uy/ul*r, ux/ul*r
					cross := ux*dy - uy*dx
					sign := 1.0
					if cross > 0 {
						sign = -1
					}
					p1, p2 := point{a.x + sign*px, a.y + sign*py}, point{a.x + sign*nx, a.y + sign*ny}
					if join == "round" {
						circle(a)
					} else {
						poly := []point{a, p2, p1}
						if join == "miter" || join == "" {
							den := ux*dy - uy*dx
							if math.Abs(den) > 1e-8 {
								tt := ((p2.x-p1.x)*dy - (p2.y-p1.y)*dx) / den
								mp := point{p1.x + tt*ux, p1.y + tt*uy}
								if math.Hypot(mp.x-a.x, mp.y-a.y) <= width*4 {
									poly = []point{a, p2, mp, p1}
								}
							}
						} // orient clockwise to match segments
						if area(poly) > 0 {
							reverse(poly)
						}
						out = append(out, contour{poly, true})
					}
				}
			}
		}
		if cap == "round" && !c.closed {
			circle(c.p[0])
			circle(c.p[n-1])
		}
	}
	return out
}
func area(p []point) float64 {
	a := 0.0
	for j := range p {
		q := p[(j+1)%len(p)]
		a += p[j].x*q.y - q.x*p[j].y
	}
	return a
}
func reverse(p []point) {
	for i, j := 0, len(p)-1; i < j; i, j = i+1, j-1 {
		p[i], p[j] = p[j], p[i]
	}
}

// box is one sliding-window pass (edge pixels replicate). The sums are
// integer and per line, so lines run in parallel with identical output.
func box(src, dst *image.RGBA, radius int, horizontal bool) {
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	n := radius*2 + 1
	sp, dp := src.Pix, dst.Pix
	ss, ds := src.Stride, dst.Stride
	if horizontal {
		parallelLines(h, func(lo, hi int) {
			for y := lo; y < hi; y++ {
				row := y * ss
				out := y * ds
				var sum [4]int
				for j := -radius; j <= radius; j++ {
					p := row + max(0, min(w-1, j))*4
					for c := 0; c < 4; c++ {
						sum[c] += int(sp[p+c])
					}
				}
				for x := 0; x < w; x++ {
					p := out + x*4
					for c := 0; c < 4; c++ {
						dp[p+c] = uint8(sum[c] / n)
					}
					a := row + max(0, min(w-1, x-radius))*4
					b := row + max(0, min(w-1, x+radius+1))*4
					for c := 0; c < 4; c++ {
						sum[c] += int(sp[b+c]) - int(sp[a+c])
					}
				}
			}
		})
		return
	}
	parallelLines(w, func(lo, hi int) {
		for x := lo; x < hi; x++ {
			col := x * 4
			var sum [4]int
			for j := -radius; j <= radius; j++ {
				p := max(0, min(h-1, j))*ss + col
				for c := 0; c < 4; c++ {
					sum[c] += int(sp[p+c])
				}
			}
			for y := 0; y < h; y++ {
				p := y*ds + col
				for c := 0; c < 4; c++ {
					dp[p+c] = uint8(sum[c] / n)
				}
				a := max(0, min(h-1, y-radius))*ss + col
				b := max(0, min(h-1, y+radius+1))*ss + col
				for c := 0; c < 4; c++ {
					sum[c] += int(sp[b+c]) - int(sp[a+c])
				}
			}
		}
	})
}
func blur(src *image.RGBA, radius float64) *image.RGBA {
	if radius <= 0 {
		return src
	}
	dst := image.NewRGBA(src.Bounds())
	tmp := image.NewRGBA(src.Bounds())
	copy(tmp.Pix, src.Pix)
	r := max(1, int(math.Round(radius*.57735)))
	for j := 0; j < 3; j++ {
		box(tmp, dst, r, true)
		box(dst, tmp, r, false)
	}
	return tmp
}
func (r *render) finish(dst, src *image.RGBA, l scene.Layer, alpha float64, clip *image.Alpha) {
	r.behaviours(src, l)
	if l.Blur > 0 {
		src = blur(src, l.Blur*float64(r.w)/1920)
	}
	if l.Glow > 0 {
		gl := blur(src, l.Glow*float64(r.w)/1920)
		composite(dst, gl, alpha, clip, "screen")
	}
	composite(dst, src, alpha, clip, l.Blend)
}
func composite(dst, src *image.RGBA, alpha float64, clip *image.Alpha, mode string) {
	alpha = max(0, min(1, alpha))
	parallelLines(dst.Rect.Dy(), func(lo, hi int) {
		compositeRows(dst, src, alpha, clip, mode, lo, hi)
	})
}

func compositeRows(dst, src *image.RGBA, alpha float64, clip *image.Alpha, mode string, lo, hi int) {
	for y := lo; y < hi; y++ {
		for x := 0; x < dst.Rect.Dx(); x++ {
			i := y*src.Stride + x*4
			j := y*dst.Stride + x*4
			op := alpha
			if clip != nil {
				op *= float64(clip.Pix[y*clip.Stride+x]) / 255
			}
			sa := float64(src.Pix[i+3]) / 255 * op
			if sa == 0 {
				continue
			}
			da := float64(dst.Pix[j+3]) / 255
			for c := 0; c < 3; c++ {
				s := float64(src.Pix[i+c]) / 255 * op
				d := float64(dst.Pix[j+c]) / 255
				v := s + d*(1-sa)
				if mode != "" && mode != "normal" && da > 0 {
					su, du := s/sa, d/da
					b := su
					switch mode {
					case "multiply":
						b = su * du
					case "screen":
						b = su + du - su*du
					case "overlay":
						if du <= .5 {
							b = 2 * su * du
						} else {
							b = 1 - 2*(1-su)*(1-du)
						}
					}
					v = s*(1-da) + d*(1-sa) + sa*da*b
				}
				dst.Pix[j+c] = uint8(max(0, min(255, v*255+.5)))
			}
			dst.Pix[j+3] = uint8((sa+da*(1-sa))*255 + .5)
		}
	}
}
func (r *render) post(dst *image.RGBA, effects []scene.Effect) {
	for _, e := range effects {
		amount := r.n(e.Amount, 0)
		seed := e.Seed + uint32(r.b.Number("seed", 7))
		frame := int(r.t * 24)
		parallelLines(r.h, func(lo, hi int) {
			for y := lo; y < hi; y++ {
				for x := 0; x < r.w; x++ {
					i := y*dst.Stride + x*4
					v := 1.0
					offset := 0.0
					switch e.Type {
					case "grain":
						offset = (random(seed, x+y*r.w+frame*100003) - .5) * min(amount, 1) * 24
					case "scanlines":
						if y%3 == 0 {
							v = 1 - amount
						}
					case "vignette":
						dx, dy := (float64(x)/float64(r.w)-.5)*1.4, (float64(y)/float64(r.h)-.5)*1.4
						v = 1 - amount*min(1, dx*dx+dy*dy)
					case "flicker":
						v = 1 - amount*random(seed, frame)
					}
					for c := 0; c < 3; c++ {
						dst.Pix[i+c] = uint8(max(0, min(float64(dst.Pix[i+3]), float64(dst.Pix[i+c])*v+offset)))
					}
				}
			}
		})
	}
}
func bilinear(src image.Image, x, y float64) color.RGBA {
	b := src.Bounds()
	x = max(float64(b.Min.X), min(float64(b.Max.X-1), x))
	y = max(float64(b.Min.Y), min(float64(b.Max.Y-1), y))
	ix, iy := int(math.Floor(x)), int(math.Floor(y))
	a := color.RGBAModel.Convert(src.At(ix, iy)).(color.RGBA)
	bb := color.RGBAModel.Convert(src.At(min(ix+1, b.Max.X-1), iy)).(color.RGBA)
	c := color.RGBAModel.Convert(src.At(ix, min(iy+1, b.Max.Y-1))).(color.RGBA)
	d := color.RGBAModel.Convert(src.At(min(ix+1, b.Max.X-1), min(iy+1, b.Max.Y-1))).(color.RGBA)
	return mix(mix(a, bb, x-float64(ix)), mix(c, d, x-float64(ix)), y-float64(iy))
}
func (r *render) sample(dst *image.RGBA, l scene.Layer, m matrix, w, h float64) {
	src := r.b.Assets[r.s(l.Src)]
	if src == nil {
		return
	}
	b := src.Bounds()
	scale := min(w/float64(b.Dx()), h/float64(b.Dy()))
	if l.Fit != "contain" {
		scale = max(w/float64(b.Dx()), h/float64(b.Dy()))
	}
	inv := m.inverse()
	tint := rgba(r.s(l.Tint))
	for y := 0; y < r.h; y++ {
		for x := 0; x < r.w; x++ {
			p := inv.point(point{float64(x) + .5, float64(y) + .5})
			if p.x < 0 || p.x >= w || p.y < 0 || p.y >= h {
				continue
			}
			xx, yy := (p.x-w/2)/scale+float64(b.Min.X+b.Dx()/2), (p.y-h/2)/scale+float64(b.Min.Y+b.Dy()/2)
			if xx < float64(b.Min.X) || xx >= float64(b.Max.X) || yy < float64(b.Min.Y) || yy >= float64(b.Max.Y) {
				continue
			}
			c := bilinear(src, xx, yy)
			if l.TintBlend == "silhouette" {
				c.R = uint8(uint16(c.A) * uint16(tint.R) / 255)
				c.G = uint8(uint16(c.A) * uint16(tint.G) / 255)
				c.B = uint8(uint16(c.A) * uint16(tint.B) / 255)
			} else if l.Tint != nil {
				c.R = uint8(uint16(c.R) * uint16(tint.R) / 255)
				c.G = uint8(uint16(c.G) * uint16(tint.G) / 255)
				c.B = uint8(uint16(c.B) * uint16(tint.B) / 255)
			}
			dst.SetRGBA(x, y, c)
		}
	}
}

// Pixel behaviours operate on premultiplied channels and preserve the alpha mask.
func (r *render) behaviours(src *image.RGBA, l scene.Layer) {
	for _, b := range l.Behave {
		if p := b.Glint; p != nil {
			speed := p.Speed
			if speed == 0 {
				speed = 1
			}
			center := math.Mod(max(0, r.t-p.Start)*speed*.3, 1.5) - .25
			amount := p.Amplitude
			if amount == 0 {
				amount = .5
			}
			for y := 0; y < r.h; y++ {
				for x := 0; x < r.w; x++ {
					i := y*src.Stride + x*4
					k := max(0, 1-math.Abs(float64(x)/float64(r.w)+float64(y)/float64(r.h)*.2-center)/.06) * amount
					for c := 0; c < 3; c++ {
						src.Pix[i+c] = uint8(float64(src.Pix[i+c]) + (float64(src.Pix[i+3])-float64(src.Pix[i+c]))*min(1, k))
					}
				}
			}
		}
		if p := b.Chroma; p != nil {
			amp := p.Amplitude
			if amp == 0 {
				amp = 3
			}
			dx := int(math.Round(math.Sin((r.t-p.Start)*max(1, p.Speed)*6.28) * amp * float64(r.w) / 1920))
			copyPixels := append([]byte(nil), src.Pix...)
			for y := 0; y < r.h; y++ {
				for x := 0; x < r.w; x++ {
					i := y*src.Stride + x*4
					for _, c := range []int{0, 2} {
						xx := x + dx
						if c == 2 {
							xx = x - dx
						}
						if xx >= 0 && xx < r.w {
							src.Pix[i+c] = min(src.Pix[i+3], copyPixels[y*src.Stride+xx*4+c])
						}
					}
				}
			}
		}
	}
}
