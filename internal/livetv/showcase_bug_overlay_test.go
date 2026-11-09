package livetv

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

func TestShowcaseChannelsBugOverlayAndFades(t *testing.T) {
	ctx := t.Context()
	db := store.OpenTest(t)
	gfx, err := onair.New(onair.Options{DB: db, Dir: t.TempDir(), Showcase: true})
	if err != nil {
		t.Fatal(err)
	}
	defer gfx.Close()

	now := time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)
	svc := &Service{
		o:        Options{DB: db, Graphics: gfx, Now: func() time.Time { return now }},
		sessions: map[string]*session{},
	}
	format := transcode.RawFormat{Width: 1920, Height: 1080}

	recipes := showcaseRecipes()
	if len(recipes) != 4 {
		t.Fatalf("expected 4 showcase recipes, got %d", len(recipes))
	}

	prog1 := Programme{
		ID:        "prog-1",
		Start:     now,
		End:       now.Add(30 * time.Minute),
		Item:      Item{Key: 101, Title: "Night Flight", DurationMS: int64(30 * time.Minute / time.Millisecond)},
		ChannelID: "showcase",
	}
	prog2 := Programme{
		ID:        "prog-2",
		Start:     now.Add(30 * time.Minute),
		End:       now.Add(60 * time.Minute),
		Item:      Item{Key: 102, Title: "Late Feature", DurationMS: int64(30 * time.Minute / time.Millisecond)},
		ChannelID: "showcase",
	}
	chapters := []core.Chapter{{Title: "End Credits", Start: 28 * time.Minute, End: 30 * time.Minute}}

	for _, r := range recipes {
		ch := r.channel("showcase-"+r.id, r.number, "UTC", now)
		planner := &scheduleBroadcastPlanner{
			s:       svc,
			channel: ch,
			user:    core.User{ID: 1, IsOwner: true},
			format:  format,
		}
		ov1 := planner.prepareProgrammeOverlays(ctx, prog1, chapters)
		if ov1 == nil || ov1.bug == nil {
			t.Fatalf("%s: expected non-nil bug videoOverlay", r.id)
		}
		if w := ov1.bug.rect.Dx(); w < 120 || w > 160 {
			t.Fatalf("%s: 1080p compact bug width=%d px, want 120..160 px (rect=%v)", r.id, w, ov1.bug.rect)
		}
		if !ov1.fadeBreaks || ov1.fadeCredits {
			t.Fatalf("%s: unexpected default fades fadeBreaks=%v fadeCredits=%v", r.id, ov1.fadeBreaks, ov1.fadeCredits)
		}

		// Second programme reuses the prepared compact bug overlay without re-decoding or re-rendering.
		ov2 := planner.prepareProgrammeOverlays(ctx, prog2, nil)
		if ov2 == nil || ov2.bug != ov1.bug {
			t.Fatalf("%s: expected planner to reuse cached overlayBug across programmes", r.id)
		}

		// Verify break fade behaviour inside the bug region: 0 at exact boundary,
		// partial at 250ms, steady at 10s.
		base := make([]byte, format.Width*format.Height*3/2)
		for i := 0; i < format.Width*format.Height; i++ {
			base[i] = 48
		}
		for i := format.Width * format.Height; i < len(base); i++ {
			base[i] = 128
		}

		atBoundary := append([]byte(nil), base...)
		ov1.blend(atBoundary, format, prog1.Start, prog1.Start, prog1.End)
		if frameLumaRectDiff(base, atBoundary, format, ov1.bug.rect) != 0 {
			t.Fatalf("%s: expected bug to be faded to 0 at programme start boundary", r.id)
		}

		atEndBoundary := append([]byte(nil), base...)
		ov1.blend(atEndBoundary, format, prog1.End, prog1.Start, prog1.End)
		if frameLumaRectDiff(base, atEndBoundary, format, ov1.bug.rect) != 0 {
			t.Fatalf("%s: expected bug to be faded to 0 at programme end boundary", r.id)
		}

		atMidRamp := append([]byte(nil), base...)
		ov1.blend(atMidRamp, format, prog1.End.Add(-250*time.Millisecond), prog1.Start, prog1.End)

		atSteady := append([]byte(nil), base...)
		ov1.blend(atSteady, format, prog1.Start.Add(10*time.Second), prog1.Start, prog1.End)

		rampDiff := frameLumaRectDiff(base, atMidRamp, format, ov1.bug.rect)
		steadyDiff := frameLumaRectDiff(base, atSteady, format, ov1.bug.rect)
		if rampDiff == 0 || steadyDiff <= rampDiff {
			t.Fatalf("%s: expected smoothstep ramp 0 < rampDiff(%d) < steadyDiff(%d)", r.id, rampDiff, steadyDiff)
		}

		// With default fadeCredits=false, bug stays visible during credits.
		inCreditsDefault := append([]byte(nil), base...)
		ov1.blend(inCreditsDefault, format, prog1.Start.Add(29*time.Minute), prog1.Start, prog1.End)
		if frameLumaRectDiff(base, inCreditsDefault, format, ov1.bug.rect) != steadyDiff {
			t.Fatalf("%s: expected bug unchanged during credits when fadeCredits=false", r.id)
		}

		// With fadeCredits=true override on the channel, bug fades out during credits.
		fc := true
		chCredits := ch
		lookCopy := *ch.Look
		lookCopy.Bug = &core.BugSettings{FadeCredits: &fc}
		chCredits.Look = &lookCopy
		creditPlanner := &scheduleBroadcastPlanner{
			s:       svc,
			channel: chCredits,
			user:    core.User{ID: 1, IsOwner: true},
			format:  format,
		}
		ovCredits := creditPlanner.prepareProgrammeOverlays(ctx, prog1, chapters)
		if ovCredits == nil || !ovCredits.fadeCredits {
			t.Fatalf("%s: expected fadeCredits=true on channel override", r.id)
		}
		inCreditsFaded := append([]byte(nil), base...)
		ovCredits.blend(inCreditsFaded, format, prog1.Start.Add(29*time.Minute), prog1.Start, prog1.End)
		if frameLumaRectDiff(base, inCreditsFaded, format, ovCredits.bug.rect) != 0 {
			t.Fatalf("%s: expected bug to fade completely out during credits when fadeCredits=true", r.id)
		}
	}
}

