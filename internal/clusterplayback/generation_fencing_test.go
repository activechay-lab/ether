package clusterplayback

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodesource"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
)

type partitionEncoder struct {
	fixtureEncoder
	entered, release chan struct{}
}
type partitionSession struct {
	*fixtureSession
	entered, release chan struct{}
}

func (e *partitionEncoder) StartHLSOwned(ctx context.Context, q core.HLSRequest) (core.HLSSession, core.EncoderRetirement, error) {
	_, retirement, err := e.fixtureEncoder.StartHLSOwned(ctx, q)
	if err != nil {
		return nil, retirement, err
	}
	return &partitionSession{e.session, e.entered, e.release}, retirement, nil
}
func (s *partitionSession) ServePlaylist(w http.ResponseWriter, q *http.Request) {
	close(s.entered)
	select {
	case <-s.release:
	case <-q.Context().Done():
		return
	}
	s.fixtureSession.ServePlaylist(w, q)
}

// This offline invariant uses a deliberately running encoder double. The SQL
// transition models the controller's committed higher generation; it is not a
// retirement proof or a production supersession implementation.
func TestPartitionedGenerationCannotPublishRenewOrReleaseReplacement(t *testing.T) {
	ctx := context.Background()
	db := openFixtureDB(t)
	ledger, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: time.Minute, LockTimeout: fixtureSQLBudget()}, outputRetirementVerifier{})
	if err != nil {
		t.Fatal(err)
	}
	encoder := &partitionEncoder{entered: make(chan struct{}), release: make(chan struct{})}
	manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{}, Transcoder: encoder, SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	ps, _, err := manager.PrepareSurfaceSource(ctx, &core.User{ID: 9}, core.MovieKey(1), core.ClientCaps{ClientID: "partition"}, "", false)
	if err != nil {
		t.Fatal(err)
	}
	access, err := playback.NewSourceAccess(manager, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	sourceID := nodewire.Identity{Cluster: "partition", NodeID: "source", Boot: "sb"}
	workerID := nodewire.Identity{Cluster: "partition", NodeID: "worker", Boot: "wb"}
	guards := fixtureGuards(t, sourceID, workerID)
	endpoints := map[nodewire.Identity]string{}
	eligible := func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }
	base := Options{DB: db, Placement: ledger, Endpoint: func(_ context.Context, id nodewire.Identity) (string, error) { return endpoints[id], nil }, Eligible: eligible, MemberRole: eligible, Capacity: 16, ClockBoundKnown: true}
	opts := base
	opts.Self = sourceID
	opts.Guard = guards[0]
	opts.Playback = manager
	opts.SourceAccess = access
	opts.ActiveViewer = func(context.Context, int64) (*core.User, error) { return &core.User{ID: 9}, nil }
	opts.SourcePolicy = func(context.Context, *core.User, *core.PlaySession) error { return nil }
	source, err := NewRuntime(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	opts = base
	opts.Self = workerID
	opts.Guard = guards[1]
	opts.Encoder = encoder
	worker, err := NewRuntime(opts)
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
	target := placement.Target{NodeID: workerID.NodeID, Boot: workerID.Boot}
	if err = ledger.SetPool(ctx, "cpu", config.RoleTranscode, 1); err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetTarget(ctx, target, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"cpu"}}); err != nil {
		t.Fatal(err)
	}
	rec, err := ledger.Reserve(ctx, placement.Request{Session: "partition", Operation: "initial", Controller: placement.Controller{ID: sourceID.NodeID, Boot: sourceID.Boot}, Target: target, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: "cpu", Units: 1}}, Lease: time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	route := Route{Fence: rec.Fence, Source: sourceID, Viewer: 9, PlayID: ps.ID, Revision: core.SourceRevision(ps)}
	if err = source.putRoute(ctx, route); err != nil {
		t.Fatal(err)
	}
	bound, err := source.sourceOperation(ctx, sourceID, sourceRequest{Op: "bind", Route: route})
	if err != nil {
		t.Fatal(err)
	}
	ep, tc, _, err := source.peer(ctx, workerID, WorkerPath)
	if err != nil {
		t.Fatal(err)
	}
	// The actual mTLS transport authenticates the controller to the worker.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: tc}, Timeout: 3 * time.Second}
	defer client.CloseIdleConnections()
	id := remoteIdentity(route)
	start := remotetranscode.Start{Identity: id, Grant: bound.Stable, Expires: time.Now().Add(750 * time.Millisecond), Info: ps.Info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1}}
	// Production worker admission now requires the exact durable operation gate.
	startBytes, _ := json.Marshal(start)
	admission := admissionRecord{Version: 1, Route: route, Operation: sha256.Sum256([]byte("initial")), Request: core.HLSRequest{SessionID: id.Session, Info: ps.Info, Decision: start.Decision}, Identity: id, StartHash: sha256.Sum256(startBytes)}
	admissionRaw, err := admission.encode()
	if err != nil {
		t.Fatal(err)
	}
	if err = source.insertAdmission(ctx, admission, admissionRaw); err != nil {
		t.Fatal(err)
	}
	rpc := func(op string, extra map[string]any) *http.Response {
		t.Helper()
		m := map[string]any{"Version": remotetranscode.Version, "Op": op, "Identity": id}
		for k, v := range extra {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		request, _ := http.NewRequestWithContext(ctx, "POST", ep, bytes.NewReader(b))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(coordinatorEpochHeader, "1")
		res, e := client.Do(request)
		if e != nil {
			t.Fatal(e)
		}
		return res
	}
	response := rpc("start", map[string]any{"Start": start})
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("start", response.StatusCode)
	}
	delayed := make(chan *http.Response, 1)
	go func() {
		b, _ := json.Marshal(map[string]any{"Version": remotetranscode.Version, "Op": "playlist", "Identity": id})
		request, _ := http.NewRequestWithContext(ctx, "POST", ep, bytes.NewReader(b))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(coordinatorEpochHeader, "1")
		res, _ := client.Do(request)
		delayed <- res
	}()
	select {
	case <-encoder.entered:
	case <-time.After(time.Second):
		t.Fatal("playlist did not enter")
	}
	input := worker.inputs[id].input
	grant := source.sources[sourceKey(rec.Fence, route.SourceGeneration)].grant
	// Commit a replacement generation without claiming the old process has died.
	if _, err = db.ExecContext(ctx, `UPDATE placement_sessions SET generation=generation+1,operation='replacement' WHERE session=?`, rec.Session); err != nil {
		t.Fatal(err)
	}
	next := route
	next.Fence.Generation++
	if err = source.putRoute(ctx, next); err != nil {
		t.Fatal(err)
	}
	close(encoder.release)
	res := <-delayed
	if res == nil {
		t.Fatal("missing response")
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 409 || strings.Contains(string(body), "#EXTM3U") {
		t.Fatal("stale manifest published", res.StatusCode)
	}
	for _, op := range []string{"segment", "playlist"} {
		res = rpc(op, nil)
		res.Body.Close()
		if res.StatusCode == 200 {
			t.Fatal("stale output", op)
		}
	}
	renewal := remotetranscode.Renewal{Identity: id, Operation: 1, PreviousDeadline: start.Expires, Deadline: start.Expires.Add(time.Second)}
	res = rpc("renew", map[string]any{"Renewal": renewal})
	res.Body.Close()
	if res.StatusCode == 200 {
		t.Fatal("stale worker renewal")
	}
	if _, err = source.authority.Verify(ctx, nodesource.Peer{NodeID: workerID.NodeID, Boot: workerID.Boot}, grant.Credential); err == nil {
		t.Fatal("stale source grant")
	}

	sourceEP, sourceTLS, _, e := worker.peer(ctx, sourceID, nodesource.Path)
	if e != nil {
		t.Fatal(e)
	}
	sourceClient := &http.Client{Transport: &http.Transport{TLSClientConfig: sourceTLS}, Timeout: time.Second}
	defer sourceClient.CloseIdleConnections()
	sourceRequest, _ := http.NewRequest("GET", sourceEP, nil)
	sourceRequest.Header.Set(nodesource.CredentialHeader, grant.Credential)
	sourceResponse, e := sourceClient.Do(sourceRequest)
	if e != nil {
		t.Fatal(e)
	}
	sourceResponse.Body.Close()
	if sourceResponse.StatusCode != 403 {
		t.Fatal("source server accepted stale grant", sourceResponse.StatusCode)
	}
	if _, err = source.authority.Renew(ctx, source.sources[sourceKey(rec.Fence, route.SourceGeneration)].handle, grant, grant.Expires.Add(time.Second)); err == nil {
		t.Fatal("stale source renewal")
	}
	raw, _, err := input.GatewayInput(id, bound.Stable)
	if err == nil {
		headers, e := input.HeadersForInput(raw)
		if e != nil {
			t.Fatal(e)
		}
		q, _ := http.NewRequest("GET", raw, nil)
		for k, v := range headers {
			q.Header.Set(k, v)
		}
		res, err = http.DefaultClient.Do(q)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if len(b) > 0 && string(b) == "0123456789" {
			t.Fatal("stale gateway bytes")
		}
	}
	out := httptest.NewRecorder()
	_, err = source.fencedDelivery(out, httptest.NewRequest("GET", "/", nil), route).Write([]byte("old"))
	if err == nil || out.Code != 409 {
		t.Fatal("stale delivery")
	}
	if err = ledger.Renew(ctx, rec.Fence, time.Now().Add(time.Minute)); err != placement.ErrFenced {
		t.Fatal("stale placement renewal", err)
	}
	if err = ledger.Retire(ctx, rec.Fence, "offline-proof"); err != placement.ErrFenced {
		t.Fatal("stale completion", err)
	}
	select {
	case <-encoder.session.EncoderDone():
	case <-time.After(2 * time.Second):
		t.Fatal("worker exceeded self-fencing bound")
	}
	current, err := ledger.Lookup(ctx, rec.Session)
	if err != nil || current.Generation != 2 || current.Retired {
		t.Fatal("replacement changed", current, err)
	}
	var units int
	if err = db.QueryRowContext(ctx, `SELECT SUM(units) FROM placement_claims WHERE session=?`, rec.Session).Scan(&units); err != nil || units != 1 {
		t.Fatal("replacement capacity released", units, err)
	}
	t.Log("old generation denied at worker/delivery/source/gateway and placement; worker stopped by local lease; replacement retains one capacity unit")
}
