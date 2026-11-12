package onair

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"github.com/braedonsaunders/ether/internal/store"
)

type syntheticClipReader struct {
	width  int
	height int
	total  int
	tick   int
	loop   bool
	luma   byte
}

func (r *syntheticClipReader) ReadFrame(video []byte) ([]byte, error) {
	if !r.loop && r.tick >= r.total {
		return nil, io.EOF
	}
	yLen := r.width * r.height
	val := r.luma + byte(r.tick%32)
	for i := 0; i < yLen; i++ {
		video[i] = val
	}
	for i := yLen; i < len(video); i++ {
		video[i] = 128
	}
	r.tick++
	return make([]byte, 1600*4), nil
}

func (r *syntheticClipReader) Close() error { return nil }

func countMP4Files(t *testing.T, dir string) int {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".mp4") {
			n++
		}
	}
	return n
}

func cloneSeededLook(t *testing.T, svc *Service, newID, newName string) Look {
	t.Helper()
	l, err := svc.Look(context.Background(), "nocturne")
	if err != nil {
		t.Fatal(err)
	}
	l.ID = newID
	l.Name = newName
	return l
}

func TestZeroRendersOnStartupShowcaseAndLookSaveWithoutViewers(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	dir := t.TempDir()
	var renderCalls atomic.Int64
	var frameCalls atomic.Int64

	svc, err := New(Options{
		DB:       db,
		Dir:      dir,
		Showcase: true,
		Upcoming: func(context.Context) []core.ClipRequest { return nil },
		Render: func(_ context.Context, _ scene.Document, _ raster.Bindings, out string) error {
			renderCalls.Add(1)
			return os.WriteFile(out, []byte("mp4"), 0600)
		},
		RenderFrame: func(doc scene.Document, b raster.Bindings, ts float64, w, h int) *image.RGBA {
			frameCalls.Add(1)
			return raster.Render(doc, b, ts, w, h)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Start()
	defer svc.Close()

	look, err := svc.Look(ctx, "nocturne")
	if err != nil {
		t.Fatal(err)
	}
	look.Brand.Tagline = "Late-night movies"
	if _, err := svc.SaveLook(ctx, look); err != nil {
		t.Fatal(err)
	}

	svc.WakeUpcoming()
	time.Sleep(120 * time.Millisecond)

	if got := renderCalls.Load(); got != 0 {
		t.Fatalf("expected 0 background master renders with no viewers, got %d", got)
	}
	if got := frameCalls.Load(); got != 0 {
		t.Fatalf("expected 0 live frame renders with no viewers, got %d", got)
	}
	if got := svc.Visibility().MastersRendered; got != 0 {
		t.Fatalf("expected Visibility().MastersRendered == 0, got %d", got)
	}
	if mp4s := countMP4Files(t, dir); mp4s != 0 {
		t.Fatalf("expected 0 .mp4 files on disk with no viewers, got %d", mp4s)
	}
}

func TestColdPlateRealFFmpegAndRestartDurability(t *testing.T) {
	ffPath, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available in PATH")
	}
	// This test covers save-while-airing and restart durability, not machine
	// speed: never let a loaded runner trip the real-time gate.
	defer func(g, f float64) { liveSpeedGrace, liveSpeedFloor = g, f }(liveSpeedGrace, liveSpeedFloor)
	liveSpeedFloor = 0

	ctx := context.Background()
	dbDir := t.TempDir()
	cacheDir := t.TempDir()
	db, err := store.Open(ctx, dbDir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	var frameRenders atomic.Int64

	svc, err := New(Options{
		DB:     db,
		Dir:    cacheDir,
		FFmpeg: func() string { return ffPath },
		Logger: logger,
		RenderFrame: func(_ scene.Document, _ raster.Bindings, ts float64, w, h int) *image.RGBA {
			frameRenders.Add(1)
			im := image.NewRGBA(image.Rect(0, 0, w, h))
			shade := uint8(40 + int(ts*30)%180)
			// Paint a moving horizontal band so frame generation is fast and visibly animated.
			bandY := int(ts*40) % max(1, h-16)
			for y := bandY; y < min(h, bandY+16); y++ {
				row := y * im.Stride
				for x := 0; x < w*4; x += 4 {
					im.Pix[row+x] = shade
					im.Pix[row+x+1] = 120
					im.Pix[row+x+2] = 220
					im.Pix[row+x+3] = 255
				}
			}
			return im
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Start()

	// Standard 8-second look role (240 frames @ 30fps, exact multiple of 120 keyframe cadence).
	custom := cloneSeededLook(t, svc, "test-live-save", "Test Live Save")
	custom.Scenes = []StyleScene{
		{
			ID:      "back-main",
			Name:    "Back",
			Role:    "back",
			Enabled: true,
			Weight:  1,
			Document: scene.Document{
				V:        1,
				Moment:   "back",
				Duration: 8,
				Layers: []scene.Layer{
					{ID: "bg", Type: "rect", X: 0, Y: 0, W: 1920, H: 1080, Fill: "#122038"},
				},
			},
		},
	}
	if _, err := svc.SaveLook(ctx, custom); err != nil {
		t.Fatal(err)
	}

	var openedCachedPaths []string
	openClip := func(_ context.Context, path string, loop bool) (FrameReader, error) {
		if st, err := os.Stat(path); err != nil || st.Size() == 0 {
			return nil, fmt.Errorf("missing or empty master at %s: %w", path, err)
		}
		openedCachedPaths = append(openedCachedPaths, path)
		return &syntheticClipReader{width: 320, height: 180, total: 240, loop: loop, luma: 90}, nil
	}

	reqCh1 := core.ClipRequest{ChannelID: "ch-1", LookID: custom.ID, Moment: "back", Bind: map[string]string{"channel.name": "CHROMA 80"}}
	ms, mode, err := svc.OpenMoment(ctx, reqCh1, 320, 180, 30, 1, false, openClip)
	if err != nil {
		t.Fatalf("cold OpenMoment failed: %v", err)
	}
	if mode != "static-plate" {
		t.Fatalf("expected cold static plate, got %q", mode)
	}

	frameBuf := make([]byte, 320*180*3/2)
	var snap0, snap90 []byte
	framesRead := 0
	for {
		audio, rErr := ms.ReadFrame(frameBuf)
		if errors.Is(rErr, io.EOF) {
			break
		}
		if rErr != nil {
			t.Fatalf("ReadFrame error at tick %d: %v", framesRead, rErr)
		}
		if len(audio) == 0 {
			t.Fatalf("expected non-empty PCM audio tick at %d", framesRead)
		}
		if framesRead == 0 {
			snap0 = append([]byte(nil), frameBuf[:320*180]...)
		}
		if framesRead == 90 {
			snap90 = append([]byte(nil), frameBuf[:320*180]...)
		}
		framesRead++
	}
	_ = ms.Close()

	if framesRead != 240 {
		t.Fatalf("expected 240 plate frames for 8s@30fps moment, got %d", framesRead)
	}
	if !bytes.Equal(snap0, snap90) {
		t.Fatalf("expected cold plate to remain stable until the next boundary")
	}
	waitMomentMaster(t, svc, reqCh1)
	if speed := svc.Visibility().LastSpeedX; speed <= 0 {
		t.Fatalf("expected measured master render speed, got %.2fx", speed)
	}
	if mp4s := countMP4Files(t, cacheDir); mp4s != 1 {
		t.Fatalf("expected exactly 1 durable .mp4 master on disk, got %d", mp4s)
	}

	rendersAfterFirst := frameRenders.Load()

	// Second airing on ANOTHER channel reuses the exact same look master without rendering!
	reqCh2 := core.ClipRequest{ChannelID: "ch-2", LookID: custom.ID, Moment: "back", Bind: map[string]string{"channel.name": "RETRO 90"}}
	ms2, mode2, err := svc.OpenMoment(ctx, reqCh2, 320, 180, 30, 1, false, openClip)
	if err != nil {
		t.Fatalf("second OpenMoment failed: %v", err)
	}
	_ = ms2.Close()
	if mode2 != "cached" {
		t.Fatalf("expected second airing across channels to hit 'cached', got %q", mode2)
	}
	if frameRenders.Load() != rendersAfterFirst {
		t.Fatalf("expected zero frame renders on cached airing, before=%d after=%d", rendersAfterFirst, frameRenders.Load())
	}
	if len(openedCachedPaths) != 1 {
		t.Fatalf("expected openClip called once with cached master, got %d", len(openedCachedPaths))
	}

	// Verify visibility log recorded render_speed_x.
	if !strings.Contains(logBuf.String(), `"render_speed_x"`) {
		t.Fatalf("expected visibility log to include render_speed_x, got logs:\n%s", logBuf.String())
	}

	// Simulate full server restart on the same DB + cache directory.
	svc.Close()
	svcRestarted, err := New(Options{
		DB:     db,
		Dir:    cacheDir,
		FFmpeg: func() string { return ffPath },
		RenderFrame: func(scene.Document, raster.Bindings, float64, int, int) *image.RGBA {
			t.Fatalf("unexpected RenderFrame after restart — durable cache should hit!")
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer svcRestarted.Close()

	if ref, ok := svcRestarted.Clip(ctx, reqCh2); !ok || ref.Path == "" {
		t.Fatalf("expected durable master cache to survive restart, got ok=%v ref=%+v", ok, ref)
	}
	ms3, mode3, err := svcRestarted.OpenMoment(ctx, reqCh2, 320, 180, 30, 1, false, openClip)
	if err != nil {
		t.Fatalf("restarted OpenMoment failed: %v", err)
	}
	_ = ms3.Close()
	if mode3 != "cached" {
		t.Fatalf("expected 'cached' mode after restart, got %q", mode3)
	}
}

func TestLiveRenderSpeedGateFallbackAndVisibilityLog(t *testing.T) {
	// This test is about the gate itself: judge after a short window.
	defer func(g, f float64) { liveSpeedGrace, liveSpeedFloor = g, f }(liveSpeedGrace, liveSpeedFloor)
	liveSpeedGrace = 0.2
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelInfo}))

	svc, err := New(Options{
		DB:     db,
		Dir:    t.TempDir(),
		Logger: logger,
		// 180ms per frame across 2 workers => ~11 fps (~0.37x realtime at 30fps), well below 1.0x.
		RenderFrame: func(_ scene.Document, _ raster.Bindings, _ float64, w, h int) *image.RGBA {
			time.Sleep(180 * time.Millisecond)
			return image.NewRGBA(image.Rect(0, 0, w, h))
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Start()
	defer svc.Close()

	look := cloneSeededLook(t, svc, "slow-unsplittable", "Slow")
	look.Scenes = []StyleScene{{ID: "slow", Name: "Slow", Role: "upnext", Enabled: true, Weight: 1, Document: scene.Document{V: 1, Moment: "upnext", Duration: 8, Layers: []scene.Layer{
		{ID: "slot", Type: "text", Text: "{next.title}", X: 100, Y: 300, Size: 90, Fill: "#ffffff"},
		{ID: "fx", Type: "noise", Opacity: 0.25, Blend: "multiply"},
	}}}}
	if _, err := svc.SaveLook(ctx, look); err != nil {
		t.Fatal(err)
	}
	req := core.ClipRequest{ChannelID: "ch-slow", LookID: look.ID, Moment: "upnext", Bind: map[string]string{"next.title": "Slow"}}
	ms, mode, err := svc.OpenMoment(ctx, req, 160, 90, 30, 1, false, nil)
	if err != nil {
		t.Fatalf("initial OpenMoment failed: %v", err)
	}
	if mode != "live-nosave" {
		t.Fatalf("expected unsplittable live attempt, got %q", mode)
	}

	buf := make([]byte, 160*90*3/2)
	var gotErr error
	for range 20 {
		_, rErr := ms.ReadFrame(buf)
		if rErr != nil {
			gotErr = rErr
			break
		}
	}
	_ = ms.Close()

	if !errors.Is(gotErr, ErrLiveBelowRealtime) {
		t.Fatalf("expected ErrLiveBelowRealtime when render speed < 1.0x, got %v", gotErr)
	}

	allowed, speedX, reason := svc.LiveRenderAllowed(req)
	if allowed {
		t.Fatalf("expected LiveRenderAllowed=false after slow render (speed %.2fx)", speedX)
	}
	if speedX <= 0 || speedX >= 1.0 {
		t.Fatalf("expected measured speedX in (0, 1.0), got %.2f", speedX)
	}
	if !strings.Contains(reason, "< 1.00x realtime") {
		t.Fatalf("expected clear fallback reason, got %q", reason)
	}

	// Next boundary immediately rejects live render and logs why.
	ms2, mode2, err2 := svc.OpenMoment(ctx, req, 160, 90, 30, 1, false, nil)
	if err2 != nil || mode2 != "static-plate" {
		t.Fatalf("expected visible plate after speed gate, mode=%q err=%v", mode2, err2)
	}
	_ = ms2.Close()

	vis := svc.Visibility()
	if vis.Fallbacks < 2 {
		t.Fatalf("expected >= 2 fallback records, got %+v", vis)
	}
	logs := logBuf.String()
	if !strings.Contains(logs, "render_speed_below_1x") || !strings.Contains(logs, "onair live moment fallback") {
		t.Fatalf("expected fallback reason and speed logged, got:\n%s", logs)
	}
}

func TestBoundedMasterCacheSplitSlotMomentsAndUnsplittableNoSave(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cacheDir := t.TempDir()
	svc, err := New(Options{
		DB:  db,
		Dir: cacheDir,
		Artwork: func(_ context.Context, ref string) (image.Image, error) {
			im := image.NewRGBA(image.Rect(0, 0, 64, 96))
			c := color.RGBA{R: 200, G: byte(len(ref)*17) & 0xff, B: 90, A: 255}
			for y := 0; y < 96; y++ {
				for x := 0; x < 64; x++ {
					im.SetRGBA(x, y, c)
				}
			}
			return im, nil
		},
		// Fast base-frame rasterizer for the 1920x1080 pass 0 save while exercising real raster.Render on slot overlays!
		RenderFrame: func(doc scene.Document, b raster.Bindings, ts float64, w, h int) *image.RGBA {
			if w <= 320 {
				return raster.Render(doc, b, ts, w, h)
			}
			return image.NewRGBA(image.Rect(0, 0, w, h))
		},
		Render: func(_ context.Context, _ scene.Document, _ raster.Bindings, out string) error {
			return os.WriteFile(out, []byte("synthetic-mp4-master"), 0600)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Start()
	defer svc.Close()

	// Build a look from real shipped Nocturne scenes (3 static + 2 splittable slot roles = 5 roles),
	// keeping each role's valid canonical duration (8s).
	boundedLook := cloneSeededLook(t, svc, "bounded-look", "Bounded Network")
	wantRoles := map[string]bool{"ident": true, "break": true, "back": true, "upnext": true, "tonight": true}
	var kept []StyleScene
	seenRoles := map[string]bool{}
	for _, sc := range boundedLook.Scenes {
		if wantRoles[sc.Role] && !seenRoles[sc.Role] {
			seenRoles[sc.Role] = true
			sc.Enabled = true
			sc.Weight = 1
			kept = append(kept, sc)
		}
	}
	if len(kept) != 5 {
		t.Fatalf("expected 5 roles from nocturne template, got %d", len(kept))
	}
	boundedLook.Scenes = kept
	if _, err := svc.SaveLook(ctx, boundedLook); err != nil {
		t.Fatal(err)
	}

	const w, h = 320, 180
	// Use 1 fps (8 frames per 8s airing) so 70+ airings run in < 200ms while testing real raster.Render overlays.
	openClip := func(_ context.Context, path string, loop bool) (FrameReader, error) {
		if _, err := os.Stat(path); err != nil {
			return nil, err
		}
		return &syntheticClipReader{width: w, height: h, total: 4, loop: loop, luma: 32}, nil
	}

	drainStream := func(ms *MomentStream) []byte {
		buf := make([]byte, w*h*3/2)
		var last []byte
		for {
			_, rErr := ms.ReadFrame(buf)
			if errors.Is(rErr, io.EOF) {
				break
			}
			if rErr != nil {
				t.Fatalf("ReadFrame failed: %v", rErr)
			}
			last = append(last[:0], buf...)
		}
		_ = ms.Close()
		return last
	}

	// Air all 3 static roles first across 2 different channels.
	for _, moment := range []string{"ident", "break", "back"} {
		for idx, ch := range []string{"ch-a", "ch-b"} {
			ms, mode, oErr := svc.OpenMoment(ctx, core.ClipRequest{
				ChannelID: ch,
				LookID:    boundedLook.ID,
				Moment:    moment,
				Bind:      map[string]string{"channel.name": "Chan " + ch},
			}, w, h, 1, 2, false, openClip)
			if oErr != nil {
				t.Fatalf("OpenMoment(%s, %s): %v", moment, ch, oErr)
			}
			drainStream(ms)
			waitMomentMaster(t, svc, core.ClipRequest{LookID: boundedLook.ID, Moment: moment})
			if moment == "ident" {
				if idx == 0 && mode != "static-plate" {
					t.Fatalf("expected first ident mode 'static-plate', got %q", mode)
				}
				if idx == 1 && mode != "split-cached" {
					t.Fatalf("expected second ident mode 'split-cached', got %q", mode)
				}
			} else {
				if idx == 0 && mode != "static-plate" {
					t.Fatalf("expected first %s mode static-plate, got %q", moment, mode)
				}
				if idx == 1 && mode != "cached" && mode != "split-cached" {
					t.Fatalf("expected second %s mode cached, got %q", moment, mode)
				}
			}
		}
	}

	// Now air 35 distinct programmes through both `upnext` and `tonight` (70 slot moment airings!).
	var frameAlpha, frameOmega []byte
	for i := 0; i < 35; i++ {
		title := fmt.Sprintf("DISTINCT PROGRAMME %02d — %s", i, strings.Repeat("W", (i%7)+1))
		timeStr := fmt.Sprintf("%02d:%02d", (8+i)%24, (i*15)%60)
		heading := "COMING UP"
		if i >= 18 {
			heading = "TONIGHT"
		}

		upReq := core.ClipRequest{
			ChannelID: "ch-a",
			LookID:    boundedLook.ID,
			Moment:    "upnext",
			Bind: map[string]string{
				"next.title": title,
				"next.time":  timeStr,
			},
			Art: map[string]string{
				"next.poster": fmt.Sprintf("poster-%02d", i),
			},
		}
		msUp, modeUp, oErr := svc.OpenMoment(ctx, upReq, w, h, 1, 2, false, openClip)
		if oErr != nil {
			t.Fatalf("upnext %d failed: %v", i, oErr)
		}
		f := drainStream(msUp)
		waitMomentMaster(t, svc, upReq)
		if i == 0 {
			if modeUp != "static-plate" {
				t.Fatalf("expected first upnext mode 'static-plate', got %q", modeUp)
			}
		} else {
			if modeUp != "split-cached" {
				t.Fatalf("expected upnext %d mode 'split-cached', got %q", i, modeUp)
			}
			if i == 1 {
				frameAlpha = f
			}
			if i == 34 {
				frameOmega = f
			}
		}

		tnReq := core.ClipRequest{
			ChannelID: "ch-a",
			LookID:    boundedLook.ID,
			Moment:    "tonight",
			Bind: map[string]string{
				"lineup.heading": heading,
				"list[0].time":   timeStr,
				"list[0].title":  title,
				"list[1].time":   "21:30",
				"list[1].title":  fmt.Sprintf("SECOND SHOW %02d", i),
				"list[2].time":   "22:00",
				"list[2].title":  fmt.Sprintf("LATE FEATURE %02d", i),
			},
		}
		msTn, modeTn, oErr := svc.OpenMoment(ctx, tnReq, w, h, 1, 2, false, openClip)
		if oErr != nil {
			t.Fatalf("tonight %d failed: %v", i, oErr)
		}
		drainStream(msTn)
		waitMomentMaster(t, svc, tnReq)
		if i == 0 && modeTn != "static-plate" {
			t.Fatalf("expected first tonight mode 'static-plate', got %q", modeTn)
		}
		if i > 0 && modeTn != "split-cached" {
			t.Fatalf("expected tonight %d mode 'split-cached', got %q", i, modeTn)
		}
	}

	if bytes.Equal(frameAlpha, frameOmega) {
		t.Fatalf("expected live-composited slot frames to differ across distinct programme titles/posters")
	}

	// BINDING ASSERTION: cache growth is strictly bounded by looks × moment roles (1 × 5 = 5),
	// completely independent of the 35+ programmes aired!
	const numLooks = 1
	numRoles := len(boundedLook.Scenes) // 5
	gotMP4s := countMP4Files(t, cacheDir)
	if gotMP4s != numLooks*numRoles {
		t.Fatalf("master cache grew beyond looks×roles: expected %d .mp4 files after 35 programmes, got %d", numLooks*numRoles, gotMP4s)
	}

	// Finally, verify an UNSPLITTABLE slot scene (procedural/non-normal layer interleaved ABOVE a live slot layer)
	// renders live in real time each airing with ZERO disk saves!
	unsplittableLook := cloneSeededLook(t, svc, "unsplittable-look", "Interleaved FX")
	unsplittableDoc := scene.Document{
		V:        1,
		Moment:   "upnext",
		Duration: 8,
		Layers: []scene.Layer{
			{ID: "bg", Type: "rect", X: 0, Y: 0, W: 1920, H: 1080, Fill: "#111111"},
			{ID: "slot", Type: "text", Text: "{next.title}", X: 100, Y: 300, Size: 90, Fill: "#ffffff", Font: "Inter"},
			{ID: "fx", Type: "noise", Opacity: 0.25, Blend: "multiply"},
		},
	}
	if _, _, ok := scene.SplitSlotLayers(unsplittableDoc); ok {
		t.Fatalf("expected interleaved noise-above-slot scene to be unsplittable")
	}
	unsplittableLook.Scenes = []StyleScene{
		{
			ID:       "upnext-interleaved",
			Name:     "Interleaved Up Next",
			Role:     "upnext",
			Enabled:  true,
			Weight:   1,
			Document: unsplittableDoc,
		},
	}
	if _, err := svc.SaveLook(ctx, unsplittableLook); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 5; i++ {
		ms, mode, oErr := svc.OpenMoment(ctx, core.ClipRequest{
			ChannelID: "ch-fx",
			LookID:    unsplittableLook.ID,
			Moment:    "upnext",
			Bind:      map[string]string{"next.title": fmt.Sprintf("Interleaved Show %d", i)},
		}, w, h, 1, 2, false, openClip)
		if oErr != nil {
			t.Fatalf("unsplittable OpenMoment %d: %v", i, oErr)
		}
		if mode != "live-nosave" {
			t.Fatalf("expected unsplittable slot scene mode 'live-nosave', got %q", mode)
		}
		drainStream(ms)
	}

	// Still strictly 5 .mp4 files — zero files written for the unsplittable slot scene!
	if gotAfter := countMP4Files(t, cacheDir); gotAfter != numLooks*numRoles {
		t.Fatalf("expected unsplittable live-nosave airings to write 0 files (stay at %d), got %d", numLooks*numRoles, gotAfter)
	}
}

func TestCacheEvictionBudgetRespectedByQueuedMaster(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cacheDir := t.TempDir()
	now := time.Date(2026, 11, 10, 21, 0, 0, 0, time.UTC)
	svc, err := New(Options{
		DB:         db,
		Dir:        cacheDir,
		CacheBytes: 1500, // room for at most 1 of our 1000-byte masters once unpinned
		Now:        func() time.Time { return now },
		RenderFrame: func(_ scene.Document, _ raster.Bindings, _ float64, w, h int) *image.RGBA {
			return image.NewRGBA(image.Rect(0, 0, w, h))
		},
		Render: func(_ context.Context, _ scene.Document, _ raster.Bindings, out string) error {
			return os.WriteFile(out, bytes.Repeat([]byte("M"), 1000), 0600)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.Start()
	defer svc.Close()

	look := cloneSeededLook(t, svc, "evict-look", "Evict")
	look.Scenes = []StyleScene{
		{
			ID: "s1", Name: "Ident", Role: "ident", Enabled: true, Weight: 1,
			Document: scene.Document{V: 1, Moment: "ident", Duration: 8, Layers: []scene.Layer{{ID: "bg1", Type: "rect", X: 0, Y: 0, W: 1920, H: 1080, Fill: "#112233"}}},
		},
		{
			ID: "s2", Name: "Back", Role: "back", Enabled: true, Weight: 1,
			Document: scene.Document{V: 1, Moment: "back", Duration: 8, Layers: []scene.Layer{{ID: "bg2", Type: "rect", X: 0, Y: 0, W: 1920, H: 1080, Fill: "#223344"}}},
		},
	}
	if _, err := svc.SaveLook(ctx, look); err != nil {
		t.Fatal(err)
	}

	openClip := func(_ context.Context, _ string, loop bool) (FrameReader, error) {
		return &syntheticClipReader{width: 160, height: 90, total: 4, loop: loop}, nil
	}

	ms1, _, err := svc.OpenMoment(ctx, core.ClipRequest{LookID: look.ID, Moment: "ident"}, 160, 90, 1, 2, false, openClip)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 160*90*3/2)
	for {
		if _, rErr := ms1.ReadFrame(buf); errors.Is(rErr, io.EOF) {
			break
		}
	}
	_ = ms1.Close()
	waitMomentMaster(t, svc, core.ClipRequest{LookID: look.ID, Moment: "ident"})

	// Expire the actual cache pin so the first master is eligible for LRU eviction.
	// Cache scheduling uses wall time, independently of the branding clock.
	svc.mu.Lock()
	for hash, entry := range svc.cache {
		entry.Pin = time.Now().Add(-time.Hour)
		svc.cache[hash] = entry
	}
	for hash := range svc.pins {
		svc.pins[hash] = time.Now().Add(-time.Hour)
	}
	svc.mu.Unlock()

	ms2, _, err := svc.OpenMoment(ctx, core.ClipRequest{LookID: look.ID, Moment: "back"}, 160, 90, 1, 2, false, openClip)
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, rErr := ms2.ReadFrame(buf); errors.Is(rErr, io.EOF) {
			break
		}
	}
	_ = ms2.Close()
	waitMomentMaster(t, svc, core.ClipRequest{LookID: look.ID, Moment: "back"})

	if got := countMP4Files(t, cacheDir); got != 1 {
		t.Fatalf("expected CacheBytes budget eviction to keep 1 .mp4 master on disk, got %d (%s)", got, filepath.Base(cacheDir))
	}
}

// The cold plate completes independently of its queued master.
func waitMomentMaster(t *testing.T, svc *Service, req core.ClipRequest) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := svc.Clip(context.Background(), req); ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued master did not become durable")
}
