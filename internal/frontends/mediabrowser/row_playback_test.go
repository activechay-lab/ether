package mediabrowser

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

// The bundled playbackmanager posts this shape for a Home card without a
// detail MediaSourceId. A successful start must survive an advisory request;
// failures at every preparation boundary must reach its ErrorCode handler.
func TestJellyfinRowPlaybackLifecycle(t *testing.T) {
	for _, stage := range []string{"success", "plan", "open", "stream"} {
		t.Run(stage, func(t *testing.T) {
			opened, stopped, starts := 0, 0, 0
			sessions := frontendtest.Sessions{
				PlanFunc: func(_ context.Context, req frontend.PlanRequest) (frontend.PlayPlan, error) {
					if req.Version != "" || req.DetailSource != "" {
						t.Fatal("quick play unexpectedly pinned a detail source")
					}
					if !req.Intent.Estimate && stage == "plan" {
						return frontend.PlayPlan{}, errors.New("preparation failed")
					}
					return frontend.PlayPlan{ID: "plan", Estimated: req.Intent.Estimate, Media: frontend.MediaInfo{Container: "mkv"}, Decision: frontend.Decision{Kind: "copy", VideoCopy: true, AudioCopy: true}, DirectStream: true, HLS: true, HLSContainer: "fmp4"}, nil
				},
				OpenFunc: func(context.Context, frontend.OpenRequest) (frontend.Session, error) {
					if stage == "open" {
						return frontend.Session{}, frontend.ErrNotAvailable
					}
					opened++
					return frontend.Session{ID: "session"}, nil
				},
				StartStreamFunc: func(_ context.Context, _ frontend.SessionAccess, _ string, req frontend.StreamRequest) (string, error) {
					starts++
					if req.Format != frontend.StreamHLS {
						t.Fatalf("format=%s", req.Format)
					}
					if stage == "stream" {
						return "", frontend.ErrNotAvailable
					}
					return "/sessions/media/session/grant/master.m3u8", nil
				},
				StopFunc: func(context.Context, frontend.SessionAccess, string) error { stopped++; return nil },
			}
			inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Sessions: sessions, Capabilities: frontendtest.Capabilities{}, Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 1}, nil }}})
			if err != nil {
				t.Fatal(err)
			}
			call := func(play string) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodPost, "/Items/"+ids.RatingKeyGUID(5)+"/PlaybackInfo", strings.NewReader(`{"UserId":"viewer","StartTimeTicks":0,"IsPlayback":`+play+`,"AutoOpenLiveStream":`+play+`,"DeviceProfile":{"DirectPlayProfiles":[{"Type":"Video","Container":"mp4","VideoCodec":"h264","AudioCodec":"aac"}],"TranscodingProfiles":[{"Type":"Video","Protocol":"hls","Container":"mp4","VideoCodec":"h264","AudioCodec":"aac"}]}}`))
				r.Header.Set("X-Emby-Token", "grant")
				w := httptest.NewRecorder()
				inst.Handler().ServeHTTP(w, r)
				return w
			}
			w := call("true")
			if w.Code != 200 {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			if stage != "success" {
				if !strings.Contains(w.Body.String(), `"ErrorCode":"NoCompatibleStream"`) {
					t.Fatal(w.Body.String())
				}
				if stage == "stream" && stopped != 1 {
					t.Fatal("failed stream retained session")
				}
				return
			}
			if !strings.Contains(w.Body.String(), "master.m3u8") || opened != 1 || starts != 1 || stopped != 0 {
				t.Fatal(w.Body.String(), opened, starts, stopped)
			}
			w = call("false")
			if w.Code != 200 || opened != 1 || starts != 1 || stopped != 0 {
				t.Fatal("advisory request disturbed active playback", w.Code, opened, starts, stopped)
			}
		})
	}
}
