package raster

import (
	"image"
	"math"
	"strings"

	"github.com/braedonsaunders/ether/internal/onair/fonts"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

func (r *render) text(dst *image.RGBA, l scene.Layer, m matrix) {
	id := r.s(l.Font)
	parsed := r.parsed[id]
	if parsed == nil {
		parsed = r.b.Fonts[id]
		if parsed == nil {
			parsed, _ = fonts.Parse(id)
		}
		r.parsed[id] = parsed
	}
	if parsed == nil {
		return
	}
	// Fall back for missing Greek/Cyrillic/Latin glyphs. A future caller can
	// extend fonts.Bytes with the separately managed CJK font store.
	var sfntBuffer sfnt.Buffer
	for _, ch := range r.s(l.Text) {
		index, _ := parsed.GlyphIndex(&sfntBuffer, ch)
		if index == 0 {
			fallback := r.parsed["notosans"]
			if fallback == nil {
				fallback, _ = fonts.Parse("notosans")
				r.parsed["notosans"] = fallback
			}
			if fallback != nil {
				parsed = fallback
			}
			break
		}
	}
	size := r.n(l.Size, 60)
	track := r.n(l.Track, 0)
	text := r.s(l.Text)
	for _, b := range l.Behave {
		if p := b.Typewriter; p != nil {
			n := int(max(0, math.Floor((r.t-p.Start)/max(.001, p.Step))+1))
			rs := []rune(text)
			text = string(rs[:min(len(rs), n)])
		}
	}
	// Render locally at target density, then transform the glyph mask. Faces belong to this call.
	density := math.Hypot(m.a, m.b)
	if density < .001 {
		return
	}
	face, e := opentype.NewFace(parsed, &opentype.FaceOptions{Size: size * density, DPI: 72, Hinting: font.HintingNone})
	if e != nil {
		return
	}
	defer func() { face.Close() }()
	measure := func(s string) float64 {
		return float64(font.MeasureString(face, s))/64/density + float64(max(0, len([]rune(s))-1))*track
	}
	if l.Fit == "shrink" && l.MaxWidth > 0 && measure(text) > l.MaxWidth {
		ratio := l.MaxWidth / measure(text)
		size *= ratio
		track *= ratio
		face.Close()
		face, _ = opentype.NewFace(parsed, &opentype.FaceOptions{Size: size * density, DPI: 72})
		if face == nil {
			return
		}
	}
	lines := []string{text}
	if l.Fit == "wrap" && l.MaxWidth > 0 {
		lines = nil
		line := ""
		for _, word := range strings.Fields(text) {
			test := word
			if line != "" {
				test = line + " " + word
			}
			if line != "" && measure(test) > l.MaxWidth {
				lines = append(lines, line)
				line = word
			} else {
				line = test
			}
		}
		lines = append(lines, line)
	}
	if l.MaxLines > 0 && len(lines) > l.MaxLines {
		lines = lines[:l.MaxLines]
		last := []rune(lines[len(lines)-1])
		for len(last) > 0 && measure(string(last)+"…") > l.MaxWidth {
			last = last[:len(last)-1]
		}
		lines[len(lines)-1] = string(last) + "…"
	}
	maxW := 0.0
	for _, line := range lines {
		maxW = max(maxW, measure(line))
	}
	if l.MaxWidth > 0 {
		maxW = min(maxW, l.MaxWidth)
	}
	if maxW <= 0 {
		return
	}
	mask := image.NewAlpha(image.Rect(0, 0, int(math.Ceil((maxW+size)*density)), int(math.Ceil((size*1.25*float64(len(lines))+size)*density))))
	for j, line := range lines {
		width := measure(line)
		x := size / 2
		if l.Align == "center" {
			x += (maxW - width) / 2
		} else if l.Align == "right" {
			x += maxW - width
		}
		d := font.Drawer{Dst: mask, Src: image.White, Face: face, Dot: fixed.Point26_6{X: fixed.Int26_6(x * density * 64), Y: fixed.Int26_6((size + float64(j)*size*1.15) * density * 64)}}
		prev := rune(-1)
		for _, ch := range line {
			if prev >= 0 {
				d.Dot.X += face.Kern(prev, ch) + fixed.Int26_6(track*density*64)
			}
			d.DrawString(string(ch))
			prev = ch
		}
	}
	if l.Weight >= 600 {
		radius := max(1, int(math.Round(size*density*.012)))
		original := append([]byte(nil), mask.Pix...)
		for y := 0; y < mask.Rect.Dy(); y++ {
			for x := 0; x < mask.Rect.Dx(); x++ {
				a := original[y*mask.Stride+x]
				for dx := 1; dx <= radius && x >= dx; dx++ {
					a = max(a, original[y*mask.Stride+x-dx])
				}
				mask.Pix[y*mask.Stride+x] = a
			}
		}
	}
	ox := -size / 2
	if l.Align == "center" {
		ox -= maxW / 2
	} else if l.Align == "right" {
		ox -= maxW
	}
	inv := m.inverse()
	out := image.NewAlpha(dst.Bounds())
	for y := 0; y < r.h; y++ {
		for x := 0; x < r.w; x++ {
			p := inv.point(point{float64(x) + .5, float64(y) + .5})
			if l.MaxWidth > 0 && l.Align != "center" && l.Align != "right" && (p.x < 0 || p.x >= l.MaxWidth) {
				continue
			}
			xx, yy := (p.x-ox)*density, (p.y+size)*density
			if xx >= 0 && yy >= 0 && xx < float64(mask.Rect.Dx()) && yy < float64(mask.Rect.Dy()) {
				out.Pix[y*out.Stride+x] = mask.AlphaAt(int(xx), int(yy)).A
			}
		}
	}
	r.paint(dst, out, l.Fill, m)
}
