package clusterplayback

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/clusterbus"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/sessions"
)

type neutralRestoreEncoder struct {
	fixtureEncoder
	starts atomic.Int64
}

func (e *neutralRestoreEncoder) StartHLSOwned(ctx context.Context, q core.HLSRequest) (core.HLSSession, core.EncoderRetirement, error) {
	_, token, err := e.fixtureEncoder.StartHLSOwned(ctx, q)
	if err != nil {
		return nil, token, err
	}
	e.starts.Add(1)
	return &neutralRestoreSession{e.session}, token, nil
}

type neutralRestoreSession struct{ *fixtureSession }

func (s *neutralRestoreSession) ServeSegment(w http.ResponseWriter, r *http.Request, n int) {
	body := fmt.Sprintf("segment-%d", n)
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	_, _ = w.Write([]byte(body))
}

// The invariant exercises the actual neutral broker, sealed shared SQL state,
// cluster registry election, source gateway, mTLS worker attach and epoch CAS.
// Synthetic segment bodies prove request continuity, not codec continuity.
func TestNeutralControllerLossKeepsHealthyEncoder(t *testing.T) { testNeutralControls(t, true) }

func TestNeutralLiveCoordinatorControls(t *testing.T) { testNeutralControls(t, false) }

func testNeutralControls(t *testing.T, loseOwner bool) {
	for _, protocol := range []frontend.Protocol{"jellyfin", "emby"} {
		t.Run(string(protocol), func(t *testing.T) {
			fixtureStarted := time.Now()
			db := openFixtureDB(t)
			ctx, cancel := context.WithTimeout(context.Background(), fixtureExecutionBudget(fixtureStarted, 20*time.Second))
			defer cancel()
			hub := clusterbus.NewHub()
			sealer, err := config.Open(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer sealer.Close()
			verifier := NewVerifier()
			ledger, err := placement.New(db, placement.Options{MaxSessions: 32, MaxLease: 10 * time.Minute, LockTimeout: fixtureSQLBudget()}, verifier)
			if err != nil {
				t.Fatal(err)
			}
			encoder := &neutralRestoreEncoder{}
			manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{}, Transcoder: &encoder.fixtureEncoder, SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			sourceAccess, err := playback.NewSourceAccess(manager, 32, 32)
			if err != nil {
				t.Fatal(err)
			}
			ids := []nodewire.Identity{{Cluster: "neutral", NodeID: "source", Boot: "source-boot"}, {Cluster: "neutral", NodeID: "worker", Boot: "worker-boot"}, {Cluster: "neutral", NodeID: "owner", Boot: "owner-boot"}, {Cluster: "neutral", NodeID: "survivor", Boot: "survivor-boot"}, {Cluster: "neutral", NodeID: "delivery", Boot: "delivery-boot"}}
			guards := fixtureGuards(t, ids...)
			endpoints := map[nodewire.Identity]string{}
			var gone atomic.Bool
			active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
			eligible := func(_ context.Context, id nodewire.Identity, _ config.NodeRole) error {
				if id == ids[2] && gone.Load() {
					return ErrDenied
				}
				return nil
			}
			base := Options{SurfacePolicy: testSessionPolicy, Delivery: testDeliveryPolicy(), MediaServers: func(context.Context) ([]nodewire.Identity, error) { return []nodewire.Identity{ids[2], ids[3]}, nil }, DB: db, Placement: ledger, ProtocolSealer: sealer, Endpoint: func(_ context.Context, id nodewire.Identity) (string, error) { return endpoints[id], nil }, Eligible: eligible, MemberRole: eligible, Capacity: 32, ClockBoundKnown: true, ClockSkew: time.Millisecond, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision {
				return core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mpegts", AudioStream: -1, SubtitleStream: -1}
			}}
			runtimes := make([]*Runtime, len(ids))
			var privateOwner *httptest.Server
			for i := range ids {
				o := base
				o.Self = ids[i]
				o.Guard = guards[i]
				switch i {
				case 0:
					o.Roles = config.NodeRoles{config.RoleSource}
					o.Playback = manager
					o.SourceAccess = sourceAccess
					o.ActiveViewer = active
					o.SourcePolicy = func(context.Context, *core.Viewer, *core.PlaySession) error { return nil }
				case 1:
					o.Roles = config.NodeRoles{config.RoleTranscode}
					o.Encoder = encoder
				case 4:
					o.Roles = config.NodeRoles{config.RoleDelivery}
				default:
					o.Roles = config.NodeRoles{config.RoleServer, config.RoleDelivery}
				}
				// Production uses one placement verifier bound to each server,
				// rather than the shared fixture's original-controller verifier.
				var survivorVerifier *Verifier
				if i == 3 {
					survivorVerifier = NewVerifier()
					o.Placement, err = placement.New(db, placement.Options{MaxSessions: 32, MaxLease: 10 * time.Minute, LockTimeout: fixtureSQLBudget()}, survivorVerifier)
					if err != nil {
						t.Fatal(err)
					}
				}
				runtime, err := NewRuntime(o)
				if err != nil {
					t.Fatal(err)
				}
				if survivorVerifier != nil {
					if err = survivorVerifier.Bind(runtime); err != nil {
						t.Fatal(err)
					}
				}
				runtimes[i] = runtime
				defer runtime.Close()
				server := httptest.NewUnstartedServer(runtime.Handler())
				server.TLS = guards[i].ServerTLS()
				server.StartTLS()
				defer server.Close()
				endpoints[ids[i]] = server.URL
				if i == 2 {
					privateOwner = server
				}
			}
			if err = verifier.Bind(runtimes[2]); err != nil {
				t.Fatal(err)
			}
			if err = ledger.SetPool(ctx, "worker/streams", config.RoleTranscode, 1); err != nil {
				t.Fatal(err)
			}
			if err = ledger.SetTarget(ctx, placement.Target{NodeID: ids[1].NodeID, Boot: ids[1].Boot}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"worker/streams"}}); err != nil {
				t.Fatal(err)
			}
			var progress atomic.Int64
			brokers := make([]*sessions.Broker, 2)
			for i := 0; i < 2; i++ {
				runtime := runtimes[i+2]
				remote, err := NewRemotePlayback(runtime, RemotePlaybackOptions{Estimate: func(context.Context, *core.Title, core.ClientCaps) core.MediaInfo { return core.MediaInfo{} }, SelectSource: func(context.Context) (nodewire.Identity, error) { return ids[0], nil }, Capacity: 32})
				if err != nil {
					t.Fatal(err)
				}
				tc, err := runtime.DecisionTranscoder()
				if err != nil {
					t.Fatal(err)
				}
				node := hub.Node(ids[i+2].NodeID)
				events := sessions.NewEventBus(node, active)
				defer events.Close()
				broker, err := sessions.NewBroker(sessions.BrokerDeps{SurfacePolicy: testSessionPolicy, DB: db, Cluster: node, Playback: remote, Transcoder: tc, DistributedHLS: runtime, Events: events, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, Progress: fixtureProgress{}, OnTimeline: func(_ *core.Viewer, _ core.RatingKey, p time.Duration, _ time.Duration, _ string) {
					progress.Store(int64(p))
				}, Lookup: func(c context.Context, v *core.Viewer, d core.Device, k core.RatingKey) (*core.Title, error) {
					return fixtureCatalog{}.Get(c, k)
				}})
				if err != nil {
					t.Fatal(err)
				}
				brokers[i] = broker
				defer broker.Close(ctx)
			}
			access := frontend.SessionAccess{Viewer: &core.Viewer{ID: 9}, Device: core.Device{ID: "neutral-device"}, Protocol: protocol}
			plan, err := brokers[0].Plan(ctx, frontend.PlanRequest{Viewer: access.Viewer, Device: access.Device, Protocol: protocol, Item: core.MovieKey(1), Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}}})
			if err != nil {
				t.Fatal(err)
			}
			opened, err := brokers[0].Open(ctx, frontend.OpenRequest{Viewer: access.Viewer, Device: access.Device, Protocol: protocol, PlanID: plan.ID})
			if err != nil {
				t.Fatal(err)
			}
			request := frontend.StreamRequest{Format: frontend.StreamHLS}
			path, err := brokers[0].StartStream(ctx, access, opened.ID, request)
			if err != nil {
				t.Fatal(err)
			}
			parts := strings.Split(path, "/")
			grant := parts[4]
			var ciphertext string
			if err = db.QueryRow(`SELECT binding FROM clusterplayback_neutral_bindings WHERE session=?`, opened.ID).Scan(&ciphertext); err != nil || strings.Contains(ciphertext, grant) {
				t.Fatal("snapshot not sealed", err)
			}
			fetched := map[int]int{}
			fetch := func(broker *sessions.Broker, n int) {
				t.Helper()
				w := httptest.NewRecorder()
				broker.ServeMedia(w, httptest.NewRequest("GET", "https://public"+strings.TrimSuffix(path, "master.m3u8")+fmt.Sprintf("%d.ts", n), nil).WithContext(ctx))
				if w.Code != 200 || w.Body.String() != fmt.Sprintf("segment-%d", n) {
					t.Fatalf("segment %d: %d %q", n, w.Code, w.Body.String())
				}
				fetched[n]++
			}
			for n := 0; n < 3; n++ {
				fetch(brokers[0], n)
			}
			before, err := ledger.Lookup(ctx, opened.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !loseOwner {
				wrong := access
				wrong.Device.ID = "wrong-device"
				if err = brokers[1].Stop(ctx, wrong, opened.ID); err != core.ErrUnauthorized {
					t.Fatal("foreign stop", err)
				}
				if err = brokers[1].Report(ctx, access, opened.ID, frontend.Timeline{State: "playing", Position: 7 * time.Second, Sequence: 1}); err != nil || progress.Load() != int64(7*time.Second) {
					t.Fatal("cross-replica progress", err, progress.Load())
				}
				remotePath, streamErr := brokers[1].StartStream(ctx, access, opened.ID, request)
				if streamErr != nil || remotePath != path {
					t.Fatal("cross-replica stream", remotePath, streamErr)
				}
				coordinator, coordErr := runtimes[3].CurrentCoordinator(ctx, opened.ID)
				if coordErr != nil || coordinator.Identity != ids[2] || coordinator.Epoch != 1 || encoder.starts.Load() != 1 || brokers[1].HasBinding(opened.ID) {
					t.Fatal("forwarding moved healthy ownership", coordinator, coordErr)
				}
				commands, subscribeErr := brokers[0].Events(ctx, frontend.EventScope{Viewer: access.Viewer, Protocol: protocol, Device: access.Device, SessionID: opened.ID})
				if subscribeErr != nil {
					t.Fatal(subscribeErr)
				}
				if err = brokers[1].Command(ctx, access.Viewer, opened.ID, frontend.Command{Kind: frontend.CommandSeek, Position: 12 * time.Second}); err != nil {
					t.Fatal("cross-replica seek", err)
				}
				select {
				case event := <-commands:
					if event.Command == nil || event.Command.Kind != frontend.CommandSeek || event.Command.Position != 12*time.Second || event.TargetDeviceID != access.Device.ID {
						t.Fatal("incorrect seek delivery", event)
					}
				case <-ctx.Done():
					t.Fatal("seek was not delivered", ctx.Err())
				}
				if protocol == "jellyfin" {
					if err = brokers[1].Report(ctx, access, opened.ID, frontend.Timeline{State: "stopped", Position: 12 * time.Second, Sequence: 2}); err != nil {
						t.Fatal("cross-replica stopped timeline", err)
					}
				}
				if err = brokers[1].Stop(ctx, access, opened.ID); err != nil {
					t.Fatal("cross-replica stop", err)
				}
				for i := 0; i < 100; i++ {
					_ = runtimes[2].Tick(ctx)
					runtimes[1].Cleanup()
					runtimes[0].Cleanup()
					record, lookupErr := ledger.Lookup(ctx, opened.ID)
					if lookupErr == nil && record.Retired {
						break
					}
					if i == 99 {
						t.Fatal("retirement did not settle", record, lookupErr)
					}
					time.Sleep(time.Millisecond)
				}
				for _, table := range []string{"placement_claims", "placement_displaced_claims", "clusterplayback_neutral_bindings", "plex_sessions"} {
					var count int
					if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
						t.Fatal("stop leaked", table, count, err)
					}
				}
				if brokers[0].HasBinding(opened.ID) || brokers[1].HasBinding(opened.ID) {
					t.Fatal("stopped binding retained")
				}
				t.Log("progress, stream, seek command and Stop reached live coordinator synchronously; unchanged epoch/encoder; exact retirement restored all capacity")
				return
			}
			gone.Store(true)
			hub.Kill(ids[2].NodeID)
			privateOwner.Close()
			lossAt := time.Now()
			// No lease edits or takeover tick: delivery must read the healthy
			// worker through a surviving protocol server while epoch 1 remains.
			sender, err := runtimes[4].NewPublicDelivery(func(context.Context, *http.Request, string) error { return ErrDenied })
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			sender.ServeHTTP(w, httptest.NewRequest("GET", "https://public"+strings.TrimSuffix(path, "master.m3u8")+"3.ts", nil).WithContext(ctx))
			if w.Code != 200 || w.Body.String() != "segment-3" {
				t.Fatalf("during takeover: %d %q", w.Code, w.Body.String())
			}
			fetched[3]++
			coordinator, err := runtimes[3].CurrentCoordinator(ctx, opened.ID)
			if err != nil || coordinator.Epoch != 1 {
				t.Fatal("media waited for takeover", coordinator, err)
			}
			t.Logf("media during epoch 1 after controller death: %s", time.Since(lossAt))
			readCtx := context.WithValue(ctx, workerAuthorityKey{}, workerAuthority{peer: ids[3], readOnly: true})
			if runtimes[1].authorizeWorker(readCtx, before.Fence.Controller.Boot, "stop", remoteIdentity(runtimes[3].mediaSessions[opened.ID].route), nil) == nil {
				t.Fatal("read-only media replica stopped encoder")
			}
			// Persisted credentials must reject wrong keys and expired rows.
			wrongSealer, keyErr := config.Open(t.TempDir())
			if keyErr != nil {
				t.Fatal(keyErr)
			}
			runtimes[3].o.ProtocolSealer = wrongSealer
			if _, loadErr := runtimes[3].LoadNeutralBinding(ctx, opened.ID); loadErr == nil {
				t.Fatal("wrong cluster key decrypted binding")
			}
			runtimes[3].o.ProtocolSealer = sealer
			wrongSealer.Close()
			if _, err = db.Exec(`UPDATE clusterplayback_neutral_bindings SET expires=? WHERE session=?`, time.Now().Add(-time.Second).UnixMilli(), opened.ID); err != nil {
				t.Fatal(err)
			}
			if _, loadErr := runtimes[3].LoadNeutralBinding(ctx, opened.ID); loadErr == nil {
				t.Fatal("expired snapshot restored")
			}
			if _, err = db.Exec(`UPDATE clusterplayback_neutral_bindings SET expires=? WHERE session=?`, time.Now().Add(time.Minute).UnixMilli(), opened.ID); err != nil {
				t.Fatal(err)
			}
			denied := httptest.NewRecorder()
			brokers[1].ServeMedia(denied, httptest.NewRequest("GET", "https://public/sessions/media/"+opened.ID+"/wrong-grant/3.ts", nil).WithContext(ctx))
			if denied.Code == 200 {
				t.Fatal("wrong media grant installed session")
			}
			wrong := access
			wrong.Device.ID = "wrong-device"
			if _, err = brokers[1].StartStream(ctx, wrong, opened.ID, request); err != core.ErrUnauthorized {
				t.Fatal("wrong device restored", err)
			}

			wrong = access
			if protocol == "jellyfin" {
				wrong.Protocol = "emby"
			} else {
				wrong.Protocol = "jellyfin"
			}
			if err = brokers[1].Report(ctx, wrong, opened.ID, frontend.Timeline{State: "playing"}); err != core.ErrUnauthorized {
				t.Fatal("cross-protocol restored", err)
			}
			if err = brokers[1].Report(ctx, access, opened.ID, frontend.Timeline{State: "playing", Position: 7 * time.Second, Sequence: 1}); err != nil || progress.Load() != int64(7*time.Second) {
				t.Fatal("restored progress", err, progress.Load())
			}
			coordinator, err = runtimes[3].CurrentCoordinator(ctx, opened.ID)
			if err != nil || coordinator.Identity != ids[3] || coordinator.Epoch != 2 {
				t.Fatal("takeover", coordinator, err)
			}
			if time.Since(lossAt) > 6*time.Second {
				t.Fatal("takeover exceeded six seconds")
			}
			restoredPath, err := brokers[1].StartStream(ctx, access, opened.ID, request)
			if err != nil || restoredPath != path {
				t.Fatal("playlist path changed", restoredPath, err)
			}
			master := httptest.NewRecorder()
			brokers[1].ServeSessionMedia(master, httptest.NewRequest("GET", "https://public/master", nil).WithContext(ctx), access, opened.ID, "master.m3u8")
			if master.Code != 200 || !strings.Contains(master.Body.String(), "#EXTM3U") {
				t.Fatal("restored master", master.Code, master.Body.String())
			}
			for n := 4; n < 8; n++ {
				fetch(brokers[1], n)
			}
			after, err := ledger.Lookup(ctx, opened.ID)
			if err != nil || after.Fence != before.Fence || encoder.starts.Load() != 1 {
				t.Fatal("healthy encoder restarted", before.Fence, after.Fence, encoder.starts.Load(), err)
			}
			for n := 0; n < 8; n++ {
				if fetched[n] != 1 {
					t.Fatal("incomplete/duplicate sequence", n, fetched[n])
				}
			}
			if err = brokers[0].Report(ctx, access, opened.ID, frontend.Timeline{State: "playing", Position: time.Second}); err == nil || progress.Load() != int64(7*time.Second) {
				t.Fatal("stale controller wrote progress", err)
			}
			var registryOwner string
			if err = db.QueryRow(`SELECT node_id FROM plex_sessions WHERE id=?`, opened.ID).Scan(&registryOwner); err != nil || registryOwner != ids[3].NodeID {
				t.Fatal("registry not adopted", registryOwner, err)
			}
			priorSnapshot, loadErr := runtimes[3].LoadNeutralBinding(ctx, opened.ID)
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			if err = brokers[1].Stop(ctx, access, opened.ID); err != nil {
				t.Fatal(err)
			}
			if err = runtimes[3].SaveNeutralBinding(ctx, opened.ID, string(protocol), 9, access.Device.ID, priorSnapshot); err == nil {
				t.Fatal("stopped credentials resurrected")
			}
			for i := 0; i < 100; i++ {
				_ = runtimes[3].Tick(ctx)
				runtimes[1].Cleanup()
				runtimes[0].Cleanup()
				rec, e := ledger.Lookup(ctx, opened.ID)
				if e == nil && rec.Retired {
					break
				}
				if i == 99 {
					t.Fatal("retirement did not settle", rec, e)
				}
				time.Sleep(time.Millisecond)
			}
			for _, table := range []string{"placement_claims", "placement_displaced_claims"} {
				var count int
				if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
					t.Fatal("capacity retained", table, count, err)
				}
			}
			var rows int
			if err = db.QueryRow(`SELECT COUNT(*) FROM clusterplayback_neutral_bindings WHERE session=?`, opened.ID).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("stopped snapshot retained", rows, err)
			}
		})
	}
}
