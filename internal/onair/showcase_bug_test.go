package onair

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/draw"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"github.com/braedonsaunders/ether/internal/store"
)

// TestRenderShowcaseBugs generates the shipped decade channels' compact corner
// bug marks (internal/onair/showcasebugs/*.png) from their looks, family parts
// and bundled fonts. Regenerate with:
//
//	ETHER_RENDER_SHOWCASE_BUGS=showcasebugs go test ./internal/onair -run TestRenderShowcaseBugs
func TestRenderShowcaseBugs(t *testing.T) {
	dir := os.Getenv("ETHER_RENDER_SHOWCASE_BUGS")
	if dir == "" {
		t.Skip("set ETHER_RENDER_SHOWCASE_BUGS to a directory to render")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, v := range showcaseLooks() {
		img := renderShowcaseBugMark(t, v)
		var out bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestCompression}).Encode(&out, img); err != nil {
			t.Fatal(err)
		}
		file := showcaseBugNames[v.id]
		if file == "" {
			t.Fatalf("%s: missing showcaseBugNames entry", v.id)
		}
		if err := os.WriteFile(filepath.Join(dir, file), out.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func renderShowcaseBugMark(t *testing.T, v showcaseLook) *image.NRGBA {
	t.Helper()
	l := v.look()
	doc, err := families.Get(v.family, "bug")
	if err != nil {
		t.Fatalf("%s: %v", v.id, err)
	}
	b := scene.Bindings{Tokens: map[string]any{
		"bg": l.Brand.Palette.Background, "accent": l.Brand.Palette.Accent, "text": l.Brand.Palette.Text, "glow": l.Brand.Palette.Glow,
		"type.display": normalizeFont(l.Brand.Type.Display), "type.body": normalizeFont(l.Brand.Type.Body), "type.mono": normalizeFont(l.Brand.Type.Body),
		"energy": l.Brand.Energy, "words.tagline": l.Brand.Tagline,
	}, Slots: map[string]string{}}
	for _, ctrl := range l.Controls {
		b.Tokens["control."+ctrl.ID] = ctrl.Value
	}
	for _, tex := range []string{"grain", "flicker", "scanlines", "vignette", "chroma"} {
		b.Tokens["textures."+tex] = 0.0
	}
	cut := strings.LastIndex(l.Name, " ")
	if cut < 0 {
		t.Fatalf("%s: name %q has no decade", v.id, l.Name)
	}
	decade := l.Name[cut+1:]
	m := mark{t: t, id: v.id, doc: doc, b: b}
	var c composition

	switch v.family {
	case "retro":
		// Crisp airbrushed chrome "80" with a vector-sharp midnight keyline (sourced
		// from the non-glowing glyph layers so no soft halo is tinted dark) above
		// the four angled CHROMA spectrum stripe bars.
		numCore := m.piece(decade, "bug-wm-x4", "bug-wm-x3", "bug-wm-x2", "bug-wm-x1", "bug-wm-rim", "bug-wm-glint")
		numKey := tintAlpha(numCore, color.NRGBA{0x07, 0x08, 0x24, 0xf2})
		bars := renderRetroBugStripes(b)
		nh := 130.0
		nw := float64(numCore.Bounds().Dx()) * nh / float64(numCore.Bounds().Dy())
		bw := nw * 1.02
		bh := 42.0
		c.bold(numKey, 0, 1, nh, 3.2)
		c.place(numCore, 0, 0, nh)
		c.stretch(bars, (nw-bw)/2-3, nh-4, bw, bh)

	case "grunge":
		// Compact zine torn-paper sticker "90" with corner masking tape and
		// screen-print yellow/magenta misregistration.
		chip := m.piece("", "bug-chip-shadow", "bug-chip-torn-fibre", "bug-chip")
		chip = withDarkHalo(chip, 3, 0.36)
		tape := m.piece("", "bug-tape")
		tape = withDarkHalo(tape, 2, 0.28)
		numWhite := m.piece(decade, "bug-wordmark")
		numPink := tintAlpha(numWhite, color.NRGBA{0xed, 0x4c, 0x99, 0xff})
		numAcid := tintAlpha(numWhite, color.NRGBA{0xed, 0xff, 0x38, 0xff})
		nh := 104.0
		nw := float64(numWhite.Bounds().Dx()) * nh / float64(numWhite.Bounds().Dy())
		cw, ch := nw+84, 172.0
		c.stretch(chip, 0, 0, cw, ch)
		nx, ny := (cw-nw)/2, (ch-nh)/2+2
		c.place(numAcid, nx-5, ny-4, nh)
		c.place(numPink, nx+6, ny+6, nh)
		c.place(numWhite, nx, ny, nh)
		c.place(tape, -cw*0.045, -ch*0.065, ch*0.29)

	case "aqua":
		// Y2K Aqua glass orb with clean luminous planetary ring and icy "00" badge.
		emblem := m.piece("", "bug-emblem")
		num := m.piece(decade, "bug-name")
		numShadow := tintAlpha(num, color.NRGBA{0x01, 0x0c, 0x1a, 0x70})
		numKeyline := tintAlpha(num, color.NRGBA{0x02, 0x18, 0x33, 0xf2})
		eh := 174.0
		ew := float64(emblem.Bounds().Dx()) * eh / float64(emblem.Bounds().Dy())
		nh := 98.0
		c.place(emblem, 0, 0, eh)
		nx, ny := ew*0.44, eh*0.31
		c.bold(numShadow, nx+2, ny+3, nh, 4.2)
		c.bold(numKeyline, nx, ny, nh, 4.0)
		c.bold(num, nx, ny, nh, 2.2)

	case "flat":
		// Flat rounded coral tile "10" with true 45-degree extruded long shadow
		// and upward shift arrow.
		return renderFlatBugTile(b, decade)

	default:
		t.Fatalf("%s: no bug composition for family %q", v.id, v.family)
	}
	return c.render(300, 200, 10)
}

func renderRetroBugStripes(b scene.Bindings) *image.NRGBA {
	colors := []string{"{accent}", "{control.detail5}", "{control.detail3}", "{glow}"}
	var shadows, bars []scene.Layer
	for i, fill := range colors {
		y0 := float64(i) * 30.0
		dx := -float64(i) * 9.0
		path := fmt.Sprintf("M%.1f %.1f L%.1f %.1f L%.1f %.1f L%.1f %.1f Z",
			40+dx, y0, 560+dx, y0, 548+dx, y0+20, 28+dx, y0+20)
		shadows = append(shadows, scene.Layer{
			ID:          fmt.Sprintf("stripe-bg-%d", i),
			Type:        "path",
			Path:        path,
			Fill:        "#070824@0.82",
			Stroke:      "#070824@0.82",
			StrokeWidth: 6,
			Join:        "round",
		})
		bars = append(bars, scene.Layer{
			ID:   fmt.Sprintf("stripe-fg-%d", i),
			Type: "path",
			Path: path,
			Fill: fill,
		})
	}
	layers := append(shadows, bars...)
	doc := scene.Document{
		V: 1, Moment: "bug", Duration: 8, Poster: 4,
		Layers: []scene.Layer{{ID: "stripes-root", Type: "group", X: 160, Y: 180, Scale: 2.6, Layers: layers}},
	}
	frame := raster.Render(doc, raster.Bindings{Bindings: b}, 4, 1920, 1080)
	src := ink(frame)
	out := image.NewNRGBA(image.Rect(0, 0, src.Dx(), src.Dy()))
	draw.Draw(out, out.Rect, frame, src.Min, draw.Src)
	return out
}

func renderFlatBugTile(b scene.Bindings, decade string) *image.NRGBA {
	tilePath := "M -132 -96 H 132 A 44 44 0 0 1 176 -52 V 52 A 44 44 0 0 1 132 96 H -132 A 44 44 0 0 1 -176 52 V -52 A 44 44 0 0 1 -132 -96 Z"
	rb := raster.Bindings{Bindings: b}

	baseDoc := scene.Document{
		V: 1, Moment: "bug", Duration: 8, Poster: 2,
		Layers: []scene.Layer{{
			ID: "flat-base", Type: "group", X: 960, Y: 540, Scale: 2.8,
			Layers: []scene.Layer{
				{ID: "tile-drop", Type: "path", Path: tilePath, X: 0, Y: 5, Fill: "#06131a@0.48", Blur: 5},
				{ID: "tile-key", Type: "path", Path: tilePath, Fill: "#0a1c24@0.55", Stroke: "#0a1c24@0.55", StrokeWidth: 3.5, Join: "round"},
				{ID: "tile-rim", Type: "path", Path: tilePath, Fill: "{accent}", Stroke: "#ffffff@0.32", StrokeWidth: 2.2, Join: "round"},
				{ID: "tile-body", Type: "path", Path: tilePath, Fill: "{accent}"},
			},
		}},
	}
	maskDoc := scene.Document{
		V: 1, Moment: "bug", Duration: 8, Poster: 2,
		Layers: []scene.Layer{{
			ID: "flat-mask", Type: "group", X: 960, Y: 540, Scale: 2.8,
			Layers: []scene.Layer{{ID: "tile-mask", Type: "path", Path: tilePath, Fill: "#ffffff"}},
		}},
	}
	fgDoc := scene.Document{
		V: 1, Moment: "bug", Duration: 8, Poster: 2,
		Layers: []scene.Layer{{
			ID: "flat-fg", Type: "group", X: 960, Y: 540, Scale: 2.8,
			Layers: []scene.Layer{
				{
					ID: "tile-chevron", Type: "path",
					Path: "M -128 -18 L -98 -52 L -68 -18 H -86 V 46 H -110 V -18 Z",
					Fill: "{glow}",
				},
				{
					ID: "tile-num", Type: "text",
					Text: decade, Font: "{type.display}", Size: 132, Weight: 700,
					X: 26, Y: 46, Align: "center", Fill: "{text}",
				},
			},
		}},
	}

	base := raster.Render(baseDoc, rb, 2, 1920, 1080)
	mask := raster.Render(maskDoc, rb, 2, 1920, 1080)
	fg := raster.Render(fgDoc, rb, 2, 1920, 1080)

	// Cast a true 45-degree flat-design long shadow from the arrow and numerals,
	// clipped cleanly to the rounded coral tile interior.
	bounds := ink(base)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			ma := mask.RGBAAt(x, y).A
			if ma == 0 {
				continue
			}
			var maxCast uint8
			for d := 1; d <= 360; d++ {
				sx, sy := x-d, y-d
				if sx < bounds.Min.X || sy < bounds.Min.Y {
					break
				}
				if a := fg.RGBAAt(sx, sy).A; a > maxCast {
					maxCast = a
					if maxCast == 255 {
						break
					}
				}
			}
			if maxCast == 0 {
				continue
			}
			sa := float64(maxCast) / 255.0 * (float64(ma) / 255.0) * 0.24
			dst := base.RGBAAt(x, y)
			base.SetRGBA(x, y, color.RGBA{
				R: uint8(math.Round(float64(dst.R) * (1 - sa))),
				G: uint8(math.Round(float64(dst.G) * (1 - sa))),
				B: uint8(math.Round(float64(dst.B) * (1 - sa))),
				A: dst.A,
			})
		}
	}
	draw.Draw(base, bounds, fg, bounds.Min, draw.Over)

	cropped := image.NewNRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	draw.Draw(cropped, cropped.Rect, base, bounds.Min, draw.Src)
	var c composition
	c.place(cropped, 0, 0, 180)
	return c.render(300, 200, 10)
}