func frameLumaRectDiff(a, b []byte, f transcode.RawFormat, r image.Rectangle) int64 {
	var sum int64
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := y * f.Width
		for x := r.Min.X; x < r.Max.X; x++ {
			d := int64(a[row+x]) - int64(b[row+x])
			if d < 0 {
				d = -d
			}
			sum += d
		}
	}
	return sum
}

// TestRenderShowcaseBugReviewFrames renders still frames of each shipped decade
// channel's corner bug over both a dark and a bright 1080p YUV420p test frame
// through the production prepareProgrammeOverlays + videoOverlay.blend path:
//
//	ETHER_RENDER_SHOWCASE_BUG_SAMPLES=/path/to/out go test ./internal/livetv -run '^TestRenderShowcaseBugReviewFrames$'
func TestRenderShowcaseBugReviewFrames(t *testing.T) {
	dir := os.Getenv("ETHER_RENDER_SHOWCASE_BUG_SAMPLES")
	if dir == "" {
		t.Skip("set ETHER_RENDER_SHOWCASE_BUG_SAMPLES to an output directory to render review stills")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	db := store.OpenTest(t)
	gfx, err := onair.New(onair.Options{DB: db, Dir: t.TempDir(), Showcase: true})
	if err != nil {
		t.Fatal(err)
	}
	defer gfx.Close()

	now := time.Date(2026, 11, 9, 20, 0, 0, 0, time.UTC)
	svc := &Service{
		o:        Options{DB: db, Graphics: gfx, Now: func() time.Time { return now }},
		sessions: map[string]*session{},
	}
	format := transcode.RawFormat{Width: 1920, Height: 1080}

	darkYUV := sampleYUV(makeShowcaseTestScene(format.Width, format.Height, false), format)
	brightYUV := sampleYUV(makeShowcaseTestScene(format.Width, format.Height, true), format)

	prog := Programme{
		ID:    "review-prog",
		Start: now,
		End:   now.Add(30 * time.Minute),
		Item:  Item{Key: 1, Title: "Evening Showcase", DurationMS: int64(30 * time.Minute / time.Millisecond)},
	}

	for _, r := range showcaseRecipes() {
		ch := r.channel("showcase-"+r.id, r.number, "UTC", now)
		planner := &scheduleBroadcastPlanner{
			s:       svc,
			channel: ch,
			user:    core.User{ID: 1, IsOwner: true},
			format:  format,
		}
		ov := planner.prepareProgrammeOverlays(ctx, prog, nil)
		if ov == nil || ov.bug == nil {
			t.Fatalf("%s: nil bug overlay", r.id)
		}

		darkOut := append([]byte(nil), darkYUV...)
		ov.bug.blend(darkOut, format, 255)
		darkImg := yuv420ToNRGBA(darkOut, format)
		addCornerMagnifierInset(darkImg, ov.bug.rect)

		brightOut := append([]byte(nil), brightYUV...)
		ov.bug.blend(brightOut, format, 255)
		brightImg := yuv420ToNRGBA(brightOut, format)
		addCornerMagnifierInset(brightImg, ov.bug.rect)

		sep := 8
		sheet := image.NewNRGBA(image.Rect(0, 0, format.Width, format.Height*2+sep))
		draw.Draw(sheet, image.Rect(0, 0, format.Width, format.Height), darkImg, image.Point{}, draw.Src)
		draw.Draw(sheet, image.Rect(0, format.Height, format.Width, format.Height+sep), &image.Uniform{C: color.NRGBA{28, 32, 42, 255}}, image.Point{}, draw.Src)
		draw.Draw(sheet, image.Rect(0, format.Height+sep, format.Width, format.Height*2+sep), brightImg, image.Point{}, draw.Src)

		path := filepath.Join(dir, r.id+"-bug-review.png")
		var buf bytes.Buffer
		if err := (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buf, sheet); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("wrote %s (bug rect=%v, %dx%d px at 1080p)", path, ov.bug.rect, ov.bug.rect.Dx(), ov.bug.rect.Dy())
	}
}

func makeShowcaseTestScene(w, h int, bright bool) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		fy := float64(y) / float64(h-1)
		for x := 0; x < w; x++ {
			fx := float64(x) / float64(w-1)
			// Smooth diagonal light shafts + horizon gradient across the frame so
			// both edge legibility and broadcast translucency read naturally.
			wave := 0.5 + 0.5*math.Sin(fx*22.0-fy*14.0)*math.Cos(fx*9.0+fy*15.0)
			shaft := 0.5 + 0.5*math.Sin((fx*1.7+fy*0.9)*34.0)
			if bright {
				r := clampByte(220 + 26*(1-fy*0.30) + 9*(wave-0.5) + 7*(shaft-0.5))
				g := clampByte(226 + 22*(1-fy*0.26) + 9*(wave-0.5) + 6*(shaft-0.5))
				b := clampByte(235 + 16*(1-fx*0.18) + 7*(wave-0.5) + 5*(shaft-0.5))
				img.SetNRGBA(x, y, color.NRGBA{r, g, b, 255})
			} else {
				r := clampByte(14 + 20*fy + 12*wave + 8*shaft)
				g := clampByte(18 + 22*fy + 11*wave + 7*shaft)
				b := clampByte(28 + 26*fx + 14*wave + 9*shaft)
				img.SetNRGBA(x, y, color.NRGBA{r, g, b, 255})
			}
		}
	}
	return img
}

