package livetv

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/onair"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// Opt-in visual evidence uses the real seeded looks, cold decoder, and FFmpeg.
func TestColdHoldForegroundEvidence(t *testing.T) {
	dir := os.Getenv("ETHER_COLD_HOLD_EVIDENCE")
	if dir == "" {
		t.Skip("set ETHER_COLD_HOLD_EVIDENCE for offline visual evidence")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	graphics, err := onair.New(onair.Options{Showcase: true, DB: db, Dir: t.TempDir(), Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	s := &Service{o: Options{Graphics: graphics, Now: time.Now}}
	f := transcode.RawFormat{Width: 1280, Height: 720, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
	for _, look := range []struct{ id, name string }{{"80", "CHROMA 80"}, {"90", "RIOT 90"}, {"00", "ORBIT 00"}, {"10", "SHIFT 10"}} {
		t.Run(look.id, func(t *testing.T) {
			d, _, err := s.openSignalDecoder(t.Context(), core.ClipRequest{ChannelID: "cold-" + look.id, LookID: "showcase-" + look.id, Moment: "ident", Bind: map[string]string{"channel.name": look.name, "screen.reason": "moment"}}, f)
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			out := filepath.Join(dir, look.id+".mp4")
			cmd := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-y", "-f", "rawvideo", "-pixel_format", "yuv420p", "-video_size", "1280x720", "-framerate", "30", "-i", "pipe:0", "-an", "-c:v", "libx264", "-threads", "1", "-preset", "ultrafast", out)
			pipe, err := cmd.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			cmd.Stderr = os.Stderr
			if err = cmd.Start(); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 90; i++ {
				frame, e := d.Next(t.Context())
				if e != nil {
					pipe.Close()
					cmd.Wait()
					t.Fatal(e)
				}
				if _, e = pipe.Write(frame.Video); e != nil {
					pipe.Close()
					cmd.Wait()
					t.Fatal(e)
				}
			}
			pipe.Close()
			if err = cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			for _, n := range []int{0, 12, 48} {
				cmd := exec.CommandContext(t.Context(), "ffmpeg", "-v", "error", "-y", "-i", out, "-vf", fmt.Sprintf("select=eq(n\\,%d)", n), "-frames:v", "1", filepath.Join(dir, fmt.Sprintf("%s-%02d.png", look.id, n)))
				if b, e := cmd.CombinedOutput(); e != nil {
					t.Fatalf("extract: %s %v", b, e)
				}
			}
		})
	}
}