func tintAlpha(src *image.NRGBA, col color.NRGBA) *image.NRGBA {
	out := image.NewNRGBA(src.Rect)
	for y := src.Rect.Min.Y; y < src.Rect.Max.Y; y++ {
		for x := src.Rect.Min.X; x < src.Rect.Max.X; x++ {
			a := src.NRGBAAt(x, y).A
			if a == 0 {
				continue
			}
			out.SetNRGBA(x, y, color.NRGBA{col.R, col.G, col.B, uint8(uint16(a) * uint16(col.A) / 255)})
		}
	}
	return out
}

func withDarkHalo(src *image.NRGBA, radius int, strength float64) *image.NRGBA {
	pad := radius * 2
	w, h := src.Rect.Dx()+2*pad, src.Rect.Dy()+2*pad
	alpha := make([]float64, w*h)
	tmp := make([]float64, w*h)
	for y := 0; y < src.Rect.Dy(); y++ {
		for x := 0; x < src.Rect.Dx(); x++ {
			alpha[(y+pad)*w+(x+pad)] = float64(src.NRGBAAt(src.Rect.Min.X+x, src.Rect.Min.Y+y).A) / 255
		}
	}
	for pass := 0; pass < 2; pass++ {
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				sum, cnt := 0.0, 0
				for dx := -radius; dx <= radius; dx++ {
					xx := max(0, min(w-1, x+dx))
					sum += alpha[y*w+xx]
					cnt++
				}
				tmp[y*w+x] = sum / float64(cnt)
			}
		}
		for x := 0; x < w; x++ {
			for y := 0; y < h; y++ {
				sum, cnt := 0.0, 0
				for dy := -radius; dy <= radius; dy++ {
					yy := max(0, min(h-1, y+dy))
					sum += tmp[yy*w+x]
					cnt++
				}
				alpha[y*w+x] = sum / float64(cnt)
			}
		}
	}
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			sa := min(1.0, alpha[y*w+x]*1.6) * strength
			if sa > 0.004 {
				out.SetNRGBA(x, y, color.NRGBA{4, 6, 14, uint8(math.Round(sa * 255))})
			}
		}
	}
	draw.Draw(out, image.Rect(pad, pad, pad+src.Rect.Dx(), pad+src.Rect.Dy()), src, src.Rect.Min, draw.Over)
	return out
}

