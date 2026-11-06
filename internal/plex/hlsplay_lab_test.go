//go:build integration

package plex

// This opt-in lab executes the real Plex handlers and encoder against generated
// loopback media. Catalog/auth are deterministic local fixtures; no provider or
// account is contacted. Evidence lives in HLSPLAY_LAB_OUTPUT, outside the repo.
import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/hlsplay"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type playerLabRunner struct {
	mu      sync.Mutex
	process transcode.Process
}

func (r *playerLabRunner) Run(ctx context.Context, c transcode.Command) error {
	return (transcode.ExecRunner{}).Run(ctx, c)
}
func (r *playerLabRunner) Start(c transcode.Command) (transcode.Process, error) {
	// Pace only this lab encoder so it cannot finish before fault injection.
	args := []string{}
	for i := 0; i < len(c.Args); i++ {
		a := c.Args[i]
		if a == "-readrate" || a == "-readrate_initial_burst" {
			i++
			continue
		}
		if a == "-i" {
			args = append(args, "-re")
		}
		args = append(args, a)
	}
	c.Args = args
	p, e := (transcode.ExecRunner{}).Start(c)
	if e == nil {
		r.mu.Lock()
		r.process = p
		r.mu.Unlock()
	}
	return p, e
}

type playerLabPlayback struct {
	*fakePlayback
	url string
}

func (p *playerLabPlayback) InputURL(string) string { return p.url }
func TestHLSPlayerLab(t *testing.T) {
	root, ff, fp := os.Getenv("HLSPLAY_LAB_OUTPUT"), os.Getenv("HLSPLAY_FFMPEG"), os.Getenv("HLSPLAY_FFPROBE")
	if root == "" || ff == "" || fp == "" {
		t.Skip("explicit generated-media lab only")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	input := filepath.Join(root, "fixture.mp4")
	cmd := exec.Command(ff, "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-t", "70", "-c:v", "libx264", "-preset", "ultrafast", "-threads", "2", "-g", "96", "-c:a", "aac", "-movflags", "+faststart", input)
	if out, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("fixture: %v %s", e, out)
	}
	source := httptest.NewServer(http.FileServer(http.Dir(root)))
	defer source.Close()
	for _, name := range []string{"reference", "baseline", "ffmpeg-kill"} {
		fault := name == "ffmpeg-kill"
		t.Run(name, func(t *testing.T) {
			runner := &playerLabRunner{}
			diagnostics, e := os.Create(filepath.Join(root, name+"-encoder.log"))
			if e != nil {
				t.Fatal(e)
			}
			defer diagnostics.Close()
			manager, e := transcode.New(config.Transcode{FFmpegPath: ff, FFprobePath: fp, HWAccel: "none", TempDir: filepath.Join(root, name+"-scratch")}, slog.New(slog.NewTextHandler(diagnostics, &slog.HandlerOptions{Level: slog.LevelDebug})), transcode.WithRunner(runner), transcode.WithSegmentTimeout(12*time.Second))
			if e != nil {
				t.Fatal(e)
			}
			defer manager.Close()
			info, e := manager.Probe(context.Background(), source.URL+"/fixture.mp4")
			if e != nil {
				t.Fatal(e)
			}
			f := newFixture(func(d *Deps) { d.Hooks = Hooks{} })
			defer f.srv.Close()
			f.playback.info = *info
			f.srv.d.Playback = &playerLabPlayback{f.playback, source.URL + "/fixture.mp4"}
			f.srv.d.Transcoder = manager
			server := httptest.NewServer(f.srv)
			defer server.Close()
			output := filepath.Join(root, name)
			done := make(chan struct{})
			defer close(done)
			var killed chan bool
			if fault {
				killed = make(chan bool, 1)
				go func() {
					ticker := time.NewTicker(100 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-done:
							return
						case <-ticker.C:
							// The first segment proves an encoder is serving this session. Wait
							// 2s so the client has started playing, then kill our exact process.
							if _, e := os.Stat(filepath.Join(output, "segment-000001.ts")); e == nil {
								select {
								case <-done:
									return
								case <-time.After(2 * time.Second):
								}
								runner.mu.Lock()
								p := runner.process
								runner.mu.Unlock()
								if p != nil {
									p.Stop()
									killed <- true
								}
								return
							}
						}
					}
				}()
			}
			c := hlsplay.Config{BaseURL: server.URL, Token: ownerToken, RatingKey: fightClub.Key.String(), Output: output, Window: 30 * time.Second, ForwardBuffer: 8 * time.Second, RequestTimeout: 10 * time.Second, Backoff: 100 * time.Millisecond, Tries: 2, SegmentDuration: 4 * time.Second, FFmpeg: ff, FFprobe: fp}
			if name == "baseline" {
				c.Reference, e = hlsplay.LoadReference(filepath.Join(root, "reference", "report.json"))
				if e != nil {
					t.Fatal(e)
				}
			}
			r, e := hlsplay.Run(context.Background(), c)
			if fault {
				select {
				case <-killed:
				default:
					t.Fatal("fault not injected")
				}
				if len(r.Stalls) == 0 || r.PlaybackPassed {
					t.Fatalf("kill undetected: %+v err=%v", r, e)
				}
			} else if e != nil || !r.PlaybackPassed {
				t.Fatalf("baseline playback: %v outcome=%s segments=%d stalls=%+v", e, r.Outcome, len(r.Segments), r.Stalls)
			}
			if name == "baseline" && !r.Continuity.Verified {
				t.Errorf("continuity: %+v", r.Continuity)
			}
			t.Logf("%s outcome=%s segments=%d stalls=%d continuity=%t", name, r.Outcome, len(r.Segments), len(r.Stalls), r.Continuity.Verified)
		})
	}
}

