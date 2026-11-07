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
	manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{body: body}, Transcoder: encoder, SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
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

	otherManager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{body: body}, Transcoder: encoder, SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
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
	if _, err = db.ExecContext(ctx, `INSERT INTO cluster_nodes(id,incarnation,started_at,last_seen) VALUES(?,?,?,?)`, sourceID.NodeID, sourceID.Boot, time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	var paused, replacementPaused atomic.Bool
	for i, r := range []*Runtime{source, worker, replacement} {
		handler := r.Handler()
		if r == source || r == replacement {
			active := r
			pausedFlag := &paused
			if r == replacement {
				pausedFlag = &replacementPaused
			}
			handler = http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
				if pausedFlag.Load() && q.Method == "GET" && q.URL.Path == nodesource.Path {
					// Deliver a partial private response, then hang until the gateway's
					// stall deadline cancels it. Never close the transport to signal failure.
					active.Handler().ServeHTTP(&hungSourceWriter{ResponseWriter: w, ctx: q.Context()}, q)
					return
				}
				active.Handler().ServeHTTP(w, q)
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
	old := source.sources[sourceKey(initial.Fence, initial.SourceGeneration)].grant
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
	raw, _, err := input.GatewayInput(remoteIdentity(initial), source.sources[sourceKey(initial.Fence, initial.SourceGeneration)].handle.Value())
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

	// The original source is healthy again before the replacement hangs. With
	// only two eligible sources the next exact-offset read must revisit its boot
	// under generation 2, without reviving either old credential or binding.
	middleGrant := replacement.sources[sourceKey(moved.Fence, moved.SourceGeneration)].grant
	paused.Store(false)
	replacementPaused.Store(true)
	q, _ = http.NewRequestWithContext(ctx, "GET", raw, nil)
	for k, v := range headers {
		q.Header.Set(k, v)
	}
	q.Header.Set("Range", "bytes=1500-1536")
	response, err = http.DefaultClient.Do(q)
	if err != nil {
		t.Fatal(err)
	}
	received, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != 206 || string(received) != body[1500:1537] {
		t.Fatalf("A->B->A exact-offset input: HTTP %d bytes=%q err=%v", response.StatusCode, received, err)
	}
	returned, err := source.route(ctx, request.SessionID)
	if err != nil || returned.Source != initial.Source || returned.SourceGeneration != 2 || returned.Fence != initial.Fence {
		t.Fatalf("source revisit: %+v err=%v", returned, err)
	}
	after, err = source.CurrentCoordinator(ctx, request.SessionID)
	if err != nil || after.Epoch != before.Epoch || after.Identity != before.Identity || encoder.session != running {
		t.Fatal("source revisit moved healthy encoder/coordinator", after, err)
	}
	if _, err = source.authority.Verify(ctx, nodesource.Peer{NodeID: workerID.NodeID, Boot: workerID.Boot}, old.Credential); err == nil {
		t.Fatal("original grant revived on revisited source boot")
	}
	if _, err = replacement.authority.Verify(ctx, nodesource.Peer{NodeID: workerID.NodeID, Boot: workerID.Boot}, middleGrant.Credential); err == nil {
		t.Fatal("middle source grant accepted after second move")
	}
	prior := source.sourceRoutes(ctx, returned)
	if len(prior) != 2 || prior[0] != initial || prior[1] != moved {
		t.Fatal("source revisit lost exact prior retirement routes", prior)
	}
	originalBinding := source.sources[sourceKey(initial.Fence, initial.SourceGeneration)]
	currentBinding := source.sources[sourceKey(returned.Fence, returned.SourceGeneration)]
	if originalBinding == nil || currentBinding == nil || originalBinding == currentBinding || originalBinding.route != initial || currentBinding.route != returned || len(source.sources) != 2 {
		t.Fatal("source generations aliased their retained bindings")
	}
	replacementPaused.Store(false)
	// A source-generation-0 callback still belongs to Stop even when the same
	// boot now serves generation 2. Do not release capacity until it has joined.
	source.mu.Lock()
	originalBinding.operations++
	source.mu.Unlock()
	source.Stop(request.SessionID)
	worker.Cleanup()
	source.Cleanup()
	replacement.Cleanup()
	_ = source.Tick(ctx)
	record, err := ledger.Lookup(ctx, request.SessionID)
	if err != nil || record.Retired {
		t.Fatal("old source-generation callback lost its capacity responsibility", record, err)
	}
	source.mu.Lock()
	originalBinding.operations--
	source.mu.Unlock()
	var retirementErr error
	for i := 0; i < 100; i++ {
		worker.Cleanup()
		source.Cleanup()
		replacement.Cleanup()
		retirementErr = source.Tick(ctx)
		record, err = ledger.Lookup(ctx, request.SessionID)
		if err == nil && record.Retired {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err != nil || !record.Retired {
		t.Error("source revisit Stop did not retire every source generation", record, err, "tick", retirementErr)
		for _, owner := range []*Runtime{source, replacement} {
			for _, binding := range owner.sources {
				t.Logf("retained source gen=%d play=%s retiring=%v sealed=%v binding=%v issuing=%v operations=%d reads=%d", binding.route.SourceGeneration, binding.playID, binding.retiring, binding.sealed, binding.binding, binding.issuing, binding.operations, owner.bytes.Pending(scopeForRoute(binding.route)))
			}
		}
		t.FailNow()
	}
	for _, owner := range []*Runtime{source, replacement} {
		for _, binding := range owner.sources {
			if !owner.sourceJoined(binding) || owner.bytes.Pending(scopeForRoute(binding.route)) != 0 {
				t.Fatal("source generation still owns callbacks/reads after placement retirement", binding.route)
			}
			select {
			case <-binding.life.Done():
				if binding.life.Err() != nil {
					t.Fatal(binding.life.Err())
				}
			default:
				t.Fatal("source generation lifetime not retired", binding.route)
			}
		}
	}
	t.Log("A->B->A: exact bytes, both old grants fenced, healthy encoder/coordinator unchanged, all three source-generation bindings retired before capacity release")
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
