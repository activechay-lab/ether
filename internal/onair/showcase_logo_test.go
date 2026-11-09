package onair

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/draw"

	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
)

// TestRenderShowcaseLogos draws the shipped decade channels' guide logos from
// their looks' station bug ("Corner logo") scenes, recomposed for a TV guide's
// channel cell: the name stacked over a large decade number, beside the
// look's emblem, filling a transparent 640x360 PNG. Plex for Apple TV fits a
// logo to about 83 px wide, so a single-line bug drew its name ten pixels
// tall. The files are committed under internal/livetv/showcaselogos. Apple TV
// fetches them from a public URL pinned to the release, so artwork changes go
// to a new versioned directory and never overwrite a published one.
// Regenerate with
//
//	ETHER_RENDER_SHOWCASE_LOGOS=../livetv/showcaselogos/v3 go test ./internal/onair -run TestRenderShowcaseLogos
func TestRenderShowcaseLogos(t *testing.T) {
	dir := os.Getenv("ETHER_RENDER_SHOWCASE_LOGOS")
	if dir == "" {
		t.Skip("set ETHER_RENDER_SHOWCASE_LOGOS to a directory to render")
	}
	for _, v := range showcaseLooks() {
		l := v.look()
		var doc scene.Document
		for _, sc := range l.Scenes {
			if sc.Role == "bug" {
				doc = sc.Document
			}
		}
		if len(doc.Layers) == 0 {
			t.Fatalf("%s: no bug scene", v.id)
		}
		b := scene.Bindings{Tokens: map[string]any{
			"bg": l.Brand.Palette.Background, "accent": l.Brand.Palette.Accent, "text": l.Brand.Palette.Text, "glow": l.Brand.Palette.Glow,
			"type.display": normalizeFont(l.Brand.Type.Display), "type.body": normalizeFont(l.Brand.Type.Body), "type.mono": normalizeFont(l.Brand.Type.Body),
			"energy": l.Brand.Energy, "words.tagline": l.Brand.Tagline,
		}, Slots: map[string]string{}}
		for _, c := range l.Controls {
			b.Tokens["control."+c.ID] = c.Value
		}
		// Textures stay off: a guide logo is a clean mark, not a video frame.
		for _, tex := range []string{"grain", "flicker", "scanlines", "vignette", "chroma"} {
			b.Tokens["textures."+tex] = 0.0
		}
		cut := strings.LastIndex(l.Name, " ")
		if cut < 0 {
			t.Fatalf("%s: name %q has no decade", v.id, l.Name)
		}
		word, decade := l.Name[:cut], l.Name[cut+1:]
		m := mark{t: t, id: v.id, doc: doc, b: b}
		var c composition
		switch v.family {
		case "retro":
			// Chrome name over a chrome decade flanked by the bug's stripes.
			name := m.piece(word, "bug-mark")
			num := m.piece(decade, "bug-mark")
			stripes := m.piece("", "bug-stripes")
			c.place(name, 0, 0, 100)
			nw := name.Bounds().Dx() * 100 / name.Bounds().Dy()
			nh := 132.0
			numW := float64(num.Bounds().Dx()) * nh / float64(num.Bounds().Dy())
			y := 100 + 4.0
			c.place(num, (float64(nw)-numW)/2, y, nh)
			side := (float64(nw)-numW)/2 - 14
			c.stretch(stripes, 0, y+nh*.34, side, nh*.4)
			c.stretch(stripes, float64(nw)-side, y+nh*.34, side, nh*.4)
		case "grunge":
			// The torn paper chip around the name stacked over the decade,
			// with the tape on its corner.
			chip := m.piece("", "bug-chip-shadow", "bug-chip-torn-fibre", "bug-chip")
			tape := m.piece("", "bug-tape")
			text := []string{"bug-wordmark-shadow", "bug-wordmark"}
			name, num := m.piece(word, text...), m.piece(decade, text...)
			nw := float64(name.Bounds().Dx()) * 100 / float64(name.Bounds().Dy())
			numW := float64(num.Bounds().Dx()) * 100 / float64(num.Bounds().Dy())
			tw := max(nw, numW)
			cw, ch := tw+90, 2*100+12+76.0
			c.stretch(chip, 0, 0, cw, ch)
			c.place(name, (cw-nw)/2, 38, 100)
			c.place(num, (cw-numW)/2, 38+100+12, 100)
			c.place(tape, -cw*.07, -ch*.08, ch*.26)
		case "aqua", "flat":
			// The emblem beside the name stacked over a large decade.
			var emblem *image.NRGBA
			if v.family == "aqua" {
				emblem = m.piece("", "bug-emblem")
			} else {
				emblem = m.piece("", "bug-tile-shadow", "bug-mark")
			}
			text := []string{"bug-name-shadow", "bug-name"}
			name, num := m.piece(word, text...), m.piece(decade, text...)
			nameH, numH, gap := 80.0, 112.0, 10.0
			nw := float64(name.Bounds().Dx()) * nameH / float64(name.Bounds().Dy())
			numW := float64(num.Bounds().Dx()) * numH / float64(num.Bounds().Dy())
			h := nameH + gap + numH
			eh := h * .86
			ew := float64(emblem.Bounds().Dx()) * eh / float64(emblem.Bounds().Dy())
			c.place(emblem, 0, (h-eh)/2, eh)
			x := ew + 18
			// Space Grotesk ships as its light default instance, too thin
			// to read eighty pixels wide; embolden it.
			weight := 0.0
			if v.family == "aqua" {
				weight = 2.4
			}
			c.bold(name, x, 0, nameH, weight)
			c.bold(num, x+(nw-numW)/2, nameH+gap, numH, weight)
		default:
			t.Fatalf("%s: no guide composition for family %q", v.id, v.family)
		}
		var out bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, c.render(640, 360, 10)); err != nil {
			t.Fatal(err)
		}
		file := strings.ReplaceAll(strings.ToLower(l.Name), " ", "-") + ".png"
		if err := os.WriteFile(filepath.Join(dir, file), out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// mark draws parts of a look's bug scene on their own.
type mark struct {
	t   *testing.T
	id  string
	doc scene.Document
	b   scene.Bindings
}

// piece renders only the named layers (with their children) of the bug,
// with name in the channel name slot, at full strength and cropped to its
// ink. The bug is drawn for a 1080p frame, so the piece is found at frame
// size, then redrawn zoomed to fill the frame for a sharp source.
func (m mark) piece(name string, keep ...string) *image.NRGBA {
	m.t.Helper()
	var doc scene.Document
	data, _ := json.Marshal(m.doc)
	if err := json.Unmarshal(data, &doc); err != nil {
		m.t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, id := range keep {
		ids[id] = true
	}
	var show func(ls []scene.Layer, kept bool) bool
	show = func(ls []scene.Layer, kept bool) bool {
		any := false
		for i := range ls {
			l := &ls[i]
			k := kept || ids[l.ID]
			child := show(l.Layers, k)
			l.Hidden = !k && !child
			any = any || k || child
		}
		return any
	}
	if !show(doc.Layers, false) {
		m.t.Fatalf("%s: no layers %v", m.id, keep)
	}
	// Entrance fades and the bug's translucency are for video; a logo is solid.
	for i := range doc.Layers {
		doc.Layers[i].Opacity = 1.0
	}
	b := m.b
	b.Slots = map[string]string{"channel.name": name}
	rb := raster.Bindings{Bindings: b}
	bounds := ink(raster.Render(doc, rb, doc.Poster, 1920, 1080))
	if bounds.Empty() {
		m.t.Fatalf("%s: empty piece %v", m.id, keep)
	}
	bounds = bounds.Inset(-12).Intersect(image.Rect(0, 0, 1920, 1080))
	// Scenes cap blur at 64, which bounds the zoom of a blurred halo.
	zoom := min(1920/float64(bounds.Dx()), 1080/float64(bounds.Dy()), 7)
	var scale func(ls []scene.Layer)
	scale = func(ls []scene.Layer) {
		for i := range ls {
			ls[i].Blur *= zoom
			ls[i].Glow *= zoom
			scale(ls[i].Layers)
		}
	}
	scale(doc.Layers)
	doc.Layers = []scene.Layer{{ID: "guide-zoom", Type: "group", X: -float64(bounds.Min.X) * zoom, Y: -float64(bounds.Min.Y) * zoom, Scale: zoom, Layers: doc.Layers}}
	frame := raster.Render(doc, rb, doc.Poster, 1920, 1080)
	src := ink(frame)
	out := image.NewNRGBA(image.Rect(0, 0, src.Dx(), src.Dy()))
	draw.Draw(out, out.Rect, frame, src.Min, draw.Src)
	return out
}

func ink(img *image.RGBA) image.Rectangle {
	r := image.Rectangle{}
	for y := 0; y < img.Rect.Dy(); y++ {
		for x := 0; x < img.Rect.Dx(); x++ {
			if img.RGBAAt(x, y).A > 3 {
				r = r.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return r
}

// composition lays pieces out in free units, then fits them to the logo.
type composition struct {
	items []struct {
		img        *image.NRGBA
		x, y, w, h float64
	}
}

// place draws img at height h, keeping its proportions.
func (c *composition) place(img *image.NRGBA, x, y, h float64) {
	c.stretch(img, x, y, float64(img.Rect.Dx())*h/float64(img.Rect.Dy()), h)
}

// bold places img at height h thickened by r units, by drawing it around a
// circle of that radius.
func (c *composition) bold(img *image.NRGBA, x, y, h, r float64) {
	if r > 0 {
		for i := range 12 {
			a := float64(i) * math.Pi / 6
			c.place(img, x+r*math.Cos(a), y+r*math.Sin(a), h)
		}
	}
	c.place(img, x, y, h)
}

func (c *composition) stretch(img *image.NRGBA, x, y, w, h float64) {
	c.items = append(c.items, struct {
		img        *image.NRGBA
		x, y, w, h float64
	}{img, x, y, w, h})
}

// render centres the layout in a w x h canvas, as large as fits inside margin.
func (c *composition) render(w, h, margin int) *image.NRGBA {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, it := range c.items {
		minX, minY = min(minX, it.x), min(minY, it.y)
		maxX, maxY = max(maxX, it.x+it.w), max(maxY, it.y+it.h)
	}
	s := min(float64(w-2*margin)/(maxX-minX), float64(h-2*margin)/(maxY-minY))
	ox := (float64(w)-(maxX-minX)*s)/2 - minX*s
	oy := (float64(h)-(maxY-minY)*s)/2 - minY*s
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for _, it := range c.items {
		r := image.Rect(int(math.Round(ox+it.x*s)), int(math.Round(oy+it.y*s)), int(math.Round(ox+(it.x+it.w)*s)), int(math.Round(oy+(it.y+it.h)*s)))
		draw.CatmullRom.Scale(out, r, it.img, it.img.Rect, draw.Over, nil)
	}
	return out
}
