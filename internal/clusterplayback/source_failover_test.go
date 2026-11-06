package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSourceHangKeepsSequenceEncoderAndCoordinator(t *testing.T) {
	fixtureStarted := time.Now()
	sourceID := nodewire.Identity{Cluster: "indexed", NodeID: "source", Boot: "source-boot"}
	replacementID := nodewire.Identity{Cluster: "indexed", NodeID: "replacement", Boot: "replacement-boot"}
	workerID := nodewire.Identity{Cluster: "indexed", NodeID: "worker", Boot: "worker-boot"}
	guards := fixtureGuards(t, sourceID, workerID, replacementID)
	db := openFixtureDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), fixtureExecutionBudget(fixtureStarted, 20*time.Second))
	defer cancel()
	verifier := NewVerifier()
	ledger, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: 10 * time.Minute, LockTimeout: fixtureSQLBudget()}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	points := make([]core.KeyframePoint, 360)
	for i := range points {
		points[i] = core.KeyframePoint{Offset: int64(i * 10), Time: time.Duration(i) * 100 * time.Millisecond}
	}
	body := strings.Repeat("0123456789", 400)
	encoder := &fixtureEncoder{body: body, keyframes: append([]core.KeyframePoint(nil), points...)}
	manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{body: body}, Transcoder: encoder, SelectProfile: func(core.ClientCaps, *core.User) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	access, err := playback.NewSourceAccess(manager, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	endpoints := map[nodewire.Identity]string{}
	endpoint := func(_ context.Context, id nodewire.Identity) (string, error) {
		ep, ok := endpoints[id]
		if !ok {
			return "", ErrDenied
		}
		return ep, nil
	}
	eligible := func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }
	caps := remotetranscode.Supported()
	caps.Keyframes = true
	base := Options{DB: db, Placement: ledger, Endpoint: endpoint, Eligible: eligible, MemberRole: eligible, Capacity: 16, ClockBoundKnown: true, ClockSkew: time.Millisecond, WorkerCapabilities: caps, RemoteCapabilities: caps, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision { return core.Decision{} }}
	o := base
	o.Roles = config.NodeRoles{config.RoleServer, config.RoleSource}
	o.Self = sourceID
	o.Guard = guards[0]
	o.Playback = manager
	o.SourceAccess = access
	o.ActiveViewer = func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id}, nil }
	o.SourcePolicy = func(context.Context, *core.User, *core.PlaySession) error { return nil }
	source, err := NewRuntime(o)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if err = verifier.Bind(source); err != nil {
		t.Fatal(err)
	}
	o = base
	o.Self = workerID
	o.Guard = guards[1]
	o.Encoder = encoder
	worker, err := NewRuntime(o)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()

	otherManager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{body: body}, Transcoder: encoder, SelectProfile: func(core.ClientCaps, *core.User) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer otherManager.Close()
	otherAccess, err := playback.NewSourceAccess(otherManager, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	otherOptions := source.o
	otherOptions.Self = replacementID
	otherOptions.Guard = guards[2]
	otherOptions.Playback = otherManager
	otherOptions.SourceAccess = otherAccess
	replacement, err := NewRuntime(otherOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if _, err = db.ExecContext(ctx, `INSERT INTO cluster_nodes(id,incarnation,started_at,last_seen) VALUES(?,?,?,?)`, replacementID.NodeID, replacementID.Boot, time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var paused atomic.Bool
	for i, r := range []*Runtime{source, worker, replacement} {
		handler := r.Handler()
		if r == source {
			handler = http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if paused.Load() && q.Method == "GET" && q.URL.Path == nodesource.Path {
					// Deliver a partial private response, then hang until the gateway's
					// stall deadline cancels it. Never close the transport to signal failure.
					source.Handler().ServeHTTP(&hungSourceWriter{ResponseWriter: w, ctx: q.Context()}, q)
					return
				}
				source.Handler().ServeHTTP(w, q)
			})
		}
		server := httptest.NewUnstartedServer(handler)
		server.TLS = guards[i].ServerTLS()
		server.StartTLS()
		defer server.Close()
		endpoints[r.o.Self] = server.URL
	}
	if err = ledger.SetPool(ctx, "indexed-worker", config.RoleTranscode, 1); err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetTarget(ctx, placement.Target{NodeID: workerID.NodeID, Boot: workerID.Boot}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"indexed-worker"}}); err != nil {
		t.Fatal(err)
	}
	prepare := PrepareRequest{Operation: "indexed-prepare", Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: "indexed-client"}, Protocol: "hls"}
	prepared, err := source.PrepareSource(ctx, sourceID, prepare)
	if err != nil {
		t.Fatal("source DTO lost complete index", err)
	}

	request := core.HLSRequest{SessionID: "source-movement", Info: prepared.Info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1, Enhancement: core.VideoEnhancement{State: "off"}}}
	if _, err = source.StartPreparedHLS(ctx, 9, prepared, request); err != nil {
		t.Fatal(err)
	}
	initial, err := source.route(ctx, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	old := source.sources[sourceKey(initial.Fence)].grant
	running := encoder.session
	before, err := source.CurrentCoordinator(ctx, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	// Match deployment's independent SQL-only lease loop while control work
	// waits beyond a complete coordination lease on the hung source.
	keepCtx, stopKeep := context.WithCancel(ctx)
	keepDone := make(chan struct{})
	go func() {
		defer close(keepDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-keepCtx.Done():
				return
			case <-ticker.C:
				_ = source.MaintainCoordination(keepCtx)
			}
		}
	}()
	defer func() { stopKeep(); <-keepDone }()
	source.control.Lock()
	unlockDone := make(chan struct{})
	go func() {
		defer close(unlockDone)
		defer source.control.Unlock()
		timer := time.NewTimer(3 * time.Second)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
		}
	}()
	defer func() { <-unlockDone }()
	paused.Store(true)
	input := worker.inputs[remoteIdentity(initial)].input
	raw, _, err := input.GatewayInput(remoteIdentity(initial), source.sources[sourceKey(initial.Fence)].handle.Value())
	if err != nil {
		t.Fatal(err)
	}
	headers, err := input.HeadersForInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := http.NewRequestWithContext(ctx, "GET", raw, nil)
	for k, v := range headers {
		q.Header.Set(k, v)
	}
	response, err := http.DefaultClient.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	received, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 200 || string(received) != body {
		t.Fatal("gateway rejected current source generation", response.StatusCode, err)
	}
	after, err := source.CurrentCoordinator(ctx, request.SessionID)
	if err != nil || after.Epoch != before.Epoch || after.Identity != before.Identity {
		t.Fatalf("source hang changed coordinator: before=%+v after=%+v err=%v", before, after, err)
	}
	moved, err := source.route(ctx, request.SessionID)
	if err != nil || moved.SourceGeneration != 1 || moved.Fence != initial.Fence {
		t.Fatalf("source failed to move without encoder change: %+v %v", moved, err)
	}
	if _, err = source.authority.Verify(ctx, nodesource.Peer{NodeID: workerID.NodeID, Boot: workerID.Boot}, old.Credential); err == nil {
		t.Fatal("stale grant accepted")
	}
	// Exercise the actual worker authentication path after the blocked interval.
	if err = source.executions[request.SessionID].session.Reconcile(ctx); err != nil && err != core.ErrEncoderPending {
		t.Fatal("worker authentication after source hang", err)
	}
	for n := 0; n < 4; n++ {
		out := httptest.NewRecorder()
		source.executions[request.SessionID].session.ServeSegment(out, httptest.NewRequest("GET", "/", nil).WithContext(ctx), n)
		if out.Code != 200 || out.Body.String() != "segment" {
			t.Fatalf("client segment %d: HTTP %d %q", n, out.Code, out.Body.String())
		}
	}
	t.Logf("hung partial source: HTTP 200 exact sequence (%d bytes), source generation 0 -> 1, encoder generation %d, coordinator epoch %d unchanged", len(received), moved.Fence.Generation, after.Epoch)
	b := make([]byte, 37)
	if n, err := input.ReadRecoveryAt(ctx, initial.Revision, b, 1500); err != nil || n != len(b) || string(b) != body[1500:1537] {
		t.Fatalf("worker exact-offset recovery n=%d err=%v", n, err)
	}

	out := httptest.NewRecorder()
	if _, err := source.fencedDelivery(out, httptest.NewRequest("GET", "/", nil), initial).Write([]byte("encoder-output")); err != nil || out.Code != 200 {
		t.Fatal("source movement revoked healthy encoder output", out.Code, err)
	}
	if encoder.session != running {
		t.Fatal("encoder restarted")
	}
	t.Log("controller committed source generation 1; stale source fenced; worker recovered exact offset 1500; encoder identity unchanged")
}

type hungSourceWriter struct {
	http.ResponseWriter
	ctx context.Context
}

func (w *hungSourceWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p[:len(p)/2])
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
	<-w.ctx.Done()
	return n, err
}