func TestShowcaseBugMarksEmbeddedAndCached(t *testing.T) {
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	svc, err := New(Options{DB: db, Dir: t.TempDir(), Showcase: true})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	handler := svc.Handler()

	for _, v := range showcaseLooks() {
		src := showcaseBugSrc(v.id)
		if src == "" {
			t.Fatalf("%s: empty showcaseBugSrc", v.id)
		}
		im1 := showcaseBugImage(src)
		im2 := showcaseBugImage(src)
		if im1 == nil || im1 != im2 {
			t.Fatalf("%s: expected non-nil pointer-identical cached image", v.id)
		}
		sil1 := showcaseBugSilhouette(src)
		sil2 := showcaseBugSilhouette(src)
		if sil1 == nil || sil1 != sil2 {
			t.Fatalf("%s: expected non-nil pointer-identical cached silhouette", v.id)
		}
		if b := im1.Bounds(); b.Dx() != 300 || b.Dy() != 200 {
			t.Fatalf("%s: unexpected bug mark dimensions %dx%d", v.id, b.Dx(), b.Dy())
		}
		// Corners must be transparent and interior must have non-transparent ink.
		if _, _, _, a := im1.At(0, 0).RGBA(); a != 0 {
			t.Fatalf("%s: top-left corner is not transparent (alpha=%d)", v.id, a)
		}
		hasInk := false
		for y := 0; y < 200 && !hasInk; y++ {
			for x := 0; x < 300; x++ {
				if _, _, _, a := im1.At(x, y).RGBA(); a > 0x8000 {
					hasInk = true
					break
				}
			}
		}
		if !hasInk {
			t.Fatalf("%s: bug mark has no visible ink", v.id)
		}

		// Studio HTTP route serves the exact embedded PNG bytes.
		file := showcaseBugNames[v.id]
		req := httptest.NewRequest(http.MethodGet, "/showcase-bugs/"+file, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: GET /showcase-bugs/%s status=%d", v.id, file, rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "image/png" {
			t.Fatalf("%s: unexpected Content-Type %q", v.id, ct)
		}
		raw, err := showcaseBugFiles.ReadFile("showcasebugs/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(rec.Body.Bytes(), raw) {
			t.Fatalf("%s: served bytes differ from embedded PNG", v.id)
		}
	}

	for _, bad := range []string{"missing.png", "chroma-80.txt"} {
		req := httptest.NewRequest(http.MethodGet, "/showcase-bugs/"+bad, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET /showcase-bugs/%s expected 404, got %d", bad, rec.Code)
		}
	}
}

