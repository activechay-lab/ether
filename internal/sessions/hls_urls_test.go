package sessions

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontends/mediabrowser"
)

// Model the remote adapter's optional interface: it implements the ladder even
// when the worker advertises no ABR rungs. Rung calls must never reach that worker.
type playlistURLLadder struct {
	*fakeMediaSession
	rungs []core.HLSRung
}

func (s *playlistURLLadder) Rungs() []core.HLSRung { return s.rungs }
func (s *playlistURLLadder) ServeRungPlaylist(w http.ResponseWriter, r *http.Request, id string) {
	for _, rung := range s.rungs {
		if rung.ID == id {
			s.ServePlaylist(w, r)
			return
		}
	}
	http.Error(w, "unadvertised rung operation", 422)
}
func (s *playlistURLLadder) ServeRungSegment(w http.ResponseWriter, r *http.Request, id string, n int) {
	for _, rung := range s.rungs {
		if rung.ID == id {
			s.ServeSegment(w, r, n)
			return
		}
	}
	http.Error(w, "unadvertised rung operation", 422)
}

type playlistURLDistributed struct{ session *playlistURLLadder }

func (*playlistURLDistributed) Supports(core.HLSRequest) bool { return true }
func (d *playlistURLDistributed) Allocated(id string) bool    { return d.session.id == id }
func (d *playlistURLDistributed) StartDistributedHLS(_ context.Context, _ int64, _, _ string, r core.HLSRequest) (core.HLSSession, error) {
	d.session.id = r.SessionID
	return d.session, nil
}
func (d *playlistURLDistributed) Session(id string) (core.HLSSession, bool) {
	return d.session, d.Allocated(id)
}
func (*playlistURLDistributed) Stop(string) {}

type playlistURLLive struct{ fakeLive }

func (*playlistURLLive) Serve(w http.ResponseWriter, r *http.Request, _ *core.Viewer, _, tail, base string) {
	switch tail {
	case "index.m3u8":
		fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1000000\n"+base+"media.m3u8\n")
	case "media.m3u8":
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:4,\n"+base+"00000.ts\n")
	case "00000.ts":
		fmt.Fprint(w, "segment-0")
	default:
		http.NotFound(w, r)
	}
}

// Follow authored URLs through the real mounted frontend and broker, including
// relative resolution at each playlist level. Backends are offline fakes; this
// proves route/dispatch parity, not encoder transport or decoded playback.
func TestMediaBrowserAuthoredHLSURLs(t *testing.T) {
	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		for _, kind := range []string{"local-vod", "local-live", "distributed-vod-no-abr", "distributed-vod-one-rung", "distributed-vod-abr"} {
			t.Run(string(protocol)+"/"+kind, func(t *testing.T) {
				ctx := context.Background()
				active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
				events := NewEventBus(nil, active)
				defer events.Close()
				deps := BrokerDeps{
					Playback:   &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 2},
					Transcoder: &fakeMediaTranscoder{sessions: map[string]*fakeMediaSession{}},
					Live:       &playlistURLLive{}, Events: events, ActiveViewer: active, Progress: &fakeProgress{},
					Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
						return &core.Title{Key: key}, nil
					},
					CanPlay:      func(*core.Viewer, bool, time.Time) bool { return true },
					BitrateLimit: func(*core.Viewer) int { return 0 },
					OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
				}
				if strings.HasPrefix(kind, "distributed") {
					ladder := &playlistURLLadder{fakeMediaSession: &fakeMediaSession{}}
					if kind != "distributed-vod-no-abr" {
						ladder.rungs = []core.HLSRung{{ID: "base", Bandwidth: 4000000}}
					}
					if kind == "distributed-vod-abr" {
						ladder.rungs = append(ladder.rungs, core.HLSRung{ID: "720p", Bandwidth: 2000000})
					}
					deps.DistributedHLS = &playlistURLDistributed{session: ladder}
				}
				b, err := NewBroker(deps)
				if err != nil {
					t.Fatal(err)
				}
				defer b.Close(ctx)
				viewer, device := &core.Viewer{ID: 2}, core.Device{ID: "tv"}
				req := frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: protocol, Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true}}}
				if kind == "local-live" {
					req.Item, req.Channel = 0, "channel"
				}
				plan, err := b.Plan(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				session, err := b.Open(ctx, frontend.OpenRequest{Viewer: viewer, Device: device, Protocol: protocol, PlanID: plan.ID})
				if err != nil {
					t.Fatal(err)
				}
				path, err := b.StartStream(ctx, frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: protocol}, session.ID, frontend.StreamRequest{Format: frontend.StreamHLS})
				if err != nil {
					t.Fatal(err)
				}
				var front frontend.Frontend = mediabrowser.Jellyfin{MachineID: "test"}
				if protocol == "emby" {
					front = mediabrowser.Emby{MachineID: "test"}
				}
				inst, err := front.Mount(ctx, frontend.Host{Sessions: BindSurface(b, protocol), Identity: frontendtest.Identity{}})
				if err != nil {
					t.Fatal(err)
				}
				for _, prefix := range []string{"", "/" + string(protocol)} {
					seen := map[string]bool{}
					segments := 0
					var follow func(string)
					follow = func(path string) {
						t.Helper()
						if seen[path] {
							t.Fatalf("playlist cycle at %s", path)
						}
						seen[path] = true
						w := httptest.NewRecorder()
						inst.Handler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
						if w.Code != http.StatusOK {
							t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
						}
						if !strings.HasSuffix(path, ".m3u8") {
							segments++
							if w.Body.String() != "segment-0" {
								t.Fatalf("wrong segment %q", w.Body.String())
							}
							return
						}
						parent, err := url.Parse(path)
						if err != nil {
							t.Fatal(err)
						}
						children := 0
						for _, line := range strings.Split(w.Body.String(), "\n") {
							line = strings.TrimSpace(line)
							if line == "" || strings.HasPrefix(line, "#") {
								continue
							}
							child, err := url.Parse(line)
							if err != nil {
								t.Fatal(err)
							}
							follow(parent.ResolveReference(child).String())
							children++
						}
						if children == 0 {
							t.Fatalf("empty playlist %s", path)
						}
					}
					follow(prefix + path)
					want := 1
					if kind == "distributed-vod-abr" {
						want = 2
					}
					if segments != want {
						t.Fatalf("got %d segments, want %d", segments, want)
					}
				}
			})
		}
	}
}
