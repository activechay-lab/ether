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
	"testing"
	"time"
)

func TestSourceMovementKeepsEncoderAndFencesOldGrant(t *testing.T) {
	fixtureStarted := time.Now()
	sourceID := nodewire.Identity{Cluster: "indexed", NodeID: "source", Boot: "source-boot"}
	replacementID := nodewire.Identity{Cluster: "indexed", NodeID: "replacement", Boot: "replacement-boot"}
	workerID := nodewire.Identity{Cluster: "indexed", NodeID: "worker", Boot: "worker-boot"}
	guards := fixtureGuards(t, sourceID, workerID, replacementID)
	db := openFixtureDB(t)
	ctx, cancel := context.WithTimeout(context.Background(), fixtureExecutionBudget(fixtureStarted, 10*time.Second))
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
	for i, r := range []*Runtime{source, worker, replacement} {
		server := httptest.NewUnstartedServer(r.Handler())
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
	reply, err := source.sourceFailover(ctx, workerID, sourceRequest{Old: old})
	if err != nil {
		t.Fatal("controller move", err)
	}
	if reply.Grant.Scope.SourceGeneration != 1 || reply.Grant.Scope.Fence != old.Scope.Fence {
		t.Fatal("execution changed")
	}
	if _, err = source.authority.Verify(ctx, nodesource.Peer{NodeID: workerID.NodeID, Boot: workerID.Boot}, old.Credential); err == nil {
		t.Fatal("stale grant accepted")
	}
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