// TestHLSPlayerDriverLab exercises the shipped CLI and Python orchestration
// together, reusing a completed local reference capture. It injects no loss.
func TestHLSPlayerDriverLab(t *testing.T) {
	root, ff, fp := os.Getenv("HLSPLAY_LAB_OUTPUT"), os.Getenv("HLSPLAY_FFMPEG"), os.Getenv("HLSPLAY_FFPROBE")
	binary, driver, reference := os.Getenv("HLSPLAY_LAB_BINARY"), os.Getenv("HLSPLAY_LAB_DRIVER"), os.Getenv("HLSPLAY_LAB_REFERENCE")
	if root == "" || ff == "" || fp == "" || binary == "" || driver == "" || reference == "" {
		t.Skip("explicit CLI/driver lab only")
	}
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	source := httptest.NewServer(http.FileServer(http.Dir(filepath.Dir(filepath.Dir(reference)))))
	defer source.Close()
	runner := &playerLabRunner{}
	manager, e := transcode.New(config.Transcode{FFmpegPath: ff, FFprobePath: fp, HWAccel: "none", TempDir: filepath.Join(root, "scratch")}, slog.Default(), transcode.WithRunner(runner))
	if e != nil {
		t.Fatal(e)
	}
	defer manager.Close()
	info, e := manager.Probe(context.Background(), source.URL+"/fixture.mp4")
	if e != nil {
		t.Fatal(e)
	}
	f := newFixture(func(d *Deps) { d.Hooks = Hooks{} })
	defer f.srv.Close()
	f.playback.info = *info
	f.srv.d.Playback = &playerLabPlayback{f.playback, source.URL + "/fixture.mp4"}
	f.srv.d.Transcoder = manager
	server := httptest.NewServer(f.srv)
	defer server.Close()
	tokenFile := filepath.Join(root, "viewer-token")
	if e = os.WriteFile(tokenFile, []byte(ownerToken), 0600); e != nil {
		t.Fatal(e)
	}
	configPath := filepath.Join(root, "driver.json")
	data, e := json.Marshal(map[string]any{"url": server.URL, "tokenFile": tokenFile, "ratingKey": fightClub.Key.String(), "hlsplay": binary, "ffmpeg": ff, "ffprobe": fp, "reference": reference, "role": "controller", "action": "none", "phase": "steady", "windowSeconds": 30, "bufferSeconds": 8})
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(configPath, data, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	out, e := exec.CommandContext(ctx, "python3", driver, "--config", configPath, "--output", filepath.Join(root, "no-loss")).CombinedOutput()
	if e != nil {
		t.Fatalf("driver: %v %s", e, out)
	}
	data, e = os.ReadFile(filepath.Join(root, "no-loss", "report.json"))
	if e != nil {
		t.Fatal(e)
	}
	var report struct {
		Passed bool `json:"passed"`
	}
	if e = json.Unmarshal(data, &report); e != nil || !report.Passed {
		t.Fatalf("driver report: %v passed=%t", e, report.Passed)
	}
}