func TestShowcaseLookBugOverlayBindsAssetAndPreservesFades(t *testing.T) {
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	artworkCalls := 0
	customLogo := image.NewNRGBA(image.Rect(0, 0, 120, 80))
	for y := 10; y < 70; y++ {
		for x := 10; x < 110; x++ {
			customLogo.SetNRGBA(x, y, color.NRGBA{255, 255, 255, 255})
		}
	}

	svc, err := New(Options{
		DB:       db,
		Dir:      t.TempDir(),
		Showcase: true,
		Artwork: func(_ context.Context, ref string) (image.Image, error) {
			artworkCalls++
			if ref == "owner-channel-logo" {
				return customLogo, nil
			}
			return nil, fmt.Errorf("unexpected artwork lookup %q", ref)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()

	for _, v := range showcaseLooks() {
		look, err := svc.Look(ctx, v.id)
		if err != nil {
			t.Fatalf("%s: Look failed: %v", v.id, err)
		}
		if look.Bug == nil || look.Bug.Source != "logo" || look.Bug.Style != "original" {
			t.Fatalf("%s: unexpected look.Bug=%+v", v.id, look.Bug)
		}

		// Saving an edited showcase look in Studio must pass scene asset validation
		// without needing rows in onair_assets.
		edited := look
		edited.Brand.Tagline = look.Brand.Tagline + "!"
		if _, err := svc.SaveLook(ctx, edited); err != nil {
			t.Fatalf("%s: SaveLook rejected showcase bug asset: %v", v.id, err)
		}
		// Restore original for remaining checks.
		if _, err := svc.SaveLook(ctx, look); err != nil {
			t.Fatalf("%s: restore SaveLook failed: %v", v.id, err)
		}

		src := showcaseBugSrc(v.id)
		req := core.ClipRequest{
			LookID:    v.id,
			ChannelID: v.id,
			Moment:    "bug",
			Bind:      map[string]string{"channel.name": v.name},
		}
		doc1, bind1, ok := svc.Overlay(ctx, req)
		if !ok {
			t.Fatalf("%s: Overlay returned ok=false", v.id)
		}
		_, bind2, ok := svc.Overlay(ctx, req)
		if !ok {
			t.Fatalf("%s: second Overlay returned ok=false", v.id)
		}
		if artworkCalls != 0 {
			t.Fatalf("%s: expected 0 external Artwork calls for embedded bug, got %d", v.id, artworkCalls)
		}
		if bind1.Assets[src] == nil || bind1.Assets[src] != showcaseBugImage(src) || bind1.Assets[src] != bind2.Assets[src] {
			t.Fatalf("%s: Overlay did not bind the once-decoded showcaseBugImage", v.id)
		}
		if bind1.Tokens["bug.fadeBreaks"] != true || bind1.Tokens["bug.fadeCredits"] != false {
			t.Fatalf("%s: default fade tokens changed: fadeBreaks=%v fadeCredits=%v", v.id, bind1.Tokens["bug.fadeBreaks"], bind1.Tokens["bug.fadeCredits"])
		}
		if len(doc1.Layers) != 1 || doc1.Layers[0].ID != "station-bug" || len(doc1.Layers[0].Layers) != 2 {
			t.Fatalf("%s: unexpected bug document layers: %+v", v.id, doc1.Layers)
		}
		logoLayer := doc1.Layers[0].Layers[1]
		if logoLayer.ID != "station-logo" || logoLayer.Type != "image" || logoLayer.Src != src {
			t.Fatalf("%s: expected station-logo image layer bound to %q, got %+v", v.id, src, logoLayer)
		}

		// Render at 1080p and verify compact ~120-160 px width in bottom-right corner.
		frame := raster.Render(doc1, bind1, doc1.Poster, 1920, 1080)
		bounds := ink(frame)
		if bounds.Dx() < 120 || bounds.Dx() > 160 {
			t.Fatalf("%s: 1080p bug width=%d px, want within 120..160 px (bounds=%v)", v.id, bounds.Dx(), bounds)
		}
		if bounds.Min.X < 1400 || bounds.Max.X > 1740 || bounds.Min.Y < 850 || bounds.Max.Y > 990 {
			t.Fatalf("%s: 1080p bug bounds %v outside bottom-right safe corner", v.id, bounds)
		}

		// Channel overrides for fadeBreaks, fadeCredits, and monochrome style must work.
		fb, fc := false, true
		overrideReq := req
		overrideReq.Bug = &core.BugSettings{Style: "monochrome", FadeBreaks: &fb, FadeCredits: &fc}
		_, monoBind, ok := svc.Overlay(ctx, overrideReq)
		if !ok {
			t.Fatalf("%s: monochrome override Overlay failed", v.id)
		}
		if monoBind.Assets[src] != showcaseBugSilhouette(src) {
			t.Fatalf("%s: monochrome style did not bind cached silhouette", v.id)
		}
		if monoBind.Tokens["bug.fadeBreaks"] != false || monoBind.Tokens["bug.fadeCredits"] != true {
			t.Fatalf("%s: channel fade overrides not preserved: %+v", v.id, monoBind.Tokens)
		}
	}

	// Explicit channel.logo artwork overrides the default showcase bug mark.
	overrideDoc, overrideBind, ok := svc.Overlay(ctx, core.ClipRequest{
		LookID: "showcase-80",
		Moment: "bug",
		Bind:   map[string]string{"channel.name": "CHROMA 80"},
		Art:    map[string]string{"channel.logo": "owner-channel-logo"},
	})
	if !ok {
		t.Fatal("channel.logo override Overlay failed")
	}
	if overrideBind.Assets["owner-channel-logo"] != customLogo {
		t.Fatal("expected custom channel.logo asset to be bound")
	}
	if got := overrideDoc.Layers[0].Layers[1].Src; got != "owner-channel-logo" {
		t.Fatalf("expected station-logo src=owner-channel-logo, got %q", got)
	}
}

func TestShowcaseLookUpgradePreservesOwnerEdits(t *testing.T) {
	ctx := t.Context()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	dir := t.TempDir()
	// Boot once without Showcase to initialize schema, then seed 0.3.6 v1 rows.
	initSvc, err := New(Options{DB: db, Dir: dir, Showcase: false})
	if err != nil {
		t.Fatal(err)
	}
	initSvc.Close()

	v1Look := func(v showcaseLook) Look {
		l := familyLook(v.family, v.name)
		l.ID = v.id
		l.Brand.Palette.Background = v.bg
		l.Brand.Palette.Accent = v.accent
		l.Brand.Palette.Text = v.text
		l.Brand.Palette.Glow = v.glow
		l.Brand.Type.Display = v.display
		l.Brand.Type.Body = v.body
		l.Brand.Tagline = v.tagline
		l.Brand.Energy = .5
		l.Brand.Language = "en"
		l.Brand.Textures = append([]string{}, v.textures...)
		opacity := .4
		l.Bug = &core.BugSettings{Source: "wordmark", Opacity: &opacity}
		return l
	}

	now := time.Now().Unix()
	for _, v := range showcaseLooks() {
		orig := v1Look(v)
		origBytes, err := json.Marshal(orig)
		if err != nil {
			t.Fatal(err)
		}
		origHash := digest(string(origBytes))
		if _, err := db.ExecContext(ctx, "INSERT INTO onair_settings(k,v) VALUES(?,?)", "showcase:"+v.id, "1:"+origHash); err != nil {
			t.Fatal(err)
		}
		switch v.id {
		case "showcase-80":
			// Untouched 0.3.6 v1 look -> must auto-upgrade to v2 logo bug.
			if _, err := db.ExecContext(ctx, "INSERT INTO onair_looks(id,data,updated_at) VALUES(?,?,?)", v.id, string(origBytes), now); err != nil {
				t.Fatal(err)
			}
		case "showcase-90":
			// Owner-edited 0.3.6 look -> must NOT be overwritten.
			edited := orig
			edited.Brand.Tagline = "My Custom 90s Tagline"
			editedBytes, err := json.Marshal(edited)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, "INSERT INTO onair_looks(id,data,updated_at) VALUES(?,?,?)", v.id, string(editedBytes), now); err != nil {
				t.Fatal(err)
			}
		case "showcase-00":
			// Owner-deleted 0.3.6 look -> no row in onair_looks; must stay deleted.
		case "showcase-10":
			// Untouched 0.3.6 v1 look -> must auto-upgrade to v2 logo bug.
			if _, err := db.ExecContext(ctx, "INSERT INTO onair_looks(id,data,updated_at) VALUES(?,?,?)", v.id, string(origBytes), now); err != nil {
				t.Fatal(err)
			}
		}
	}

	upgraded, err := New(Options{DB: db, Dir: dir, Showcase: true})
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()

	// Untouched v1 looks upgraded to v2 logo bugs.
	for _, id := range []string{"showcase-80", "showcase-10"} {
		l, err := upgraded.Look(ctx, id)
		if err != nil {
			t.Fatalf("%s: Look failed after upgrade: %v", id, err)
		}
		if l.Bug == nil || l.Bug.Source != "logo" {
			t.Fatalf("%s: expected untouched v1 look to upgrade to Source=logo, got %+v", id, l.Bug)
		}
		doc, bind, ok := upgraded.Overlay(ctx, core.ClipRequest{LookID: id, Moment: "bug", Bind: map[string]string{"channel.name": l.Name}})
		if !ok || bind.Assets[showcaseBugSrc(id)] == nil || doc.Layers[0].Layers[1].ID != "station-logo" {
			t.Fatalf("%s: upgraded look did not bind showcase bug image", id)
		}
	}

	// Owner-edited v1 look preserved untouched.
	edited90, err := upgraded.Look(ctx, "showcase-90")
	if err != nil {
		t.Fatalf("showcase-90: Look failed: %v", err)
	}
	if edited90.Brand.Tagline != "My Custom 90s Tagline" {
		t.Fatalf("showcase-90: owner tagline overwritten: %q", edited90.Brand.Tagline)
	}
	if edited90.Bug == nil || edited90.Bug.Source != "wordmark" {
		t.Fatalf("showcase-90: owner bug source overwritten: %+v", edited90.Bug)
	}
	doc90, _, ok := upgraded.Overlay(ctx, core.ClipRequest{LookID: "showcase-90", Moment: "bug", Bind: map[string]string{"channel.name": "RIOT 90"}})
	if !ok || doc90.Layers[0].Layers[1].Type != "text" {
		t.Fatalf("showcase-90: expected wordmark text layer for preserved owner edit, got %+v", doc90.Layers)
	}

	// Owner-deleted v1 look stays deleted.
	if _, err := upgraded.Look(ctx, "showcase-00"); err == nil {
		t.Fatal("showcase-00: expected deleted look to remain deleted after upgrade")
	}
}
