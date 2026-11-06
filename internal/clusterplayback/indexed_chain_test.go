package clusterplayback

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/playbackcontinuity"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCompleteIndexedSourceCapabilityStartAndReopen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	sourceID := nodewire.Identity{Cluster: "indexed", NodeID: "source", Boot: "source-boot"}
	workerID := nodewire.Identity{Cluster: "indexed", NodeID: "worker", Boot: "worker-boot"}
	guards := fixtureGuards(t, sourceID, workerID)
	db := store.OpenTest(t)
	verifier := NewVerifier()
	ledger, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: 10 * time.Minute, LockTimeout: time.Second}, verifier)
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
	for i, r := range []*Runtime{source, worker} {
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
	if err != nil || !reflect.DeepEqual(prepared.Info.Keyframes, points) {
		t.Fatal("source DTO lost complete index", err)
	}
	detached := core.CloneMediaInfo(prepared.Info)
	prepared.Info.Keyframes[1].Offset = 1234
	replay, err := source.PrepareSource(ctx, sourceID, prepare)
	if err != nil || !reflect.DeepEqual(replay.Info.Keyframes, points) {
		t.Fatal("self RPC did not detach index", err)
	}
	request := core.HLSRequest{SessionID: "indexed-session", Info: detached, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1, Enhancement: core.VideoEnhancement{State: "off"}}}
	over := request
	over.SessionID = "over-indexed"
	over.Info = core.CloneMediaInfo(request.Info)
	over.Info.Keyframes = make([]core.KeyframePoint, remotetranscode.MaxKeyframes+1)
	if _, err = source.StartPreparedHLS(ctx, 9, replay, over); err == nil || source.Allocated(over.SessionID) {
		t.Fatal("oversize index reached reservation")
	}
	if _, err = source.StartPreparedHLS(ctx, 9, replay, request); err != nil {
		t.Fatal("indexed capability/start chain", err)
	}
	encoder.mu.Lock()
	received := append([]core.KeyframePoint(nil), encoder.received...)
	encoder.mu.Unlock()
	if !reflect.DeepEqual(received, points) {
		t.Fatal("capability/start/worker snapshot changed complete index")
	}
	record, err := ledger.Lookup(ctx, request.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	source.Stop(request.SessionID)
	for i := 0; i < 50; i++ {
		_ = source.Tick(ctx)
		worker.Cleanup()
		source.Cleanup()
		record, err = ledger.Lookup(ctx, request.SessionID)
		if err == nil && record.Retired {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !record.Retired {
		t.Fatal("indexed generation not retired")
	}
	old := playbackcontinuity.SourceBinding{Owner: placement.Target{NodeID: sourceID.NodeID, Boot: sourceID.Boot}, PlayID: replay.PlayID, Revision: replay.Revision, Fence: record.Fence}
	reopened, err := source.ReopenSource(ctx, old, "indexed-reopen")
	if err != nil || !reflect.DeepEqual(reopened.Prepared.Info.Keyframes, points) {
		t.Fatal("same-owner reopen lost complete index", err)
	}
	reopened.Responsibility.Retire()
	for i := 0; i < 50; i++ {
		_ = source.Tick(ctx)
		source.Cleanup()
		select {
		case <-reopened.Responsibility.Done():
			return
		default:
			time.Sleep(time.Millisecond)
		}
	}
	t.Fatal("indexed reopen source lifetime not retired")
}
