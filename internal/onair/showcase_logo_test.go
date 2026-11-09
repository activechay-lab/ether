package onair

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/image/draw"

	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
)

// TestRenderShowcaseLogos draws the shipped decade channels' guide logos from
// their looks' station bug ("Corner logo") scenes: transparent 640x360 PNGs
// committed under internal/livetv/showcaselogos. Apple TV fetches them from a
// public URL pinned to the release, so artwork changes go to a new versioned
// directory and never overwrite a published one. Regenerate with
//
//	ETHER_RENDER_SHOWCASE_LOGOS=../livetv/showcaselogos/v2 go test ./internal/onair -run TestRenderShowcaseLogos
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
		}, Slots: map[string]string{"channel.name": l.Name}}
		for _, c := range l.Controls {
			b.Tokens["control."+c.ID] = c.Value
		}
		// Textures stay off: a guide logo is a clean mark, not a video frame.
		for _, tex := range []string{"grain", "flicker", "scanlines", "vignette", "chroma"} {
			b.Tokens["textures."+tex] = 0.0
		}
		// The bug is drawn for a 1080p frame; render at twice that and keep
		// only the mark, then fit it with a margin into the guide's 16:9 box.
		frame := raster.Render(doc, raster.Bindings{Bindings: b}, doc.Poster, 3840, 2160)
		bounds := image.Rectangle{}
		for y := 0; y < frame.Rect.Dy(); y++ {
			for x := 0; x < frame.Rect.Dx(); x++ {
				if frame.RGBAAt(x, y).A > 4 {
					bounds = bounds.Union(image.Rect(x, y, x+1, y+1))
				}
			}
		}
		if bounds.Empty() {
			t.Fatalf("%s: empty bug", v.id)
		}
		const width, height, margin = 640, 360, 24
		scale := min(float64(width-2*margin)/float64(bounds.Dx()), float64(height-2*margin)/float64(bounds.Dy()))
		w, h := int(float64(bounds.Dx())*scale+.5), int(float64(bounds.Dy())*scale+.5)
		logo := image.NewNRGBA(image.Rect(0, 0, width, height))
		at := image.Rect((width-w)/2, (height-h)/2, (width-w)/2+w, (height-h)/2+h)
		draw.CatmullRom.Scale(logo, at, frame, bounds, draw.Src, nil)
		var out bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, logo); err != nil {
			t.Fatal(err)
		}
		name := strings.ReplaceAll(strings.ToLower(l.Name), " ", "-") + ".png"
		if err := os.WriteFile(filepath.Join(dir, name), out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
