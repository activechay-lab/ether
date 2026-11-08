//go:build transcodechaos

package plex

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// TestTranscodeChaosAVPlayer plays the corpus client sequences of
// TestTranscodeChaosCorpusScenarios in AVPlayer (CHAOS_AVVOD, built from
// tools/avprobe/vod) instead of decoding single segments with ffmpeg: seeks
// and scrubs inside the item, pause/resume to the end, and Plex-style restarts
// for track and quality changes. Each case writes one JSON line to
// CHAOS_AVREPORT; the transcodechaos -vod-avplayer mode judges them. Media
// segments the player fetched are kept under CHAOS_AVKEEP/<case>/ for the
// published-media A/V sync measurement.
func TestTranscodeChaosAVPlayer(t *testing.T) {
	corpus, ff, fp, probe, report := os.Getenv("CHAOS_CORPUS"), os.Getenv("CHAOS_FFMPEG"), os.Getenv("CHAOS_FFPROBE"), os.Getenv("CHAOS_AVVOD"), os.Getenv("CHAOS_AVREPORT")
	if probe == "" || report == "" {
		t.Skip("set CHAOS_AVVOD and CHAOS_AVREPORT (macOS, outside a sandbox)")
	}
	entries, _ := filepath.Glob(filepath.Join(corpus, "corpus-*.good"))
	if len(entries) == 0 {
		t.Skip("no public corpus staged")
	}
	only := os.Getenv("CHAOS_AVONLY")
	keep := os.Getenv("CHAOS_AVKEEP")
	out, err := os.OpenFile(report, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	source := httptest.NewServer(http.FileServer(http.Dir(corpus)))
	defer source.Close()
	// Shipped transcode defaults (fast start, ABR ladder, VBV), software
	// encode unless CHAOS_HWACCEL names one: what a viewer's server runs.
	tc := config.Defaults(t.TempDir()).Transcode
	tc.FFmpegPath, tc.FFprobePath, tc.HWAccel, tc.TempDir = ff, fp, "none", filepath.Join(t.TempDir(), "work")
	if hw := os.Getenv("CHAOS_HWACCEL"); hw != "" {
		tc.HWAccel = hw
	}
	m, err := transcode.New(tc, slog.New(slog.NewTextHandler(io.Discard, nil)), transcode.WithSegmentTimeout(15*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	for _, entry := range entries {
		name := strings.TrimSuffix(filepath.Base(entry), ".good")
		t.Run(name, func(t *testing.T) {
			info, err := m.Probe(context.Background(), source.URL+"/"+name+".good")
			if err != nil {
				t.Fatal("probe failed")
			}
			info.Keyframes = nil // as the ffmpeg sequences: demuxer seeking without an Ether index
			if info.Duration < time.Second {
				t.Skip("source has no usable duration for seek scenarios")
			}
			for _, path := range []string{"hls", "hls-fmp4"} {
				t.Run(path, func(t *testing.T) {
					for _, scenario := range []string{"seek-forward-backward", "scrub-fast-forward", "pause-resume-eos", "track-quality-change"} {
						t.Run(scenario, func(t *testing.T) {
							id := name + "/" + path + "/" + scenario
							if only != "" && !strings.Contains(id, only) {
								t.Skip("not selected")
							}
							line := avplayerCase(t, m, source.URL, name, path, scenario, *info, probe, keep)
							b, _ := json.Marshal(line)
							if _, err := out.Write(append(b, '\n')); err != nil {
								t.Fatal(err)
							}
						})
					}
				})
			}
		})
	}
}

type avplayerLine struct {
	Case     string          `json:"case"`
	Asset    string          `json:"asset"`
	Path     string          `json:"path"`
	Scenario string          `json:"scenario"`
	Duration float64         `json:"durationSeconds"`
	Script   string          `json:"script"`
	Decision []string        `json:"decisions"`
	Probe    json.RawMessage `json:"player,omitempty"`
	Error    string          `json:"error,omitempty"`
	Kept     string          `json:"kept,omitempty"`
	Requests []string        `json:"requests"`
}

var avplayerMedia = regexp.MustCompile(`/session/[^/]+/[^/]+/(init\.mp4|\d+\.(ts|m4s))$`)

func avplayerCase(t *testing.T, m *transcode.Manager, sourceURL, name, path, scenario string, info core.MediaInfo, probe, keep string) avplayerLine {
	f := newFixture()
	defer f.srv.Close()
	duration := info.Duration
	f.playback.info = info
	f.srv.d.Playback = &chaosPlayback{f.playback, sourceURL + "/" + name + ".media"}
	rec := &chaosTranscoderRecorder{Transcoder: m}
	f.srv.d.Transcoder = rec
	id := "avplayer-" + strings.NewReplacer("/", "-", ".", "-").Replace(name+"-"+path+"-"+scenario)
	headers := map[string]string{"X-Plex-Product": "Plex Web", "X-Plex-Platform": "Chrome", "X-Plex-Token": ownerToken, "X-Plex-Client-Identifier": id}
	if path == "hls-fmp4" {
		headers["X-Plex-Client-Profile-Extra"] = "add-transcode-target(type=videoProfile&context=streaming&protocol=hls&container=mp4&videoCodec=h264,hevc&audioCodec=aac)"
	}
	line := avplayerLine{Case: name + "/" + path + "/" + scenario, Asset: name, Path: path, Scenario: scenario, Duration: duration.Seconds()}
	var dir string
	if keep != "" {
		dir = filepath.Join(keep, strings.ReplaceAll(line.Case, "/", "_"))
		_ = os.RemoveAll(dir)
		_ = os.MkdirAll(dir, 0o700)
		line.Kept = dir
	}
	var mu sync.Mutex
	fetched := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range headers {
			if r.Header.Get(k) == "" {
				r.Header.Set(k, v)
			}
		}
		r.RemoteAddr = "192.168.1.50:51000"
		rw := &avplayerTee{ResponseWriter: w, status: 200}
		media := avplayerMedia.FindStringSubmatch(r.URL.Path)
		if media != nil && dir != "" || !strings.Contains(r.URL.Path, "/session/") {
			rw.capture = &bytes.Buffer{}
		}
		began := time.Now()
		f.srv.ServeHTTP(rw, r)
		mu.Lock()
		entry := fmt.Sprintf("%s %s -> %d %dB %.3fs", r.Method, r.URL.Path, rw.status, rw.n, time.Since(began).Seconds())
		if rw.status != 200 && rw.capture != nil {
			entry += " body=" + strings.TrimSpace(string(rw.capture.Bytes()[:min(200, rw.capture.Len())]))
		}
		line.Requests = append(line.Requests, entry)
		if media != nil && dir != "" && rw.status == 200 {
			fetched++
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("%04d-%s", fetched, strings.ReplaceAll(strings.TrimPrefix(media[0], "/session/"), "/", "_"))), rw.capture.Bytes(), 0o600)
		}
		mu.Unlock()
	}))
	defer srv.Close()
	start := func(offset time.Duration, extra string) string {
		direct := "1"
		if strings.Contains(extra, "&directStream=0") {
			direct = "0"
			extra = strings.ReplaceAll(extra, "&directStream=0", "")
		}
		q := fmt.Sprintf("protocol=hls&path=%%2Flibrary%%2Fmetadata%%2F%s&session=%s&directPlay=0&directStream=%s&offset=%.3f%s&X-Plex-Token=%s", fightClub.Key.String(), id, direct, offset.Seconds(), extra, url.QueryEscape(ownerToken))
		return srv.URL + "/video/:/transcode/universal/start.m3u8?" + q
	}
	d := duration.Seconds()
	var script []string
	run := 22.0
	switch scenario {
	case "seek-forward-backward":
		script = []string{fmt.Sprintf("6|seek|%.3f", d*2/3), fmt.Sprintf("14|seek|%.3f", d/4)}
	case "scrub-fast-forward":
		script = []string{fmt.Sprintf("6|seek|%.3f", d/2), fmt.Sprintf("8|seek|%.3f", d/10), fmt.Sprintf("10|seek|%.3f", d/3), fmt.Sprintf("12|seek|%.3f", d*2/3)}
		run = 20
	case "pause-resume-eos":
		script = []string{"6|pause", "9|play", "12|seek|end-3"}
		run = 20
	case "track-quality-change":
		at := 6.0
		for _, st := range f.playback.info.Streams {
			if st.Type == "audio" && st.Index != firstAudioIndex(f.playback.info.Streams) {
				track := f.bindStream(fightClub.Key, st.Index, st)
				script = append(script, fmt.Sprintf("%.0f|replace|%s|%.3f", at, start(duration/4, fmt.Sprintf("&audioStreamID=%d", track)), d/4))
				at += 8
				break
			}
		}
		for _, st := range f.playback.info.Streams {
			if st.Type == "subtitle" {
				track := f.bindStream(fightClub.Key, st.Index, st)
				script = append(script, fmt.Sprintf("%.0f|replace|%s|%.3f", at, start(duration/4, fmt.Sprintf("&subtitles=burn&subtitleStreamID=%d", track)), d/4))
				at += 8
				break
			}
		}
		script = append(script, fmt.Sprintf("%.0f|replace|%s|%.3f", at, start(duration/3, "&directStream=0&maxVideoBitrate=200&videoResolution=160x90"), d/3))
		run = at + 8
	}
	line.Script = strings.Join(script, ";")
	defer f.get(t, "/video/:/transcode/universal/stop?session="+id, "X-Plex-Product", "Plex Web", "X-Plex-Platform", "Chrome")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(run+30)*time.Second)
	defer cancel()
	// The client asks for a decision first; Ether only starts armed sessions.
	if dr, err := http.Get(strings.Replace(start(0, ""), "/universal/start.m3u8", "/universal/decision", 1)); err == nil {
		_, _ = io.Copy(io.Discard, dr.Body)
		dr.Body.Close()
		line.Requests = append(line.Requests[:0:0], append([]string{fmt.Sprintf("preflight decision -> %d", dr.StatusCode)}, line.Requests...)...)
	}
	if os.Getenv("CHAOS_AVDEBUG") != "" {
		resp, err := http.Get(start(0, ""))
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Logf("preflight %d %s url=%s", resp.StatusCode, b, start(0, ""))
		}
	}
	cmd := exec.CommandContext(ctx, probe, start(0, ""), fmt.Sprintf("%.0f", run), line.Script)
	b, err := cmd.Output()
	if err != nil {
		line.Error = "avvod: " + err.Error()
	} else {
		line.Probe = json.RawMessage(bytes.TrimSpace(b))
	}
	for _, r := range rec.requests {
		dec := r.Decision
		line.Decision = append(line.Decision, fmt.Sprintf("%s v=%s copy=%v a=%s copy=%v ch=%d h=%d br=%d", dec.Container, dec.VideoCodec, dec.VideoCopy, dec.AudioCodec, dec.AudioCopy, dec.AudioChannels, dec.MaxHeight, dec.BitrateKbps))
	}
	return line
}

func firstAudioIndex(streams []core.StreamInfo) int {
	for _, st := range streams {
		if st.Type == "audio" {
			return st.Index
		}
	}
	return -1
}

type avplayerTee struct {
	http.ResponseWriter
	status  int
	n       int
	capture *bytes.Buffer
}

func (w *avplayerTee) WriteHeader(code int) { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *avplayerTee) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.n += n
	if w.capture != nil {
		w.capture.Write(b[:n])
	}
	return n, err
}
func (w *avplayerTee) Flush() {
	if fl, ok := w.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
}
