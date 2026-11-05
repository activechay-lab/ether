package transcode

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair/encode"
	"github.com/braedonsaunders/ether/internal/onair/families"
	"github.com/braedonsaunders/ether/internal/onair/raster"
	"github.com/braedonsaunders/ether/internal/onair/scene"
	"github.com/braedonsaunders/ether/internal/onair/signal"
)

var signalGallery = flag.String("signal-gallery", "", "write owner gallery through the actual continuous runner")
var signalDocuments = flag.String("signal-documents", "internal/onair/families/documents", "exact family-document directory")
var signalNotify = flag.String("signal-notify-thread", "", "notify this requesting coordinator after each finished sample")

func TestSignalGallery(t *testing.T) {
	if *signalGallery == "" {
		t.Skip("opt-in owner gallery")
	}
	if err := os.MkdirAll(*signalGallery, 0755); err != nil {
		t.Fatal(err)
	}
	e := newRealEnv(t, WithoutHWDetection())
	type sample struct {
		name, family, moment, line, caption string
		seconds                             int
	}
	samples := []sample{{"static-30s", "signal", "static", "", "v2 live analogue static: fine bilinear snow, horizontal bandwidth smear, per-line jitter, irregular rolling bar, tears, hiss and wandering hum", 30}, {"bars-20s", "signal", "screen.bars", "", "75% colour bars with Nocturne 7 station ID and continuous 1 kHz tone at -24 dBFS RMS", 20}, {"card-back-at-6", "signal", "screen.card", "Back at 6:00 PM", "Modern card: Back at 6:00 PM", 15}, {"card-reconnecting", "signal", "screen.card", "Reconnecting…", "Modern card: Reconnecting…", 15}, {"card-waiting-for-connection", "signal", "screen.card", "Waiting for a free connection", "Modern card: Waiting for a free connection", 15}, {"we-will-be-right-back", "signal", "screen.brb", "", "We'll be right back", 15}, {"test-card", "signal", "screen.testcard", "", "Test card with continuous -24 dBFS 1 kHz reference tone", 15}}
	for _, family := range []string{"clean", "late-night"} {
		for _, moment := range []string{"ident", "upnext", "break"} {
			samples = append(samples, sample{name: family + "-" + moment, family: family, moment: moment, caption: family + " " + moment + ": Nocturne 7; Night of the Living Dead; 12:00 AM", seconds: 15})
		}
	}
	samples = append(samples, sample{name: "late-night-programme-overlays", family: "late-night", moment: "overlays", caption: "Decoded 1080p programme with Late night corner logo and lower third composited in Go", seconds: 20})
	for _, s := range samples {
		t.Run(s.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Minute)
			defer cancel()
			load := func(moment string) scene.Document {
				b, err := os.ReadFile(filepath.Join(*signalDocuments, s.family+"-"+moment+".json"))
				if err != nil {
					t.Fatal(err)
				}
				doc, err := scene.Decode(b)
				if err != nil {
					t.Fatal(err)
				}
				return doc
			}
			bindings := families.Defaults(s.family)
			bindings.Slots["channel.name"] = "Nocturne 7"
			bindings.Slots["station.id"] = "NOCTURNE 7 · ETHER"
			bindings.Slots["next.title"] = "Night of the Living Dead"
			bindings.Slots["now.title"] = "The Midnight Feature"
			bindings.Slots["next.time"] = "12:00 AM"
			bindings.Slots["next.meta"] = "1968 · Horror · 1h 36m"
			bindings.Slots["next.logline"] = "The night has only just begun."
			bindings.Slots["screen.line2"] = ""
			bindings.Tokens["words.now"] = "NOW PLAYING"
			bindings.Tokens["words.break"] = "A SHORT INTERMISSION"
			if s.line != "" {
				bindings.Slots["screen.line1"] = s.line
			}
			rb := raster.Bindings{Bindings: bindings}
			var doc, bug, lower scene.Document
			var audio []int16
			var programme Decoder
			var fixed []byte
			if s.moment != "static" && s.moment != "overlays" {
				doc = load(s.moment)
				a := scene.Audio{Generator: "bed", Seed: 7}
				if doc.Audio != nil {
					a = *doc.Audio
				}
				audio, _ = encode.PCM(a, s.seconds)
				preview := raster.Render(doc, rb, doc.Poster, 1920, 1080)
				file, err := os.Create(filepath.Join(*signalGallery, s.name+"-poster.png"))
				if err != nil {
					t.Fatal(err)
				}
				png.Encode(file, preview)
				file.Close()
				if s.family == "signal" {
					fixed = make([]byte, 1920*1080*3/2)
					galleryRGBA(fixed, preview)
				}
			}
			format := RawFormat{Width: 1920, Height: 1080, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
			if s.moment == "overlays" {
				bug = load("bug")
				lower = load("lowerthird")
				input := filepath.Join(*signalGallery, "programme-testsrc.mp4")
				if _, err := os.Stat(input); err != nil {
					b, err := exec.CommandContext(ctx, e.ffmpeg, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=1920x1080:rate=30", "-f", "lavfi", "-i", "sine=frequency=220:sample_rate=48000", "-t", "21", "-c:v", "libx264", "-preset", "veryfast", "-threads", "2", "-crf", "22", "-c:a", "aac", "-b:a", "128k", input).CombinedOutput()
					if err != nil {
						t.Fatalf("programme: %v %s", err, b)
					}
				}
				var err error
				programme, err = e.m.StartDecoder(ctx, DecoderRequest{InputURL: input, Format: format})
				if err != nil {
					t.Fatal(err)
				}
				defer programme.Close()
			}
			runner, err := e.m.StartEncoder(ctx, EncoderRequest{Source: format, Decision: core.Decision{VideoCodec: "h264", AudioCodec: "aac", BitrateKbps: 6000, AudioBitrateKbps: 192, H264Level: 40}})
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
			capture := filepath.Join(*signalGallery, "."+s.name+"-capture.ts")
			file, err := os.Create(capture)
			if err != nil {
				t.Fatal(err)
			}
			copied := make(chan error, 1)
			go func() { _, err := io.Copy(file, stream); file.Close(); copied <- err }()
			frame := RawFrame{Video: make([]byte, 1920*1080*3/2), PCM: make([]byte, 6400)}
			pcm := make([]int16, 3200)
			start := time.Now()
			for n := 0; n < s.seconds*30; n++ {
				switch s.moment {
				case "static":
					signal.StaticVideo(frame.Video, 1920, 1080, 20261105, int64(n))
					signal.StaticAudio(pcm, int64(n), 1600, 20261105)
				case "overlays":
					decoded, err := programme.Next(ctx)
					if err != nil {
						t.Fatal(err)
					}
					frame = decoded
					overlay := raster.Render(bug, rb, float64(n)/30, 1920, 1080)
					lowerImage := raster.Render(lower, rb, float64(n)/30, 1920, 1080)
					draw.Draw(overlay, overlay.Rect, lowerImage, image.Point{}, draw.Over)
					galleryOverlay(frame.Video, overlay)
				default:
					if fixed != nil {
						copy(frame.Video, fixed)
					} else {
						at := math.Mod(float64(n)/30, doc.Duration)
						galleryRGBA(frame.Video, raster.Render(doc, rb, at, 1920, 1080))
					}
					copy(pcm, audio[n*3200:(n+1)*3200])
				}
				if s.moment != "overlays" {
					for i, v := range pcm {
						binary.LittleEndian.PutUint16(frame.PCM[i*2:], uint16(v))
					}
				}
				if err := runner.Write(ctx, frame); err != nil {
					t.Fatalf("tick%d: %v", n, err)
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
			output := filepath.Join(*signalGallery, s.name+".mp4")
			if b, err := exec.CommandContext(ctx, e.ffmpeg, "-v", "error", "-y", "-i", capture, "-map", "0:v:0", "-map", "0:a:0", "-t", strconv.Itoa(s.seconds), "-c", "copy", "-movflags", "+faststart", output).CombinedOutput(); err != nil {
				t.Fatalf("remux: %v %s", err, b)
			}
			// Every accepted gallery file must decode completely, with its audio track.
			if b, err := exec.CommandContext(ctx, e.ffmpeg, "-v", "error", "-xerror", "-i", output, "-f", "null", "-").CombinedOutput(); err != nil {
				t.Fatalf("decode: %v %s", err, b)
			}
			metadata := galleryMetadata(t, ctx, e.ffprobe, output, s.caption)
			b, _ := json.MarshalIndent(metadata, "", "  ")
			if err := os.WriteFile(filepath.Join(*signalGallery, s.name+".json"), append(b, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
			_ = os.Remove(capture)
			if *signalNotify != "" {
				message := fmt.Sprintf("LANDED %s + .json in %s: %s; avg %.1f / peak1s %.1f kb/s, 6000 ceiling; full -xerror decode passed.", s.name, *signalGallery, s.caption, metadata["bitrate_kbps"], metadata["peak_1s_bitrate_kbps"])
				if err := exec.CommandContext(ctx, "bb", "thread", "tell", *signalNotify, message).Run(); err != nil {
					t.Logf("notification: %v", err)
				}
			}
			t.Logf("LANDED %s %s", output, b)
		})
	}
}

func galleryMetadata(t *testing.T, ctx context.Context, probe, path, caption string) map[string]any {
	t.Helper()
	b, err := exec.CommandContext(ctx, probe, "-v", "error", "-show_packets", "-show_format", "-show_entries", "packet=pts_time,size:format=duration,bit_rate", "-of", "json", path).Output()
	if err != nil {
		t.Fatal(err)
	}
	var data struct {
		Packets []struct {
			PTS  string `json:"pts_time"`
			Size string
		}
		Format struct {
			Duration string
			BitRate  string `json:"bit_rate"`
		}
	}
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatal(err)
	}
	bins := map[int]int{}
	for _, p := range data.Packets {
		pts, _ := strconv.ParseFloat(p.PTS, 64)
		size, _ := strconv.Atoi(p.Size)
		bins[int(pts)] += size * 8
	}
	peak := 0
	for sec, bits := range bins {
		if sec >= 0 {
			peak = max(peak, bits)
		}
	}
	duration, _ := strconv.ParseFloat(data.Format.Duration, 64)
	bitrate, _ := strconv.ParseFloat(data.Format.BitRate, 64)
	if peak > 6000000 {
		t.Fatalf("session ceiling exceeded: %d bit/s", peak)
	}
	return map[string]any{"caption": caption, "resolution": "1920x1080", "bitrate_kbps": bitrate / 1000, "duration_s": duration, "peak_1s_bitrate_kbps": float64(peak) / 1000, "session_ceiling_kbps": 6000}
}

func galleryRGBA(dst []byte, im *image.RGBA) {
	w, h := im.Rect.Dx(), im.Rect.Dy()
	for y := 0; y < h; y += 2 {
		for x := 0; x < w; x += 2 {
			r, g, b := 0, 0, 0
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					i := im.PixOffset(x+dx, y+dy)
					rr, gg, bb := int(im.Pix[i]), int(im.Pix[i+1]), int(im.Pix[i+2])
					r += rr
					g += gg
					b += bb
					dst[(y+dy)*w+x+dx] = byte(16 + (47*rr+157*gg+16*bb+128)/256)
				}
			}
			r /= 4
			g /= 4
			b /= 4
			i := (y/2)*(w/2) + x/2
			dst[w*h+i] = byte(128 + (-26*r-87*g+112*b+128)/256)
			dst[w*h+w*h/4+i] = byte(128 + (112*r-102*g-10*b+128)/256)
		}
	}
}
func galleryOverlay(dst []byte, im *image.RGBA) {
	w, h := im.Rect.Dx(), im.Rect.Dy()
	for y := 0; y < h; y += 2 {
		for x := 0; x < w; x += 2 {
			r, g, b, a := 0, 0, 0, 0
			for dy := 0; dy < 2; dy++ {
				for dx := 0; dx < 2; dx++ {
					i := im.PixOffset(x+dx, y+dy)
					alpha := int(im.Pix[i+3])
					if alpha == 0 {
						continue
					}
					rr, gg, bb := int(im.Pix[i]), int(im.Pix[i+1]), int(im.Pix[i+2])
					r += rr
					g += gg
					b += bb
					a += alpha
					j := (y+dy)*w + x + dx
					dst[j] = byte((int(dst[j])*(255-alpha)+16*alpha)/255 + (47*rr+157*gg+16*bb+128)/256)
				}
			}
			if a == 0 {
				continue
			}
			r /= 4
			g /= 4
			b /= 4
			a /= 4
			i := (y/2)*(w/2) + x/2
			j := w*h + i
			dst[j] = byte(max(0, min(255, (int(dst[j])*(255-a)+128*a)/255+(-26*r-87*g+112*b+128)/256)))
			j += w * h / 4
			dst[j] = byte(max(0, min(255, (int(dst[j])*(255-a)+128*a)/255+(112*r-102*g-10*b+128)/256)))
		}
	}
}

// The source checksum is available when reviewing exact family provenance.
func galleryDocumentDigest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }
