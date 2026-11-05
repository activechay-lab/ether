package transcode

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/signal"
)

var signalSamples = flag.String("signal-samples", "", "write niced, serial live-signal gallery samples to this directory")

func TestSignalSamples(t *testing.T) {
	if *signalSamples == "" {
		t.Skip("opt-in gallery render")
	}
	if err := os.MkdirAll(*signalSamples, 0755); err != nil {
		t.Fatal(err)
	}
	e := newRealEnv(t, WithoutHWDetection())
	base := e.m.runner
	for _, height := range []int{1080, 720} {
		for _, kind := range []string{"static", "bars"} {
			t.Run(fmt.Sprintf("%s-%d", kind, height), func(t *testing.T) {
				w, h := height*16/9, height
				f := RawFormat{Width: w, Height: h, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
				ceiling := 6000
				if h == 720 {
					ceiling = 3000
				}
				name := kind + "-30s"
				if h == 720 {
					name = kind + "-720p-30s"
				}
				resource := filepath.Join(*signalSamples, name+"-cpu.txt")
				e.m.runner = continuousTimedRunner{Runner: base, report: resource}
				ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
				defer cancel()
				runner, err := e.m.StartEncoder(ctx, EncoderRequest{Source: f, Decision: core.Decision{VideoCodec: "h264", AudioCodec: "aac", BitrateKbps: ceiling, AudioBitrateKbps: 192, H264Level: 40}})
				if err != nil {
					t.Fatal(err)
				}
				defer runner.Close()
				enc := runner.(*continuousEncoder)
				stream, err := runner.Segments().AttachTS(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				capture := filepath.Join(*signalSamples, name+".ts")
				out, err := os.Create(capture)
				if err != nil {
					t.Fatal(err)
				}
				copied := make(chan error, 1)
				go func() { _, err := io.Copy(out, stream); out.Close(); copied <- err }()
				frame := RawFrame{Video: make([]byte, w*h*3/2), PCM: make([]byte, 6400)}
				pcm := make([]int16, 3200)
				if kind == "bars" {
					signalBars(frame.Video, w, h)
				}
				start := time.Now()
				for n := 0; n < 900; n++ {
					if kind == "static" {
						signal.StaticVideo(frame.Video, w, h, 20261105, int64(n))
						signal.StaticAudio(pcm, int64(n), 1600, 20261105)
					} else {
						for i := 0; i < 1600; i++ {
							q := int16(math.Sin(2*math.Pi*1000*float64(n*1600+i)/48000) * math.Sqrt2 * math.Pow(10, -24.0/20) * 32767)
							pcm[i*2] = q
							pcm[i*2+1] = q
						}
					}
					for i, v := range pcm {
						binary.LittleEndian.PutUint16(frame.PCM[i*2:], uint16(v))
					}
					if err := runner.Write(ctx, frame); err != nil {
						t.Fatalf("tick %d: %v", n, err)
					}
					time.Sleep(time.Until(start.Add(time.Duration(n+1) * time.Second / 30)))
				}
				time.Sleep(time.Second)
				for _, socket := range enc.sockets {
					socket.close()
				}
				select {
				case <-enc.done:
				case <-time.After(2 * time.Second):
				}
				runner.Close()
				<-copied
				output := filepath.Join(*signalSamples, name+".mp4")
				if b, err := exec.CommandContext(t.Context(), e.ffmpeg, "-v", "error", "-y", "-i", capture, "-map", "0:v:0", "-map", "0:a:0", "-t", "30", "-c", "copy", "-movflags", "+faststart", output).CombinedOutput(); err != nil {
					t.Fatalf("remux: %v %s", err, b)
				}
				probe, err := exec.CommandContext(t.Context(), e.ffprobe, "-v", "error", "-show_entries", "format=duration,bit_rate:stream=codec_name,profile,width,height,r_frame_rate,bit_rate", "-of", "json", output).Output()
				if err != nil {
					t.Fatal(err)
				}
				os.WriteFile(filepath.Join(*signalSamples, name+"-probe.json"), probe, 0644)
				if h == 1080 {
					strip := filepath.Join(*signalSamples, kind+"-3s-strip.jpg")
					if b, err := exec.CommandContext(t.Context(), e.ffmpeg, "-v", "error", "-y", "-i", output, "-vf", `select='eq(n,0)+eq(n,45)+eq(n,90)',scale=640:360,tile=3x1`, "-frames:v", "1", strip).CombinedOutput(); err != nil {
						t.Fatalf("strip: %v %s", err, b)
					}
				}
				stats, _ := os.ReadFile(resource)
				cpu, rss := continuousResources(stats, time.Since(start))
				t.Logf("%s CPU=%.3f core RSS=%.1f MiB: %s", name, cpu, rss, probe)
			})
		}
	}
}

// Generated equivalent of the Signal family's 75% colour-bar document.
func signalBars(dst []byte, w, h int) {
	top := [][3]int{{191, 191, 191}, {191, 191, 0}, {0, 191, 191}, {0, 191, 0}, {191, 0, 191}, {191, 0, 0}, {0, 0, 191}}
	mid := [][3]int{{0, 0, 191}, {19, 19, 19}, {191, 0, 191}, {19, 19, 19}, {0, 191, 191}, {19, 19, 19}, {191, 191, 191}}
	bottom := [][3]int{{0, 33, 76}, {255, 255, 255}, {50, 0, 71}, {9, 9, 9}, {19, 19, 19}, {29, 29, 29}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			palette := top
			if y >= h*820/1080 {
				palette = bottom
			} else if y >= h*720/1080 {
				palette = mid
			}
			rgb := palette[min(len(palette)-1, x*len(palette)/w)]
			r, g, b := float64(rgb[0]), float64(rgb[1]), float64(rgb[2])
			dst[y*w+x] = byte(math.Round(16 + .182586*r + .614231*g + .062007*b))
			if x%2 == 0 && y%2 == 0 {
				i := (y/2)*(w/2) + x/2
				dst[w*h+i] = byte(math.Round(128 - .100644*r - .338572*g + .439216*b))
				dst[w*h+w*h/4+i] = byte(math.Round(128 + .439216*r - .398942*g - .040274*b))
			}
		}
	}
}
