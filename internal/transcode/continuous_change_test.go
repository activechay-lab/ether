package transcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/mediaproc"
)

// AAC turning into AC3 on the same PID leaves ffmpeg's AAC decoder fed with
// undecodable packets: no exit, no recovery. A live decoder must report the
// change (so Steady rejoins the source) instead of stalling or going silent.
func TestLiveDecoderReportsCodecChange(t *testing.T) {
	e := newRealEnv(t)
	parts := []string{filepath.Join(e.dir, "aac.ts"), filepath.Join(e.dir, "ac3.ts")}
	for i, codec := range []string{"aac", "ac3"} {
		args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=30", "-f", "lavfi", "-i", "sine=f=440:r=48000", "-t", "4", "-c:v", "libx264", "-preset", "ultrafast", "-g", "30", "-pix_fmt", "yuv420p", "-c:a", codec, "-ac", "2", "-output_ts_offset", []string{"0", "4"}[i], "-f", "mpegts", parts[i]}
		if out, err := mediaproc.CommandContext(t.Context(), time.Minute, e.ffmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("fixture: %v %s", err, out)
		}
	}
	joined := filepath.Join(e.dir, "switch.ts")
	var all []byte
	for _, p := range parts {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, b...)
	}
	if err := os.WriteFile(joined, all, 0o644); err != nil {
		t.Fatal(err)
	}
	f := RawFormat{Width: 320, Height: 180, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	d, err := e.m.StartDecoder(ctx, DecoderRequest{InputURL: joined, Format: f, Live: true, InputFormat: "mpegts"})
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	frames := 0
	for {
		_, err = d.Next(ctx)
		if err != nil {
			break
		}
		frames++
	}
	if !errors.Is(err, ErrInputChanged) {
		t.Fatalf("after %d frames the decoder ended with %v, want ErrInputChanged", frames, err)
	}
	// Everything before the switch played; the change was caught within a
	// second of media after it.
	if frames < 100 || frames > 150 {
		t.Fatalf("change reported after %d frames", frames)
	}
}
