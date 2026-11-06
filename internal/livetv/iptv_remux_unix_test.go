//go:build darwin || linux

package livetv

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	clientcaps "github.com/braedonsaunders/ether/internal/frontend/kit/caps"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type measuredLiveTranscoder struct {
	*transcode.Manager
	requests []core.HLSRequest
}

func (m *measuredLiveTranscoder) StartHLS(ctx context.Context, r core.HLSRequest) (core.HLSSession, error) {
	m.requests = append(m.requests, r)
	return m.Manager.StartHLS(ctx, r)
}
func childCPU() time.Duration {
	var r syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_CHILDREN, &r)
	return time.Duration(r.Utime.Sec+r.Stime.Sec)*time.Second + time.Duration(r.Utime.Usec+r.Stime.Usec)*time.Microsecond
}

// Opt-in real-media proof; the provider serves only a generated local fixture.
func TestIPTVRealHEVCRemux(t *testing.T) {
	file := os.Getenv("ETHER_IPTV_HEVC_FIXTURE")
	if file == "" {
		t.Skip("set ETHER_IPTV_HEVC_FIXTURE to a generated eight-second 4K HEVC TS")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"Plex for Android (TV)", "Plex for Apple TV", "Jellyfin HEVC", "Emby HEVC", "H264 viewer"} {
		t.Run(profile, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			var calls atomic.Int32
			up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "video/mp2t")
				started := time.Now()
				for off := 0; off < len(data); {
					end := min(off+18800, len(data))
					if _, err := w.Write(data[off:end]); err != nil {
						return
					}
					w.(http.Flusher).Flush()
					off = end
					target := time.Duration(float64(off) / float64(len(data)) * 8 * float64(time.Second))
					delay := time.Until(started.Add(target))
					if delay > 0 {
						timer := time.NewTimer(delay)
						select {
						case <-r.Context().Done():
							timer.Stop()
							return
						case <-timer.C:
						}
					}
				}
				<-r.Context().Done()
			}))
			defer up.Close()
			m, err := transcode.New(config.Transcode{FFmpegPath: os.Getenv("ETHER_IPTV_FFMPEG"), FFprobePath: os.Getenv("ETHER_IPTV_FFPROBE"), TempDir: filepath.Join(t.TempDir(), "hls")}, slog.New(slog.NewTextHandler(io.Discard, nil)), transcode.WithoutHWDetection())
			if err != nil {
				t.Fatal(err)
			}
			defer m.Close()
			tc := &measuredLiveTranscoder{Manager: m}
			s := testService(t, Options{HTTPClient: up.Client(), Transcoder: tc})
			c, err := s.SaveChannel(ctx, Channel{Name: "Generated HEVC", Number: "1", Kind: "iptv", Enabled: true, StreamURL: up.URL})
			if err != nil {
				t.Fatal(err)
			}
			caps := core.ClientCaps{Product: profile, MaxHeight: 2160, VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"}}
			if profile == "Jellyfin HEVC" || profile == "Emby HEVC" {
				protocol := core.Protocol("jellyfin")
				if profile == "Emby HEVC" {
					protocol = "emby"
				}
				caps, err = (clientcaps.Builder{}).Build(ctx, clientcaps.DeviceInfo{Protocol: protocol, Product: profile}, clientcaps.DeviceProfile{
					DirectPlayProfiles:  []clientcaps.DirectPlayProfile{{Type: "Video", Container: "mp4,ts", VideoCodec: "h264,hevc", AudioCodec: "aac"}},
					TranscodingProfiles: []clientcaps.TranscodingProfile{{Type: "Video", Protocol: "hls", Container: "mp4", VideoCodec: "h264,hevc", AudioCodec: "aac"}},
				}, nil, "")
				if err != nil {
					t.Fatal(err)
				}
			} else if profile == "H264 viewer" {
				caps.VideoCodecs = []string{"h264"}
			} else {
				platform := "Android"
				if profile == "Plex for Apple TV" {
					platform = "tvOS"
				}
				d := transcode.PlaybackDefaultsFor(profile, platform, "", "", "", nil)
				caps.VideoCodecs = d.Video
				caps.MaxHeight = d.MaxHeight
				caps.HDR10 = d.HDR10
			}
			before := childCPU()
			start := time.Now()
			u := &core.User{ID: 1}
			info, err := s.Tune(ctx, u, c.ID, caps)
			if err != nil {
				t.Fatal(err)
			}
			dec := tc.requests[0].Decision
			if profile != "H264 viewer" && (!dec.VideoCopy || dec.VideoCodec != "hevc" || dec.Container != "fmp4") {
				t.Fatalf("decision=%+v", dec)
			}
			if profile == "H264 viewer" && (dec.VideoCopy || dec.VideoCodec != "h264") {
				t.Fatalf("decision=%+v", dec)
			}
			out := httptest.NewRecorder()
			s.ServeRelaySegment(out, httptest.NewRequest("GET", "/", nil).WithContext(ctx), info.ID, 0, u)
			ttff := time.Since(start)
			if out.Code != 200 || out.Body.Len() == 0 {
				t.Fatalf("media=%d bytes=%d", out.Code, out.Body.Len())
			}
			if dec.VideoCopy {
				init := httptest.NewRecorder()
				s.ServeRelaySegment(init, httptest.NewRequest("GET", "/", nil), info.ID, -1, u)
				if init.Code != 200 || init.Body.Len() == 0 {
					t.Fatal("missing init")
				}
				if ttff >= 4800*time.Millisecond {
					t.Fatalf("copy TTFF=%s", ttff)
				}
			}
			s.Stop(ctx, info.ID, u)
			cpu := childCPU() - before
			if calls.Load() != 1 {
				t.Fatalf("provider opens=%d", calls.Load())
			}
			t.Logf("copy=%v codec=%s container=%s TTFF=%s child_cpu=%s media_bytes=%d", dec.VideoCopy, dec.VideoCodec, dec.Container, ttff, cpu, out.Body.Len())
			if dec.VideoCopy && cpu > ttff/2+250*time.Millisecond {
				t.Fatalf("remux CPU too high: %s over %s", cpu, ttff)
			}
		})
	}
}

