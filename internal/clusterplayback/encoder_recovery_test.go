package clusterplayback

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/store"
)

type recoveryTestEncoder struct {
	fixtureEncoder
	requests []core.HLSRequest
	ready    chan struct{}
	proceed  chan struct{}
	data     []byte
}

func (e *recoveryTestEncoder) SupportsDistributedRequest(core.HLSRequest) bool { return true }
func (e *recoveryTestEncoder) StartHLSOwned(ctx context.Context, q core.HLSRequest) (core.HLSSession, core.EncoderRetirement, error) {
	if e.ready != nil {
		close(e.ready)
		select {
		case <-e.proceed:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}

	_, token, err := e.fixtureEncoder.StartHLSOwned(ctx, q)
	if err != nil {
		return nil, token, err
	}
	e.mu.Lock()
	e.requests = append(e.requests, cloneAdmissionRequest(q))
	session := e.session
	e.mu.Unlock()
	return &recoveryTestSession{fixtureSession: session, data: e.data}, token, nil
}

type recoveryTestSession struct {
	*fixtureSession
	data []byte
}

func (s *recoveryTestSession) ServeSegment(w http.ResponseWriter, q *http.Request, n int) {
	w.Header().Set("Content-Type", "video/mp2t")
	w.Header().Set("Content-Length", strconv.Itoa(len(s.data)))
	w.Header().Set("X-Segment", strconv.Itoa(n))
	_, _ = w.Write(s.data)
}

// This invariant uses real mTLS RPC, SQLite placement and source gateway input.
// The encoded bytes are a recorded offline TS fixture: it tests ownership and
// complete/unique delivery, not real decoder or audio splice continuity.
func TestEncoderLossRecoveryInvariant(t *testing.T) {
	for _, mode := range []string{"exit", "partition", "newboot", "removed", "clock"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			data, err := os.ReadFile("../playbackbuffer/testdata/cpu.ts")
			if err != nil {
				t.Fatal(err)
			}
			db := store.OpenTest(t)
			v := NewVerifier()
			p, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: 10 * time.Minute, LockTimeout: time.Second}, v)
			if err != nil {
				t.Fatal(err)
			}
			a := &recoveryTestEncoder{data: data}
			b := &recoveryTestEncoder{data: data, ready: make(chan struct{}), proceed: make(chan struct{})}
			manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{}, Transcoder: &a.fixtureEncoder, SelectProfile: func(core.ClientCaps, *core.User) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer manager.Close()
			access, err := playback.NewSourceAccess(manager, 16, 16)
			if err != nil {
				t.Fatal(err)
			}
			ids := []nodewire.Identity{{Cluster: "loss", NodeID: "source", Boot: "source-boot"}, {Cluster: "loss", NodeID: "a", Boot: "a-boot"}, {Cluster: "loss", NodeID: "b", Boot: "b-boot"}, {Cluster: "loss", NodeID: "controller", Boot: "controller-boot"}}
			guards := fixtureGuards(t, ids...)
			endpoints := map[nodewire.Identity]string{}
			var clockFault atomic.Bool
			member := func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }
			eligible := func(_ context.Context, id nodewire.Identity, _ config.NodeRole) error {
				if id == ids[1] && clockFault.Load() {
					return ErrDenied
				}
				return nil
			}
			base := Options{DB: db, Placement: p, Endpoint: func(_ context.Context, id nodewire.Identity) (string, error) { return endpoints[id], nil }, Eligible: eligible, MemberRole: member, Capacity: 16, ClockBoundKnown: true, ClockSkew: time.Millisecond, EncoderRPCDeadline: 50 * time.Millisecond, EncoderFailureGrace: time.Millisecond, EncoderSegmentWait: time.Second, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision { return core.Decision{} }}
			var down atomic.Bool
			runtimes := make([]*Runtime, 4)
			for i := range ids {
				o := base
				o.Self = ids[i]
				o.Guard = guards[i]
				switch i {
				case 0:
					o.Playback = manager
					o.SourceAccess = access
					o.SourcePolicy = func(context.Context, *core.Viewer, *core.PlaySession) error { return nil }
					o.ActiveViewer = func(context.Context, int64) (*core.Viewer, error) { return &core.Viewer{ID: 9}, nil }
				case 1:
					o.ClockReady = func() bool { return !clockFault.Load() }
					o.Encoder = a
				case 2:
					o.Encoder = b
				}
				runtime, err := NewRuntime(o)
				if err != nil {
					t.Fatal(err)
				}
				runtimes[i] = runtime
				defer runtime.Close()
				handler := runtime.Handler()
				if i == 1 {
					handler = http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
						if down.Load() {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err == nil {
								_ = conn.Close()
							}
							return
						}
						runtime.Handler().ServeHTTP(w, q)
					})
				}
				server := httptest.NewUnstartedServer(handler)
				server.TLS = guards[i].ServerTLS()
				server.StartTLS()
				defer server.Close()
				endpoints[ids[i]] = server.URL
			}
			front := runtimes[3]
			if err = v.Bind(front); err != nil {
				t.Fatal(err)
			}
			for _, id := range ids[1:3] {
				scope := id.NodeID + "/streams"
				if err = p.SetPool(ctx, scope, config.RoleTranscode, 1); err != nil {
					t.Fatal(err)
				}
				if err = p.SetTarget(ctx, placement.Target{NodeID: id.NodeID, Boot: id.Boot}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {scope}}); err != nil {
					t.Fatal(err)
				}
			}
			prepared, err := front.PrepareSource(ctx, ids[0], PrepareRequest{Operation: "prepare", Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: "loss"}, Protocol: "hls"})
			if err != nil {
				t.Fatal(err)
			}
			q := core.HLSRequest{SessionID: "recover", Info: core.MediaInfo{Duration: time.Minute, Container: "mkv", Streams: []core.StreamInfo{{Type: "video", Codec: "h264", Height: 720}}}, SegmentDuration: 4 * time.Second, Decision: core.Decision{Kind: core.DecisionTranscode, VideoCopy: false, AudioCopy: true, Container: "mpegts", SubtitleStream: -1}}
			session, err := front.StartPreparedHLS(ctx, 9, prepared, q)
			if err != nil {
				t.Fatal(err)
			}
			fetched := map[int]int{}
			fetch := func(n int) {
				t.Helper()
				w := httptest.NewRecorder()
				session.ServeSegment(w, httptest.NewRequest("GET", "https://public/segment", nil).WithContext(ctx), n)
				if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
					t.Fatalf("segment %d: %d %s", n, w.Code, w.Body.String())
				}
				fetched[n]++
			}
			fetch(0)
			fetch(1)
			front.mu.Lock()
			old := *front.executions[q.SessionID]
			front.mu.Unlock()
			if mode == "exit" || mode == "newboot" || mode == "removed" {
				if err = a.session.RetireEncoder(); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "clock" {
				clockFault.Store(true)
				// This is a clock quarantine, with the transport still live.
				// Renewal must refuse, while status and exact stop stay usable.
				if err := runtimes[1].authorizeWorker(ctx, old.route.Fence.Controller.Boot, "renew", remoteIdentity(old.route), nil); err != ErrDenied {
					t.Fatalf("quarantined renewal: %v", err)
				}
				if err := runtimes[1].authorizeWorker(ctx, old.route.Fence.Controller.Boot, "status", remoteIdentity(old.route), nil); err != nil {
					t.Fatalf("quarantined status: %v", err)
				}
			} else {
				down.Store(true)
			}
			_ = front.Tick(ctx)
			time.Sleep(2 * time.Millisecond)
			replacementDone := make(chan error, 1)
			go func() { replacementDone <- front.Tick(ctx) }()
			select {
			case <-b.ready:
			case <-ctx.Done():
				t.Fatal("replacement not started")
			}
			waiting := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				request := httptest.NewRequest("GET", "https://public/wait", nil).WithContext(ctx)
				// Both public forwarding and controller handlers capture the old route
				// before waiting. The source runtime supplies a replica with no local
				// execution map, so successor authorization must use durable history.
				forwarded := runtimes[0].fencedPublicDelivery(w, request, old.route)
				defer forwarded.Close()
				controller := front.fencedPublicDelivery(forwarded, request, old.route)
				defer controller.Close()
				session.ServeSegment(controller, request, 2)
				waiting <- w
			}()
			select {
			case <-waiting:
				t.Fatal("segment request failed before replacement ready")
			case <-time.After(10 * time.Millisecond):
			}
			close(b.proceed)
			if err = <-replacementDone; err != nil {
				t.Fatal("replacement", err)
			}
			response := <-waiting
			if response.Code != 200 || !bytes.Equal(response.Body.Bytes(), data) {
				t.Fatalf("waiting segment: %d", response.Code)
			}
			fetched[2]++
			// Explicitly retry a retained historical segment after the generation
			// switch: its original bytes are valid under current delivery authority.
			historical := httptest.NewRecorder()
			session.ServeSegment(historical, httptest.NewRequest("GET", "https://public/history", nil).WithContext(ctx), 0)
			if historical.Code != 200 || !bytes.Equal(historical.Body.Bytes(), data) {
				t.Fatalf("historical segment after recovery: %d", historical.Code)
			}

			record, err := p.Lookup(ctx, q.SessionID)
			if err != nil || record.Generation != 2 || record.Target.NodeID != "b" {
				t.Fatalf("replacement: %+v %v", record, err)
			}
			if len(b.requests) != 1 || b.requests[0].Resume == nil || *b.requests[0].Resume != (core.HLSResume{FirstSegment: 2, Generation: 2}) || !reflect.DeepEqual(b.requests[0].Decision, q.Decision) {
				t.Fatalf("immutable plan/resume: %+v", b.requests)
			}
			gate, err := front.readOperationGate(ctx, operationKey{ids[0].Cluster, q.SessionID, 1})
			if err != nil || gate.State != gateSealedClaimed || gate.Gate.Arm == nil {
				t.Fatalf("sealed armed gate: %+v %v", gate, err)
			}
			// Original capacity is still charged while its process cannot be checked.
			var charged int
			if err = db.QueryRow(`SELECT COUNT(*) FROM placement_displaced_claims`).Scan(&charged); err != nil || charged != 1 {
				t.Fatalf("lost charge: %d %v", charged, err)
			}
			for n := 3; n < 6; n++ {
				fetch(n)
			}
			events, eventErr := front.RecoveryEvents(ctx, 256)
			if eventErr != nil || len(events) != 1 || events[0].Status != "recovered" || events[0].FailedNodeID != "a" || events[0].ReplacementNodeID != "b" || events[0].InterruptionMS == nil || events[0].ClientStalls != nil {
				t.Fatalf("events: %+v %v", events, eventErr)
			}
			if mode == "newboot" {
				if err = p.SetTarget(ctx, placement.Target{NodeID: "a", Boot: "a-new-boot"}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"a/streams"}}); err != nil {
					t.Fatal(err)
				}
				// Reconciliation uses durable displaced history even if no local
				// old-session pointer remains after controller restart.
				front.mu.Lock()
				failed := front.executions[q.SessionID].failed
				front.executions[q.SessionID].failed = nil
				front.mu.Unlock()
				if err = front.reconcileBootRetirements(ctx); err != nil {
					t.Fatal(err)
				}
				front.mu.Lock()
				front.executions[q.SessionID].failed = failed
				front.mu.Unlock()
				if err = db.QueryRow(`SELECT COUNT(*) FROM placement_displaced_claims`).Scan(&charged); err != nil || charged != 0 {
					t.Fatal("new boot leaked capacity", charged, err)
				}
			}
			if mode == "removed" {
				if err = p.RemoveTarget(ctx, old.route.Fence.Target, "offline-test/operator-fenced-exact-boot"); err != nil {
					t.Fatal(err)
				}
				if err = db.QueryRow(`SELECT COUNT(*) FROM placement_displaced_claims`).Scan(&charged); err != nil || charged != 0 {
					t.Fatal("removed node leaked capacity", charged, err)
				}
				if err = p.SetTarget(ctx, old.route.Fence.Target, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"a/streams"}}); err != placement.ErrFenced {
					t.Fatal("removed boot readmitted", err)
				}
			}
			down.Store(false)
			late := httptest.NewRecorder()
			old.session.ServeSegment(late, httptest.NewRequest("GET", "https://public/late", nil).WithContext(ctx), 2)
			if late.Code == 200 {
				t.Fatal("old generation output accepted")
			}
			// Exact old-worker stop/retirement releases only its displaced charge.
			for i := 0; i < 30; i++ {
				runtimes[1].Cleanup()
				_ = front.Tick(ctx)
				_ = db.QueryRow(`SELECT COUNT(*) FROM placement_displaced_claims`).Scan(&charged)
				if charged == 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if err = db.QueryRow(`SELECT COUNT(*) FROM placement_displaced_claims`).Scan(&charged); err != nil || charged != 0 {
				t.Fatalf("displaced capacity leak: %d %v", charged, err)
			}
			for n := 0; n < 6; n++ {
				if fetched[n] != 1 {
					t.Fatalf("sequence %d count %d", n, fetched[n])
				}
			}
			_ = session.Close()
			for i := 0; i < 30; i++ {
				_ = front.Tick(ctx)
				runtimes[0].Cleanup()
				runtimes[1].Cleanup()
				runtimes[2].Cleanup()
				record, _ = p.Lookup(ctx, q.SessionID)
				if record.Retired {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if !record.Retired || a.session.EncoderErr() != nil || b.session.EncoderErr() != nil {
				t.Fatal("encoder retirement leak")
			}
			if err = db.QueryRow(`SELECT COUNT(*) FROM placement_claims`).Scan(&charged); err != nil || charged != 0 {
				t.Fatalf("current capacity leak: %d %v", charged, err)
			}
			front.mu.Lock()
			done := front.executions[q.SessionID].done
			front.mu.Unlock()
			select {
			case <-done:
			default:
				t.Fatal("session slot leaked")
			}
			if err = front.ReleaseOutput(ctx, q.SessionID, record.Fence); err != nil {
				t.Fatal("output leak", err)
			}
		})
	}
}
