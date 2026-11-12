package raster

import (
	"encoding/json"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"golang.org/x/image/font/opentype"
	"image"
	"image/draw"
	"math"
	"sync"
)

// cachedRun retains tightly cropped static regions in painter order.
type layerPlate struct {
	key   string
	image *image.RGBA
}

func cropPlate(full *image.RGBA) *image.RGBA {
	bounds := image.Rectangle{}
	for y := full.Rect.Min.Y; y < full.Rect.Max.Y; y++ {
		for x := full.Rect.Min.X; x < full.Rect.Max.X; x++ {
			if full.Pix[full.PixOffset(x, y)+3] != 0 {
				bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	dst := image.NewRGBA(bounds)
	draw.Draw(dst, bounds, full, bounds.Min, draw.Src)
	return dst
}

type cachedRun struct {
	layers []scene.Layer
	image  *image.RGBA
	ready  bool
}

// Sequence renders static regions once and reuses immutable unchanged frames.
type Sequence struct {
	mu              sync.Mutex
	doc             scene.Document
	bind            Bindings
	w, h            int
	prefix          int
	runs            []cachedRun
	plate, previous *image.RGBA
	key             string
	state           any
	procedural      bool
	cache           map[string]layerPlate
	RasterCalls     int
}

func NewSequence(d scene.Document, b Bindings, w, h int) *Sequence {
	s := &Sequence{doc: d, bind: b, w: w, h: h, cache: map[string]layerPlate{}}
	for _, l := range d.Layers {
		if moving(l) {
			break
		}
		s.prefix++
	}
	// Keep static runs in painter order. Destination-dependent blend modes
	// stay in the animated pass so their result sees the actual lower layers.
	for _, l := range d.Layers[s.prefix:] {
		cacheable := !moving(l) && plainBlend(l)
		if len(s.runs) == 0 || (s.runs[len(s.runs)-1].image != nil) != cacheable {
			run := cachedRun{}
			if cacheable {
				run.image = image.NewRGBA(image.Rectangle{})
			}
			s.runs = append(s.runs, run)
		}
		i := len(s.runs) - 1
		s.runs[i].layers = append(s.runs[i].layers, l)
	}
	bts, _ := json.Marshal(d.Layers[s.prefix:])
	_ = json.Unmarshal(bts, &s.state)
	s.procedural = proceduralLayers(d.Layers[s.prefix:])
	return s
}
func proceduralLayers(ls []scene.Layer) bool {
	for _, l := range ls {
		if l.Start != 0 || l.End != 0 || len(l.Behave) > 0 || l.Reveal != nil {
			return true
		}
		switch l.Type {
		case "noise", "blob", "particles", "grid":
			return true
		}
		if proceduralLayers(l.Layers) {
			return true
		}
	}
	return false
}
func plainBlend(l scene.Layer) bool {
	if l.Blend != "" && l.Blend != "normal" {
		return false
	}
	for _, c := range l.Layers {
		if !plainBlend(c) {
			return false
		}
	}
	return true
}
func moving(l scene.Layer) bool {
	for _, c := range l.Layers {
		if moving(c) {
			return true
		}
	}
	if l.Start != 0 || l.End != 0 || len(l.Behave) > 0 || l.Reveal != nil {
		return true
	}
	switch l.Type {
	case "noise", "blob", "particles", "grid":
		return true
	}
	b, _ := json.Marshal(l)
	var v any
	_ = json.Unmarshal(b, &v)
	var animated func(any) bool
	animated = func(v any) bool {
		switch x := v.(type) {
		case map[string]any:
			if _, ok := x["keys"]; ok {
				return true
			}
			for _, v := range x {
				if animated(v) {
					return true
				}
			}
		case []any:
			for _, v := range x {
				if animated(v) {
					return true
				}
			}
		}
		return false
	}
	return animated(v)
}
func evaluated(v any, b scene.Bindings, t float64) any {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["keys"]; ok {
			return scene.Eval(x, b, t)
		}
		out := map[string]any{}
		for k, v := range x {
			out[k] = evaluated(v, b, t)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = evaluated(v, b, t)
		}
		return out
	default:
		return scene.Eval(v, b, t)
	}
}
func (s *Sequence) Frame(t float64) *image.RGBA {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.plate == nil {
		d := s.doc
		d.Layers = d.Layers[:s.prefix]
		d.Post = nil
		s.plate = Render(d, s.bind, 0, s.w, s.h)
		s.RasterCalls++
	}
	d := s.doc
	d.Layers = d.Layers[s.prefix:]
	state := []any{evaluated(s.state, s.bind.Bindings, t)}
	if s.procedural {
		state = append(state, t)
	}
	for _, effect := range d.Post {
		state = append(state, effect.Type, scene.Eval(effect.Amount, s.bind.Bindings, t))
		if effect.Type == "grain" || effect.Type == "flicker" {
			state = append(state, math.Floor(t*24))
		}
	}
	key, _ := json.Marshal(state)
	if s.previous != nil && s.key == string(key) {
		return s.previous
	}
	dst := image.NewRGBA(s.plate.Rect)
	copy(dst.Pix, s.plate.Pix)
	r := render{s.bind, t, s.w, s.h, map[string]*opentype.Font{}, s.cache}
	for i := range s.runs {
		run := &s.runs[i]
		if run.image != nil {
			if !run.ready {
				run.ready = true
				doc := s.doc
				doc.Layers = run.layers
				doc.Post = nil
				full := Render(doc, s.bind, 0, s.w, s.h)
				s.RasterCalls++
				run.image = cropPlate(full)
			}
			draw.Draw(dst, run.image.Rect, run.image, run.image.Rect.Min, draw.Over)
		} else {
			r.layers(dst, run.layers, matrix{a: float64(s.w) / 1920, d: float64(s.h) / 1080}, 1, nil)
			s.RasterCalls++
		}
	}

	r.post(dst, d.Post)
	s.key = string(key)
	s.previous = dst
	return dst
}
