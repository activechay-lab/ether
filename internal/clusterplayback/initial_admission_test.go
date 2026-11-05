package clusterplayback

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/remotetranscode"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestDurableAdmissionAcrossCoordinatorRestart(t *testing.T) {
	// Independently mutable fields must detach even when the request is only a
	// future instruction shape. This does not enable or admit recovery requests.
	stream, disposition := 2, true
	mutable := core.HLSRequest{InputURL: "http://127.0.0.1/private-input", InputHeaders: map[string]string{"X-Test": "value"}, SoftSubtitleStream: &stream,
		Info:     core.MediaInfo{Streams: []core.StreamInfo{{Codec: "h264", DefaultDisposition: &disposition}}, Keyframes: []core.KeyframePoint{{Offset: 1}}, Chapters: []core.Chapter{{Title: "original"}}},
		Decision: core.Decision{AudioFilters: []string{"original"}}, RecoveryStart: &core.RecoveryStart{AdmittedDecision: core.Decision{AudioFilters: []string{"original"}}, Audio: &core.RecoveryAACCursor{SourceRevision: "original"}}}
	initial := newInitialAdmission(Route{}, mutable)
	sendCopy := cloneAdmissionRequest(initial.request)
	mutable.Info.Streams[0].Codec, mutable.Info.Keyframes[0].Offset, mutable.Info.Chapters[0].Title = "changed", 9, "changed"
	*mutable.Info.Streams[0].DefaultDisposition = false
	mutable.Decision.AudioFilters[0], *mutable.SoftSubtitleStream = "changed", 9
	mutable.RecoveryStart.Audio.SourceRevision = "changed"
	sendCopy.RecoveryStart.AdmittedDecision.AudioFilters[0] = "changed"
	if initial.request.InputURL != "" || initial.request.InputHeaders != nil || initial.request.InputLease != nil || initial.request.Info.Streams[0].Codec != "h264" || !*initial.request.Info.Streams[0].DefaultDisposition || initial.request.Info.Keyframes[0].Offset != 1 || initial.request.Info.Chapters[0].Title != "original" || initial.request.Decision.AudioFilters[0] != "original" || *initial.request.SoftSubtitleStream != 2 || initial.request.RecoveryStart.Audio.SourceRevision != "original" || initial.request.RecoveryStart.AdmittedDecision.AudioFilters[0] != "original" {
		t.Fatal("initial admission shares caller or send-copy ownership")
	}
	var empty *remotetranscode.Session
	if _, _, err := empty.StartCommitment(); !errors.Is(err, remotetranscode.ErrUnsupported) {
		t.Fatal("nil token supplied a commitment")
	}
	if _, _, err := new(remotetranscode.Session).StartCommitment(); !errors.Is(err, remotetranscode.ErrUnsupported) {
		t.Fatal("unconstructed token supplied a commitment")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db := store.OpenTest(t)
	verifier := NewVerifier()
	ledger, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: 10 * time.Minute, LockTimeout: time.Second}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{}, Transcoder: &fixtureEncoder{}, SelectProfile: func(core.ClientCaps, *core.User) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close() })
	access, err := playback.NewSourceAccess(manager, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	controller := nodewire.Identity{Cluster: "admission", NodeID: "controller-source", Boot: "controller-source-boot"}
	worker := nodewire.Identity{Cluster: "admission", NodeID: "worker", Boot: "worker-boot"}
	replacement := controller
	replacement.Boot = "replacement-controller-boot"
	guards := fixtureGuards(t, controller, worker, replacement)
	endpoints := map[nodewire.Identity]string{}
	eligible := func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }
	front, err := NewRuntime(Options{DB: db, Placement: ledger, Self: controller, Guard: guards[0], Capacity: 16, ClockBoundKnown: true, ClockSkew: time.Millisecond,
		Eligible: eligible, MemberRole: eligible, Endpoint: func(_ context.Context, id nodewire.Identity) (string, error) {
			if ep := endpoints[id]; ep != "" {
				return ep, nil
			}
			return "", ErrDenied
		},
		Playback: manager, SourceAccess: access, ActiveViewer: func(context.Context, int64) (*core.User, error) { return &core.User{ID: 9}, nil }, SourcePolicy: func(context.Context, *core.User, *core.PlaySession) error { return nil }, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision { return core.Decision{} }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = front.Close() })
	if err := verifier.Bind(front); err != nil {
		t.Fatal(err)
	}
	sourceServer := httptest.NewUnstartedServer(front.Handler())
	sourceServer.TLS = guards[0].ServerTLS()
	sourceServer.StartTLS()
	t.Cleanup(sourceServer.Close)
	endpoints[controller] = sourceServer.URL
	prepared, err := front.PrepareSource(ctx, controller, PrepareRequest{Operation: "initial-admission", Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: "initial-client"}, Protocol: core.ProtocolHLS})
	if err != nil {
		t.Fatal(err)
	}
	request := core.HLSRequest{SessionID: "initial-public", InputURL: "http://127.0.0.1/never-retained", ReleaseID: prepared.ReleaseID, Info: core.CloneMediaInfo(prepared.Info), Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mpegts", SubtitleStream: -1, Reason: "initial-plan"}}
	expected := cloneAdmissionRequest(request)
	expected.InputURL = ""
	type receivedStart struct {
		hash        [32]byte
		identity    remotetranscode.Identity
		beforeStart bool
	}
	received := make(chan receivedStart, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	var calls atomic.Int32
	var hookHash [32]byte
	hookWire := make(chan [32]byte, 1)
	workerServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		peer, err := guards[1].Request(q)
		if err != nil || peer != controller {
			http.Error(w, "denied", 401)
			return
		}
		if q.URL.Path == CapabilitiesPath {
			_ = json.NewEncoder(w).Encode(capabilityReply{Identity: worker, Capabilities: remotetranscode.Supported(), Supported: true})
			return
		}
		var message struct {
			Op       string
			Identity remotetranscode.Identity
			Start    json.RawMessage
		}
		if json.NewDecoder(http.MaxBytesReader(w, q.Body, remotetranscode.MaxMessage)).Decode(&message) != nil || message.Op != "start" {
			http.Error(w, "unavailable", 503)
			return
		}
		if message.Identity.Session == "hook-boundary" {
			hookWire <- sha256.Sum256(message.Start)
			connection, _, err := w.(http.Hijacker).Hijack()
			if err == nil {
				_ = connection.Close()
			}
			return
		}
		calls.Add(1)
		front.mu.Lock()
		x := front.executions[request.SessionID]
		before := x != nil && x.initial != nil && x.initial.route == x.route && x.initial.hasCommitment && reflect.DeepEqual(x.initial.request, expected)
		front.mu.Unlock()
		durable, durableErr := front.readAdmission(q.Context(), x.route)
		before = before && durableErr == nil && durable.StartHash == sha256.Sum256(message.Start)
		received <- receivedStart{hash: sha256.Sum256(message.Start), identity: message.Identity, beforeStart: before}
		<-release
		// The bytes were received over mTLS, but no acknowledgement or worker
		// acceptance proof is returned. The caller must retain the exact token.
		connection, _, err := w.(http.Hijacker).Hijack()
		if err == nil {
			_ = connection.Close()
		}
	}))
	workerServer.TLS = guards[1].ServerTLS()
	workerServer.StartTLS()
	t.Cleanup(workerServer.Close)
	endpoints[worker] = workerServer.URL
	if err := ledger.SetPool(ctx, "admission-worker", config.RoleTranscode, 1); err != nil {
		t.Fatal(err)
	}
	if err := ledger.SetTarget(ctx, placement.Target{NodeID: worker.NodeID, Boot: worker.Boot}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"admission-worker"}}); err != nil {
		t.Fatal(err)
	}
	finished := make(chan struct{})
	result := make(chan error, 1)
	t.Cleanup(func() {
		releaseOnce.Do(func() { close(release) })
		<-finished
		front.mu.Lock()
		x := front.executions[expected.SessionID]
		front.mu.Unlock()
		if x != nil {
			_, _ = front.sourceOperation(context.Background(), controller, sourceRequest{Op: "retire", Route: x.route})
			front.Cleanup()
		}
	})
	go func() {
		defer close(finished)
		_, err := front.StartPreparedHLS(ctx, 9, prepared, request)
		result <- err
	}()
	var observed receivedStart
	select {
	case observed = <-received:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !observed.beforeStart {
		t.Fatal("detached authoritative snapshot did not precede start")
	}
	// Simulate loss of all coordinator execution memory while the worker has
	// received Start but no response has returned. Source is an independent role
	// whose original bound lifetime must survive for this historical read.
	opts := front.o
	opts.Self, opts.Guard = replacement, guards[2]
	opts.Playback, opts.SourceAccess = nil, nil
	restarted, restartErr := NewRuntime(opts)
	if restartErr != nil {
		t.Fatal(restartErr)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	front.mu.Lock()
	original := front.executions[expected.SessionID]
	originalRoute := original.route
	front.mu.Unlock()
	recovered, recoverErr := restarted.loadRecoveryAdmission(ctx, originalRoute, 9)
	if recoverErr != nil || !reflect.DeepEqual(recovered.request, expected) || recovered.commitment != observed.hash || recovered.commitmentIdentity != observed.identity || restarted.Allocated(expected.SessionID) {
		t.Fatal("restart lost historical authority or created an execution", recoverErr)
	}
	recovered.request.Info.Streams[0].Codec = "mutated-recovery-copy"
	recovered, recoverErr = restarted.loadRecoveryAdmission(ctx, originalRoute, 9)
	if recoverErr != nil || !reflect.DeepEqual(recovered.request, expected) {
		t.Fatal("recovery copy mutated stored authority", recoverErr)
	}
	if _, err := restarted.loadRecoveryAdmission(ctx, originalRoute, 10); err == nil {
		t.Fatal("wrong authenticated viewer read admission")
	}
	rawAdmission, err := front.readAdmission(ctx, originalRoute)
	if err != nil {
		t.Fatal(err)
	}
	encodedAdmission, _ := rawAdmission.encode()
	if strings.Contains(encodedAdmission, "never-retained") || strings.Contains(encodedAdmission, "private-input") {
		t.Fatal("input actor leaked into database")
	}
	// Idempotence and immutable payload/hash conflict use the real database.
	if err := front.persistAdmission(ctx, original, observed.identity, observed.hash); err != nil {
		t.Fatal("exact retry failed", err)
	}
	conflicting := observed.hash
	conflicting[0] ^= 1
	if err := front.persistAdmission(ctx, original, observed.identity, conflicting); err == nil {
		t.Fatal("conflicting hash replaced commitment")
	}
	changed := rawAdmission
	changed.Request = cloneAdmissionRequest(rawAdmission.Request)
	changed.Request.Decision.Reason = "conflicting-original"
	changedRaw, _ := changed.encode()
	if err := front.insertAdmission(ctx, changed, changedRaw); err == nil {
		t.Fatal("conflicting payload replaced original")
	}
	// A database timeout is unknown, not evidence the insert did not happen.
	canceled, stopWrite := context.WithCancel(ctx)
	stopWrite()
	if err := front.insertAdmission(canceled, rawAdmission, encodedAdmission); !isAdmissionUnknown(err) {
		t.Fatal("SQL timeout was not unknown", err)
	}
	request.Info.Streams[0].Codec = "caller-mutated-after-send"
	request.Decision.Reason = "caller-mutated-after-send"
	releaseOnce.Do(func() { close(release) })
	if err := <-result; !errors.Is(err, ErrPending) {
		t.Fatal("lost result was not retained as uncertain", err)
	}
	front.mu.Lock()
	x := front.executions[expected.SessionID]
	admission := x.initial
	token := x.session
	retained := x.retirement
	snapshot := cloneAdmissionRequest(admission.request)
	boundIdentity, boundHash, present := admission.commitmentIdentity, admission.commitment, admission.hasCommitment
	route := admission.route
	front.mu.Unlock()
	if !reflect.DeepEqual(snapshot, expected) || route.Source != prepared.Owner || route.PlayID != prepared.PlayID || route.Revision != prepared.Revision || route.Fence.Controller != (placement.Controller{ID: controller.NodeID, Boot: controller.Boot}) || route.Fence.Target != (placement.Target{NodeID: worker.NodeID, Boot: worker.Boot}) || !present || boundHash != observed.hash || boundIdentity != observed.identity || boundIdentity != remoteIdentity(route) || retained != token || !front.Allocated(expected.SessionID) {
		t.Fatal("uncertain start lost or changed exact admission/commitment")
	}
	if _, available := front.Session(expected.SessionID); available {
		t.Fatal("commitment became session availability")
	}
	if token.EncoderErr() != core.ErrEncoderPending {
		t.Fatal("commitment became retirement proof")
	}
	identity, hash, err := token.StartCommitment()
	if err != nil || identity != observed.identity || hash != observed.hash {
		t.Fatal("getter did not hash actual retained marshaled bytes")
	}
	wrongRoute := route
	wrongRoute.Fence.Target.Boot = "different-worker-boot"
	wrong := &execution{route: wrongRoute, initial: newInitialAdmission(wrongRoute, expected)}
	if front.retainStartCommitment(wrong, token) || wrong.retirement != token || wrong.initial.hasCommitment {
		t.Fatal("identity mismatch lost token or installed a commitment")
	}

	// Exercise the actual adapter boundary: unknown and conflicting commits
	// retain a pending token and send zero additional Start requests. The hook
	// deliberately mutates caller-owned request slices after Start installation.
	endpoint, tc, check, err := front.peer(ctx, worker, WorkerPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, hookErr := range []error{&placement.UnknownOutcome{Err: context.DeadlineExceeded}, ErrDenied} {
		adversarial := cloneAdmissionRequest(expected)
		adapter, err := remotetranscode.NewAdapter(remotetranscode.AdapterOptions{Endpoint: endpoint, Client: &http.Client{Transport: &http.Transport{TLSClientConfig: tc}}, RecheckServer: check, Capacity: 1, Decide: front.o.Decide,
			Assign: func(context.Context, core.HLSRequest) (remotetranscode.Assignment, error) {
				return remotetranscode.Assignment{Identity: observed.identity, Grant: "not-persisted", Expires: time.Now().Add(time.Minute)}, nil
			},
			CommitStart: func(c context.Context, id remotetranscode.Identity, hash [32]byte) error {
				if deadline, ok := c.Deadline(); !ok || time.Until(deadline) > 5*time.Second {
					t.Error("unbounded commit callback")
				}
				adversarial.Info.Streams[0].Codec = "callback-mutation"
				return hookErr
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		hs, held, err := adapter.StartHLSOwned(ctx, adversarial)
		adapter.Close()
		if hs != nil || held == nil || !errors.Is(err, remotetranscode.ErrUncertain) || held.EncoderErr() != core.ErrEncoderPending || calls.Load() != 1 {
			t.Fatal("failed commit sent or retired Start", err)
		}
	}
	// A successful hook cannot mutate bytes already installed in Session.start.
	adversarial := cloneAdmissionRequest(expected)
	adversarial.SessionID = "hook-boundary"
	hookIdentity := observed.identity
	hookIdentity.Session = adversarial.SessionID
	hookAdapter, err := remotetranscode.NewAdapter(remotetranscode.AdapterOptions{Endpoint: endpoint, Client: &http.Client{Transport: &http.Transport{TLSClientConfig: tc}}, RecheckServer: check, Capacity: 1, Decide: front.o.Decide,
		Assign: func(context.Context, core.HLSRequest) (remotetranscode.Assignment, error) {
			return remotetranscode.Assignment{Identity: hookIdentity, Grant: "hook-only", Expires: time.Now().Add(time.Minute)}, nil
		},
		CommitStart: func(c context.Context, id remotetranscode.Identity, hash [32]byte) error {
			hookHash = hash
			adversarial.Info.Streams[0].Codec = "changed-by-hook"
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, hookToken, _ := hookAdapter.StartHLSOwned(ctx, adversarial)
	hookAdapter.Close()
	if hookToken == nil || hookHash == ([32]byte{}) || hookHash != <-hookWire {
		t.Fatal("callback changed committed Start bytes")
	}
	// Loss of current viewer policy blocks historical loading, independently
	// of a valid retained row and a still-present source binding.
	savedPolicy := front.o.SourcePolicy
	front.o.SourcePolicy = func(context.Context, *core.Viewer, *core.PlaySession) error { return ErrDenied }
	if _, err := restarted.loadRecoveryAdmission(ctx, originalRoute, 9); err == nil {
		t.Fatal("historical row bypassed current policy")
	}
	front.o.SourcePolicy = savedPolicy
	// Exercise immutable generation and quota SQL using a separate inert index;
	// this data never grants execution authority or enters Runtime.executions.
	scratch := rawAdmission
	scratch.Route.Fence.Session, scratch.Request.SessionID, scratch.Identity.Session = "admission-cas", "admission-cas", "admission-cas"
	if err := front.putRoute(ctx, scratch.Route); err != nil {
		t.Fatal(err)
	}
	scratchRaw, _ := scratch.encode()
	savedCapacity := front.o.HistoryCapacity
	front.o.HistoryCapacity = 1
	if err := front.insertAdmission(ctx, scratch, scratchRaw); !errors.Is(err, ErrPending) {
		t.Fatal("history quota admitted another record", err)
	}
	front.o.HistoryCapacity = savedCapacity
	if err := front.insertAdmission(ctx, scratch, scratchRaw); err != nil {
		t.Fatal(err)
	}
	next := scratch
	next.Route.Fence.Generation++
	next.Identity = remoteIdentity(next.Route)
	if err := front.putRoute(ctx, next.Route); err != nil {
		t.Fatal(err)
	}
	nextRaw, _ := next.encode()
	if err := front.insertAdmission(ctx, next, nextRaw); err != nil {
		t.Fatal(err)
	}
	if err := front.insertAdmission(ctx, scratch, scratchRaw); err == nil {
		t.Fatal("old-generation callback accepted after replacement")
	}
	if got, err := front.readAdmission(ctx, next.Route); err != nil || got.StartHash != next.StartHash {
		t.Fatal("old callback overwrote replacement", err)
	}
	_ = token.RetireEncoder()
	identity, hash, err = token.StartCommitment()
	if err != nil || identity != observed.identity || hash != observed.hash || calls.Load() != 1 {
		t.Fatal("stopping changed commitment or sent another start")
	}
	if record, err := ledger.Lookup(ctx, expected.SessionID); err != nil || record.Fence != route.Fence || record.Retired {
		t.Fatal("uncertain start optimistically released placement", err)
	}
}
