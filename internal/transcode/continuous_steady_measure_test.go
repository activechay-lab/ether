package transcode

// Opt-in: ETHER_STEADY_MEASURE=1 go test -run TestSteadyChainMeasurement.
// One Steady channel end to end (live decoder with deinterlace/aspect/jump
// filters, Go pacing, x264 rendition) at real time on high-motion 1080p
// content: CPU of both ffmpeg processes in cores, per frame rate.
import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/mediaproc"
)

type steadyTimedRunner struct {
	Runner
	dir string
	n   atomic.Int64
}

func (r *steadyTimedRunner) Start(c Command) (Process, error) {
	report := filepath.Join(r.dir, strconv.FormatInt(r.n.Add(1), 10)+".time")
	c.Args = append([]string{"-v", "-o", report, c.Path}, c.Args...)
	c.Path = "/usr/bin/time"
	return r.Runner.Start(c)
}

func TestSteadyChainMeasurement(t *testing.T) {
	if os.Getenv("ETHER_STEADY_MEASURE") == "" || runtime.GOOS != "linux" {
		t.Skip("opt-in Linux measurement")
	}
	e := newRealEnv(t, WithoutHWDetection())
	seconds := 20
	for _, fps := range []int{30, 60} {
		t.Run(fmt.Sprintf("1080p%d", fps), func(t *testing.T) {
			input := filepath.Join(e.dir, fmt.Sprintf("motion%d.ts", fps))
			// Fast pans over fine detail plus per-frame grain: harder than
			// sports for an encoder, the bound a host budget must cover.
			src := fmt.Sprintf("testsrc2=size=1920x1080:rate=%d,scroll=h=0.004:v=0.002,noise=alls=10:allf=t+u", fps)
			args := []string{"-v", "error", "-y", "-f", "lavfi", "-i", src, "-f", "lavfi", "-i", "sine=f=440:r=48000", "-t", strconv.Itoa(seconds + 4), "-c:v", "libx264", "-preset", "veryfast", "-b:v", "12M", "-g", strconv.Itoa(fps * 2), "-pix_fmt", "yuv420p", "-c:a", "aac", "-ac", "2", "-f", "mpegts", input}
			if out, err := mediaproc.CommandContext(t.Context(), 5*time.Minute, e.ffmpeg, args...).CombinedOutput(); err != nil {
				t.Fatalf("fixture: %v %s", err, out)
			}
			dir := filepath.Join(e.dir, fmt.Sprintf("times%d", fps))
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			base := e.m.runner
			e.m.runner = &steadyTimedRunner{Runner: base, dir: dir}
			defer func() { e.m.runner = base }()
			f := RawFormat{Width: 1920, Height: 1080, FPSNum: fps, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
			ctx, cancel := context.WithTimeout(t.Context(), time.Duration(seconds+30)*time.Second)
			defer cancel()
			dec, err := e.m.StartDecoder(ctx, DecoderRequest{InputURL: input, Format: f, Live: true, InputFormat: "mpegts"})
			if err != nil {
				t.Fatal(err)
			}
			enc, err := e.m.StartEncoder(ctx, EncoderRequest{Source: f, SegmentDuration: 2 * time.Second, Decision: core.Decision{VideoCodec: "h264", AudioCodec: "aac", MaxHeight: 1080, BitrateKbps: 12000}})
			if err != nil {
				dec.Close()
				t.Fatal(err)
			}
			start := time.Now()
			count := seconds * fps
			done := 0
			for n := 0; n < count; n++ {
				frame, err := dec.Next(ctx)
				if err != nil {
					break
				}
				frame.PCM = append(frame.PCM[:0:0], make([]byte, tickPCM(f, int64(n)))...)
				if err := enc.Write(ctx, frame); err != nil {
					break
				}
				done++
				time.Sleep(time.Until(start.Add(time.Duration(n+1) * time.Second / time.Duration(fps))))
			}
			wall := time.Since(start)
			speed := enc.(*continuousEncoder).RealtimeSpeed()
			dec.Close()
			enc.Close()
			total := 0.0
			files, _ := filepath.Glob(filepath.Join(dir, "*.time"))
			for _, name := range files {
				b, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				cpu, _ := continuousResources(b, wall)
				total += cpu
			}
			t.Logf("1080p%d software Steady chain: %.2f cores over %.1fs, %d/%d frames paced, encoder speed %.2fx, host %d cores", fps, total, wall.Seconds(), done, count, speed, runtime.NumCPU())
			if done < count*95/100 {
				t.Fatalf("chain fell behind real time: %d of %d frames", done, count)
			}
		})
	}
}
