package clusterplayback

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/authhdr"
	"github.com/braedonsaunders/ether/internal/frontends/mediabrowser"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/sessions"
)

type deliveryMediaEncoder struct{ fixtureEncoder }

func (*deliveryMediaEncoder) Decide(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision {
	return core.Decision{Kind: core.DecisionRemux, Container: "mpegts", VideoCopy: true, AudioCopy: true, AudioStream: -1, SubtitleStream: -1}
}
func (*deliveryMediaEncoder) HWAccel() core.HWAccel { return core.HWAccel{} }
func (e *deliveryMediaEncoder) StartHLSOwned(ctx context.Context, q core.HLSRequest) (core.HLSSession, core.EncoderRetirement, error) {
	s, life, err := e.fixtureEncoder.StartHLSOwned(ctx, q)
	if err != nil {
		return nil, life, err
	}
	return deliveryPlaylistSession{s.(*fixtureSession)}, life, nil
}
func (e *deliveryMediaEncoder) Session(id string) (core.HLSSession, bool) {
	if e.session == nil || e.session.id != id {
		return nil, false
	}
	return deliveryPlaylistSession{e.session}, true
}

type deliveryPlaylistSession struct{ *fixtureSession }

func (s deliveryPlaylistSession) ServePlaylist(w http.ResponseWriter, r *http.Request) {
	const body = "#EXTM3U\n#EXT-X-TARGETDURATION:4\n#EXTINF:4,\n0.ts\n"
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method != "HEAD" {
		_, _ = io.WriteString(w, body)
	}
}

// The actual mounted frontends and broker own grants/policy; only the encoder
// and source bytes are offline doubles. Both senders start without session data.
func TestMediaBrowserDeliveryReplaysOriginalFrontendAndFencesLoss(t *testing.T) {
	fixtureStarted := time.Now()
	ids := []nodewire.Identity{{Cluster: "mb-delivery", NodeID: "owner", Boot: "owner-boot"}, {Cluster: "mb-delivery", NodeID: "delivery-a", Boot: "delivery-a-boot"}, {Cluster: "mb-delivery", NodeID: "delivery-b", Boot: "delivery-b-boot"}}
	guards := fixtureGuards(t, ids...)
	db := openFixtureDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), fixtureExecutionBudget(fixtureStarted, 20*time.Second))
	defer cancel()
	ledger, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: 2 * time.Minute, LockTimeout: fixtureSQLBudget()}, outputRetirementVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	endpoints := map[nodewire.Identity]string{}
	eligible := func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }
	base := Options{DB: db, Placement: ledger, Endpoint: func(_ context.Context, id nodewire.Identity) (string, error) { return endpoints[id], nil }, Eligible: eligible, MemberRole: eligible, Capacity: 16, ClockBoundKnown: true}
	var runtimes []*Runtime
	var senders []http.Handler
	for n, id := range ids {
		options := base
		options.Self = id
		options.Guard = guards[n]
		options.Roles = config.NodeRoles{config.RoleDelivery}
		if n == 0 {
			options.Roles = config.NodeRoles{config.RoleServer}
		}
		runtime, err := NewRuntime(options)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		runtimes = append(runtimes, runtime)
		if n > 0 {
			handler, err := runtime.NewPublicDelivery(func(context.Context, *http.Request, string) error { return ErrDenied })
			if err != nil {
				t.Fatal(err)
			}
			senders = append(senders, handler)
		}
	}
	owner := runtimes[0]
	private := httptest.NewUnstartedServer(owner.Handler())
	private.TLS = guards[0].ServerTLS()
	private.StartTLS()
	defer private.Close()
	endpoints[ids[0]] = private.URL
	mux := http.NewServeMux()
	if err = owner.BindMediaBrowserDelivery(mux); err != nil {
		t.Fatal(err)
	}
	target := placement.Target{NodeID: "encoder", Boot: "encoder-boot"}
	if err = ledger.SetPool(ctx, "mb-output", config.RoleTranscode, 2); err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetTarget(ctx, target, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"mb-output"}}); err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		t.Run(string(protocol), func(t *testing.T) {
			encoder := &deliveryMediaEncoder{}
			manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{}, Transcoder: encoder, SelectProfile: func(core.ClientCaps, *core.User) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			auth := &composedAuth{}
			events := sessions.NewEventBus(nil, auth.ActiveUser)
			defer events.Close()
			var hidden atomic.Bool
			broker, err := sessions.NewBroker(sessions.BrokerDeps{Playback: manager, Transcoder: encoder, Events: events, ActiveViewer: auth.ActiveUser, CanPlay: func(*core.User, bool, time.Time) bool { return true }, BitrateLimit: func(*core.User) int { return 0 }, Progress: composedState{}, OnTimeline: func(*core.User, core.RatingKey, time.Duration, time.Duration, string) {}, Lookup: func(c context.Context, u *core.User, d core.Device, k core.RatingKey) (*core.Title, error) {
				if hidden.Load() {
					return nil, core.ErrUnauthorized
				}
				return fixtureCatalog{}.Get(c, k)
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer broker.Close(ctx)
			viewer, _ := auth.ActiveUser(ctx, 9)
			device := core.Device{ID: "mb-device"}
			plan, err := broker.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: device, Protocol: protocol, Item: core.MovieKey(1), Caps: core.ClientCaps{ClientID: device.ID, Protocol: core.Protocol(protocol)}, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectStream: true, AudioStream: -1, SubtitleStream: -1}}})
			if err != nil {
				t.Fatal(err)
			}
			opened, err := broker.Open(ctx, frontend.OpenRequest{Protocol: protocol, PlanID: plan.ID, Viewer: viewer, Device: device})
			if err != nil {
				t.Fatal(err)
			}
			access := frontend.SessionAccess{Viewer: viewer, Device: device, Protocol: protocol}
			master, err := broker.StartStream(ctx, access, opened.ID, frontend.StreamRequest{Format: frontend.StreamHLS})
			if err != nil {
				t.Fatal(err)
			}
			// Full-node local sources publish gen-zero routing before returning
			// their grant URL; the same route can later acquire an encoder fence.
			ps := manager.Sessions()[0]
			localOptions := base
			localOptions.Self = ids[0]
			localOptions.Guard = guards[0]
			localOptions.Roles = config.NodeRoles{config.RoleServer, config.RoleSource}
			localOptions.Playback = manager
			localOptions.ActiveViewer = auth.ActiveUser
			localOptions.SourcePolicy = func(context.Context, *core.User, *core.PlaySession) error { return nil }
			localOptions.SourceAccess, err = playback.NewSourceAccess(manager, 16, 16)
			if err != nil {
				t.Fatal(err)
			}
			localRuntime, err := NewRuntime(localOptions)
			if err != nil {
				t.Fatal(err)
			}
			defer localRuntime.Close()
			if err = localRuntime.BindLocalClientSession(ctx, opened.ID, ps); err != nil {
				t.Fatal(err)
			}
			localRoute, err := owner.route(ctx, opened.ID)
			if err != nil || localRoute.Fence.Generation != 0 || localRoute.Source != ids[0] {
				t.Fatal("missing local direct owner route", err)
			}
			record, err := ledger.Reserve(ctx, placement.Request{Session: opened.ID, Operation: "mb-route", Controller: placement.Controller{ID: ids[0].NodeID, Boot: ids[0].Boot}, Target: target, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: "mb-output", Units: 1}}, Lease: time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			if err = owner.putRoute(ctx, Route{Fence: record.Fence, Source: ids[0], Viewer: 9, PlayID: ps.ID, Revision: core.SourceRevision(ps)}); err != nil {
				t.Fatal(err)
			}
			host := frontend.Host{Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}, Sessions: sessions.BindSurface(broker, protocol)}
			var instance frontend.Instance
			if protocol == "jellyfin" {
				instance, err = (mediabrowser.Jellyfin{MachineID: "offline"}).Mount(ctx, host)
			} else {
				instance, err = (mediabrowser.Emby{MachineID: "offline"}).Mount(ctx, host)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer instance.Close(ctx)
			var wantCredential atomic.Value
			wantCredential.Store("")
			mux.Handle("/"+string(protocol)+"/", http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if got := authhdr.MediaBrowserToken(q); got != wantCredential.Load().(string) {
					http.Error(w, "credential changed", 500)
					return
				}
				instance.Handler().ServeHTTP(w, q)
			}))
			servers := []*httptest.Server{httptest.NewServer(senders[0]), httptest.NewServer(senders[1])}
			for _, server := range servers {
				defer server.Close()
			}
			alias := "/" + string(protocol) + strings.TrimSuffix(master, "master.m3u8")
			variants := []struct{ name, query, header, value string }{{"grant-only", "", "", ""}, {"api_key", "api_key=offline-key", "", ""}, {"ApiKey", "ApiKey=offline-key", "", ""}, {"emby-token", "", "X-Emby-Token", "offline-key"}, {"mediabrowser-token", "", "X-MediaBrowser-Token", "offline-key"}, {"authorization", "", "Authorization", `MediaBrowser Client="test", DeviceId="mb-device", Token="offline-key"`}, {"emby-authorization", "", "X-Emby-Authorization", `Emby Client="test", DeviceId="mb-device", Token="offline-key"`}}
			standalone := httptest.NewServer(mux)
			defer standalone.Close()
			var requests int
			check := func(resource, method, byteRange string, variant int, server *httptest.Server) int {
				t.Helper()
				v := variants[variant]
				credential := "offline-key"
				if variant == 0 {
					credential = ""
				}
				wantCredential.Store(credential)
				path := alias + resource
				if v.query != "" {
					path += "?" + v.query
				}
				directRequest := httptest.NewRequest(method, "http://public.test"+path, nil)
				directRequest.Header.Set("User-Agent", "offline-player")
				directRequest.Header.Set("X-Emby-Device-Id", device.ID)
				if v.header != "" {
					directRequest.Header.Set(v.header, v.value)
				}
				if byteRange != "" {
					directRequest.Header.Set("Range", byteRange)
				}
				original, _ := http.NewRequestWithContext(ctx, method, standalone.URL+path, nil)
				original.Host = "public.test"
				original.Header = directRequest.Header.Clone()
				directResponse, err := standalone.Client().Do(original)
				if err != nil {
					t.Fatal(err)
				}
				directBody, err := io.ReadAll(directResponse.Body)
				directResponse.Body.Close()
				if err != nil {
					t.Fatal(err)
				}

				q, _ := http.NewRequestWithContext(ctx, method, server.URL+path, nil)
				q.Host = "public.test"
				q.Header = directRequest.Header.Clone()
				response, err := server.Client().Do(q)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != directResponse.StatusCode || string(body) != string(directBody) {
					t.Fatalf("%s %s %s: direct/delivery status %d/%d bytes %q/%q", v.name, method, resource, directResponse.StatusCode, response.StatusCode, string(directBody), body)
				}
				for _, key := range []string{"Content-Type", "Content-Length", "Content-Range", "Content-Disposition", "Last-Modified", "Allow", "Vary", "Accept-Ranges", "ETag", "Cache-Control", "Content-Security-Policy", "X-Content-Type-Options", "Access-Control-Allow-Origin", "Access-Control-Allow-Methods", "Access-Control-Allow-Headers"} {
					if response.Header.Get(key) != directResponse.Header.Get(key) {
						t.Fatalf("%s header changed", key)
					}
				}
				if resource == "base/0.ts" && method == "GET" && byteRange != "" {
					expected := 206
					if byteRange == "bytes=999999-" {
						expected = 416
					}
					if response.StatusCode != expected {
						t.Fatalf("range status %d, expected %d", response.StatusCode, expected)
					}
				}
				requests++
				return response.StatusCode
			}
			for v := range variants {
				for _, resource := range []string{"master.m3u8", "base/index.m3u8", "base/0.ts"} {
					for _, method := range []string{"GET", "HEAD", "OPTIONS"} {
						check(resource, method, "", v, servers[requests%2])
					}
				}
				for _, byteRange := range []string{"bytes=0-2", "bytes=999999-"} {
					check("base/0.ts", "GET", byteRange, v, servers[requests%2])
				}
			}
			servers[0].Close()
			check("base/1.ts", "GET", "", 0, servers[1])
			hidden.Store(true)
			if code := check("base/0.ts", "GET", "", 0, servers[1]); code != 404 {
				t.Fatalf("revoked policy %d", code)
			}
			hidden.Store(false)
			if _, err = broker.StartStream(ctx, access, opened.ID, frontend.StreamRequest{Format: frontend.StreamDirect}); err != nil {
				t.Fatal(err)
			}
			for v := range variants {
				for _, method := range []string{"GET", "HEAD"} {
					for _, byteRange := range []string{"", "bytes=0-2", "bytes=999999-"} {
						check("file", method, byteRange, v, servers[1])
					}
				}
			}
			originalAlias := alias
			parts := strings.Split(alias, "/")
			alias = strings.Replace(alias, "/"+parts[5]+"/", "/wrong-grant/", 1)
			if code := check("file", "GET", "", 1, servers[1]); code != 404 {
				t.Fatalf("account token replaced path grant: %d", code)
			}
			alias = originalAlias
			auth.mu.Lock()
			auth.revoked = true
			auth.mu.Unlock()
			if code := check("file", "GET", "", 0, servers[1]); code != 404 {
				t.Fatalf("revoked viewer %d", code)
			}
			auth.mu.Lock()
			auth.revoked = false
			auth.mu.Unlock()
			// With the same viewer/grant, another protocol still has no authority.
			wrong := "emby"
			if protocol == "emby" {
				wrong = "jellyfin"
			}
			q := httptest.NewRequest("GET", "http://public.test"+strings.Replace(alias, string(protocol), wrong, 1)+"base/0.ts", nil)
			out := httptest.NewRecorder()
			senders[1].ServeHTTP(out, q)
			if out.Code != 404 {
				t.Fatalf("cross protocol %d", out.Code)
			}
			if _, err = db.ExecContext(ctx, `UPDATE placement_sessions SET generation=generation+1 WHERE session=?`, opened.ID); err != nil {
				t.Fatal(err)
			}
			fenced := httptest.NewRecorder()
			senders[1].ServeHTTP(fenced, httptest.NewRequest("GET", "http://public.test"+alias+"file", nil))
			if fenced.Code != 409 || strings.Contains(fenced.Body.String(), "segment") {
				t.Fatalf("stale generation %d", fenced.Code)
			}
			t.Logf("%s: %d original/delivery response pairs; two senders; credential forms, Range/HEAD/416, policy and generation fencing", protocol, requests)
		})
	}
}