func TestIPTVRealReconnectAfterDeliveredMedia(t *testing.T) {
	file := os.Getenv("ETHER_IPTV_HEVC_FIXTURE")
	if file == "" {
		t.Skip("set ETHER_IPTV_HEVC_FIXTURE")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var opens, active atomic.Int32
	var overlap atomic.Bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		opens.Add(1)
		if active.Add(1) != 1 {
			overlap.Store(true)
		}
		defer active.Add(-1)
		w.Header().Set("Content-Type", "video/mp2t")
		started := time.Now()
		for off := 0; off < len(data); {
			end := min(off+18800, len(data))
			if _, err := w.Write(data[off:end]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			off = end
			wait := time.Until(started.Add(time.Duration(float64(off) / float64(len(data)) * 8 * float64(time.Second))))
			if wait > 0 {
				timer := time.NewTimer(wait)
				select {
				case <-r.Context().Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}
	}))
	defer up.Close()
	m, err := transcode.New(config.Transcode{FFmpegPath: os.Getenv("ETHER_IPTV_FFMPEG"), FFprobePath: os.Getenv("ETHER_IPTV_FFPROBE"), TempDir: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)), transcode.WithoutHWDetection())
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	s := testService(t, Options{HTTPClient: up.Client(), Transcoder: m})
	ctx, cancel := context.WithTimeout(t.Context(), 65*time.Second)
	defer cancel()
	_, err = s.ImportSource(ctx, Source{ID: "capped", Name: "Fake account", Playlist: "#EXTM3U\n#EXTINF:-1,Repeating EOF\n" + up.URL + "\n"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := s.sourceEntry(ctx, "capped")
	if err != nil {
		t.Fatal(err)
	}
	entry.limit = 1
	s.authCache.mu.Lock()
	s.authCache.m["capped"] = entry
	s.authCache.mu.Unlock()
	cs, _ := s.Channels(ctx)
	u := &core.User{ID: 1}
	cpuStart := childCPU()
	var firstMedia time.Duration
	start := time.Now()
	info, err := s.Tune(ctx, u, cs[0].ID, core.ClientCaps{VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background(), info.ID, u)
	for n := 0; n < 30; n++ {
		out := httptest.NewRecorder()
		s.ServeRelaySegment(out, httptest.NewRequest("GET", "/", nil).WithContext(ctx), info.ID, n, u)
		if out.Code != 200 {
			t.Fatalf("segment %d failed after %s: %d", n, time.Since(start), out.Code)
		}
		if n == 0 {
			firstMedia = time.Since(start)
		}
		if opens.Load() >= 5 && time.Since(start) > 32*time.Second {
			s.Stop(context.Background(), info.ID, u)
			t.Logf("opens=%d media still available after %s TTFF=%s child_cpu=%s codec=%s", opens.Load(), time.Since(start), firstMedia, childCPU()-cpuStart, info.MediaInfo.Video().Codec)
			if overlap.Load() {
				t.Fatal("overlapping account connections")
			}
			return
		}
	}
	t.Fatal("fixture did not exercise enough reconnects")
}
