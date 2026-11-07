package livetv

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPlaylistHeadersFollowMediaRedirectsOnly(t *testing.T) {
	for _, hls := range []bool{false, true} {
		t.Run(fmt.Sprint("hls=", hls), func(t *testing.T) {
			packet := make([]byte, 188)
			packet[0] = 0x47
			var edgeHits, apiHits atomic.Int32
			var edge *httptest.Server
			edge = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				edgeHits.Add(1)
				if r.Header.Get("User-Agent") != "Channel/1" || r.Header.Get("Referer") != "https://example.invalid/player" {
					t.Error("media identity lost on edge")
				}
				if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("API credentials leaked to edge")
				}
				if r.URL.Path == "/hop" {
					http.Redirect(w, r, edge.URL+"/media", 302)
					return
				}
				w.Header().Set("Content-Type", "video/mp2t")
				w.Write(packet)
			}))
			defer edge.Close()
			var origin *httptest.Server
			origin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fake-account" {
					t.Error("origin credentials missing")
				}
				if r.URL.Path == "/list" {
					apiHits.Add(1)
					if r.Header.Get("User-Agent") != "API/1" || r.Header.Get("Referer") != "" {
						t.Error("channel headers contaminated API")
					}
					fmt.Fprintf(w, "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" http-user-agent=\"Attribute/1\" http-referrer=\"https://example.invalid/attribute\",One\n#EXTVLCOPT:http-user-agent=Channel/1\n#EXTVLCOPT:http-referrer=https://example.invalid/player\n%s/stream\n#EXTINF:-1 tvg-id=\"two\",Two\n%s/other\n", origin.URL, origin.URL)
					return
				}
				if r.Header.Get("User-Agent") != "Channel/1" || r.Header.Get("Referer") != "https://example.invalid/player" {
					w.WriteHeader(403)
					return
				}
				if hls {
					fmt.Fprintf(w, "#EXTM3U\n#EXTINF:4,\n%s/hop\n", edge.URL)
					return
				}
				http.Redirect(w, r, edge.URL+"/hop", 302)
			}))
			defer origin.Close()
			tc := &compatibilityTranscoder{info: core.MediaInfo{Container: "mpegts", Streams: []core.StreamInfo{{Type: "video", Codec: "h264", Width: 1280, Height: 720}, {Index: 1, Type: "audio", Codec: "aac", Default: true, Channels: 2}}}}
			s := testService(t, Options{HTTPClient: origin.Client(), Transcoder: tc})
			defer s.Close()
			ctx := context.Background()
			_, err := s.ImportSource(ctx, Source{ID: "headers", Name: "Lab", PlaylistURL: origin.URL + "/list", Auth: &SourceAuth{Type: "bearer", Token: "fake-account", UserAgent: "API/1"}})
			if err != nil {
				t.Fatal(err)
			}
			cs, _ := s.Channels(ctx)
			var c Channel
			for _, x := range cs {
				if x.GuideID == "one" {
					c = x
				} else if len(x.StreamSources[0].Headers) != 0 {
					t.Fatal("headers leaked into next channel")
				}
			}
			u := &core.User{ID: 1}
			info, err := s.Tune(ctx, u, c.ID, core.ClientCaps{VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Stop(ctx, info.ID, u)
			if hls {
				manifest := httptest.NewRecorder()
				s.ServePlaylist(manifest, httptest.NewRequest("GET", "/", nil), info.ID, u)
				if manifest.Code != 200 {
					t.Fatal(manifest.Code)
				}
				out := httptest.NewRecorder()
				s.ServeRelaySegment(out, httptest.NewRequest("GET", "/", nil), info.ID, 0, u)
				if out.Code != 200 || out.Body.Len() != 188 {
					t.Fatalf("HLS media %d %d", out.Code, out.Body.Len())
				}
			} else {
				input, err := s.relayInput(s.sessions[info.ID])
				if err != nil {
					t.Fatal(err)
				}
				res, err := http.Get(input)
				if err != nil {
					t.Fatal(err)
				}
				b, err := io.ReadAll(res.Body)
				res.Body.Close()
				if err != nil || res.StatusCode != 200 || len(b) != 188 {
					t.Fatalf("TS media %d bytes %d %v", res.StatusCode, len(b), err)
				}
			}
			if apiHits.Load() != 1 || edgeHits.Load() < 2 {
				t.Fatalf("API=%d edge=%d", apiHits.Load(), edgeHits.Load())
			}
		})
	}
}

func TestPlaylistHeaderAttributesRefresh(t *testing.T) {
	parse := func(ua string) Channel {
		cs, err := ParseM3U(strings.NewReader("#EXTM3U\n#EXTINF:-1 tvg-id=\"one\" user-agent=\""+ua+"\" referrer=\"https://example.invalid/ref\",One\nhttp://example.invalid/media\n"), "lab")
		if err != nil {
			t.Fatal(err)
		}
		return cs[0]
	}
	old, next := parse("First/1"), parse("Second/1")
	old.StreamSources = append(old.StreamSources, StreamSource{ID: "backup", URL: "http://backup.invalid", Headers: map[string]string{"User-Agent": "Backup/1"}})
	got := mergeImportedStreams(next, old)
	if got[0].Headers["User-Agent"] != "Second/1" || got[0].Headers["Referer"] != "https://example.invalid/ref" || got[1].Headers["User-Agent"] != "Backup/1" {
		t.Fatalf("refresh headers=%+v", got)
	}
	var h map[string]string
	playlistMediaHeader(&h, "http-user-agent", "bad\r\nInjected: yes")
	if len(h) != 0 {
		t.Fatal("header injection accepted")
	}
}