func clampByte(v float64) uint8 {
	return uint8(max(0, min(255, math.Round(v))))
}

func yuv420ToNRGBA(raw []byte, f transcode.RawFormat) *image.NRGBA {
	ycbcr := image.NewYCbCr(image.Rect(0, 0, f.Width, f.Height), image.YCbCrSubsampleRatio420)
	n := f.Width * f.Height
	copy(ycbcr.Y, raw[:n])
	copy(ycbcr.Cb, raw[n:n+n/4])
	copy(ycbcr.Cr, raw[n+n/4:])
	out := image.NewNRGBA(ycbcr.Rect)
	draw.Draw(out, out.Rect, ycbcr, image.Point{}, draw.Src)
	return out
}

// addCornerMagnifierInset places a crisp 3x pixel-accurate zoom of the bottom-right
// bug region into the top-left corner of the 1080p review frame so the owner can
// inspect both true 1:1 1080p scale/placement and close-up pixel edges at a glance.
func addCornerMagnifierInset(frame *image.NRGBA, bugRect image.Rectangle) {
	crop := bugRect.Inset(-28).Intersect(frame.Rect)
	const zoom = 3
	dstW, dstH := crop.Dx()*zoom, crop.Dy()*zoom
	ox, oy := 48, 48

	border := image.Rect(ox-3, oy-3, ox+dstW+3, oy+dstH+3)
	draw.Draw(frame, border, &image.Uniform{C: color.NRGBA{120, 130, 145, 255}}, image.Point{}, draw.Src)

	for dy := 0; dy < dstH; dy++ {
		sy := crop.Min.Y + dy/zoom
		for dx := 0; dx < dstW; dx++ {
			sx := crop.Min.X + dx/zoom
			frame.SetNRGBA(ox+dx, oy+dy, frame.NRGBAAt(sx, sy))
		}
	}
}
