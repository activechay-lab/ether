package livetv

import (
	"context"
	"flag"
	"image"
	"image/color"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/encode"
	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"github.com/braedonsaunders/ether/internal/transcode"
)

var staticUnifyDir = flag.String("static-unify-demo", "", "offline switcher evidence directory (masters/ contains before and texture-probe inputs)")

type staticEvidenceGraphics struct {
	doc      scene.Document
	bindings raster.Bindings
}

func (g staticEvidenceGraphics) Duration(string, string) (time.Duration, bool) {
	return 8 * time.Second, true
}
func (g staticEvidenceGraphics) Clip(context.Context, core.ClipRequest) (core.ClipRef, bool) {
	return core.ClipRef{}, false
}
func (g staticEvidenceGraphics) Ensure(context.Context, core.ClipRequest) {}
func (g staticEvidenceGraphics) Overlay(context.Context, core.ClipRequest) (scene.Document, raster.Bindings, bool) {
	return g.doc, g.bindings, true
}

// TestStaticUnifyDemo advances the real switcher with a deterministic offline
// frame clock. Encoding has no real-time deadlines, so a busy shared workstation
// cannot drop texture frames. This is visual evidence, not a real-time load test.
func TestStaticUnifyDemo(t *testing.T) {
	if *staticUnifyDir == "" {
		t.Skip("opt-in static evidence")
	}
	ctx := t.Context()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Fatal(err)
	}
	fp, err := exec.LookPath("ffprobe")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := transcode.New(config.Transcode{FFmpegPath: ff, FFprobePath: fp, TempDir: t.TempDir(), HWAccel: "none"}, slog.New(slog.NewTextHandler(io.Discard, nil)), transcode.WithoutHWDetection())
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	f := transcode.RawFormat{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
	entries, err := os.ReadDir(filepath.Join(*staticUnifyDir, "masters"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"nocturne-offair-after", "signal-static-after"}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".mp4" {
			names = append(names, e.Name()[:len(e.Name())-4])
		}
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(*staticUnifyDir, name+".mp4")
			if _, err := os.Stat(out); err == nil {
				return
			}
			var d transcode.Decoder
			frames := 120
			if name == "nocturne-offair-after" {
				doc, _ := families.Get("late-night", "offair")
				g := staticEvidenceGraphics{doc, raster.Bindings{Bindings: families.Defaults("late-night")}}
				s := testService(t, Options{Graphics: g})
				d, _, err = s.openGraphicsDecoder(ctx, manager, core.ClipRequest{ChannelID: "nocturne", Moment: "offair"}, f, true)
				if err == nil {
					if _, ok := d.(*signalInputDecoder); !ok {
						t.Fatal("static must air live")
					}
				}
				frames = 240
			} else if name == "signal-static-after" {
				s := testService(t, Options{})
				d, _, err = s.openSignalDecoder(ctx, core.ClipRequest{ChannelID: "nocturne", Moment: "screen.static"}, f)
				frames = 240
			} else {
				d, err = manager.StartDecoder(ctx, transcode.DecoderRequest{InputURL: filepath.Join(*staticUnifyDir, "masters", name+".mp4"), Format: f, Loop: true})
			}
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			plan := broadcastPlan{id: "evidence"}
			b, err := newBroadcast(ctx, manager, liveSignalPlanner{plan}, f)
			if err != nil {
				t.Fatal(err)
			}
			defer b.cancel()
			inputCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			b.current = &broadcastInput{ctx: inputCtx, cancel: cancel, plan: plan, decoder: d, frames: make(chan transcode.RawFrame, 1), errs: make(chan error, 1)}
			q := newBroadcastFrameQueue(30)
			b.renditions["evidence"] = &broadcastRendition{queue: q, cancel: func() {}}
			generation := time.Duration(0)
			render := func(n int) *image.RGBA {
				start := time.Now()
				raw, e := d.Next(ctx)
				if e != nil {
					panic(e)
				}
				b.current.frames <- raw
				b.tick = int64(n)
				b.step(b.origin.Add(time.Duration(n) * time.Second / 30))
				raw, _ = q.pop(ctx)
				generation += time.Since(start)
				return evidenceRGBA(raw.Video, f.Width, f.Height)
			}
			err = encode.WithOptions(ctx, ff, render, frames, 30, "", out, encode.Options{Workers: 1, MaxRate: "5000k", BufferSize: "5000k"})
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%s: real switcher, native 1080p30, %.3f decoder wait + switcher elapsed / clip duration (not CPU)", name, generation.Seconds()/(float64(frames)/30))
		})
	}
}
func evidenceRGBA(v []byte, w, h int) *image.RGBA {
	im := image.NewRGBA(image.Rect(0, 0, w, h))
	q := func(v int) byte { return byte(max(0, min(255, (v+128)>>8))) }
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := y/2*(w/2) + x/2
			yy := int(v[y*w+x]) - 16
			u := int(v[w*h+i]) - 128
			vv := int(v[w*h*5/4+i]) - 128
			im.SetRGBA(x, y, color.RGBA{q(298*yy + 459*vv), q(298*yy - 55*u - 136*vv), q(298*yy + 541*u), 255})
		}
	}
	return im
}
