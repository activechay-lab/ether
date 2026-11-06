package clusterplayback

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/playback"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSequentialCrossReplicaCapacityRetirement(t *testing.T) {
	sourceID := nodewire.Identity{Cluster: "plex-composition", NodeID: "source", Boot: "source-boot"}
	workerID := nodewire.Identity{Cluster: sourceID.Cluster, NodeID: "worker", Boot: "worker-boot"}
	ownerID := nodewire.Identity{Cluster: sourceID.Cluster, NodeID: "owner", Boot: "owner-boot"}
	senderID := nodewire.Identity{Cluster: sourceID.Cluster, NodeID: "sender", Boot: "sender-boot"}
	guards := fixtureGuards(t, sourceID, workerID, ownerID, senderID)
	db := store.OpenTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	verifier := NewVerifier()
	ledger, err := placement.New(db, placement.Options{MaxSessions: 32, MaxLease: 10 * time.Minute, LockTimeout: time.Second}, verifier)
	if err != nil {
		t.Fatal(err)
	}
	encoder := &fixtureEncoder{}
	manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{}, Transcoder: encoder, SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return core.QualityProfile{} }}, config.Defaults(t.TempDir()), playback.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	access, err := playback.NewSourceAccess(manager, 32, 32)
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
	auth := &composedAuth{}
	base := Options{DB: db, Placement: ledger, Endpoint: endpoint, Eligible: eligible, MemberRole: eligible, Capacity: 32, ClockBoundKnown: true, ClockSkew: time.Millisecond, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision {
		return core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, Container: "mpegts", AudioStream: -1, SubtitleStream: -1}
	}}
	sourceOpts := base
	sourceOpts.Self = sourceID
	sourceOpts.Guard = guards[0]
	sourceOpts.Roles = config.NodeRoles{config.RoleSource}
	sourceOpts.Playback = manager
	sourceOpts.SourceAccess = access
	sourceOpts.ActiveViewer = auth.ActiveUser
	sourceOpts.SourcePolicy = func(context.Context, *core.User, *core.PlaySession) error { return nil }
	source, err := NewRuntime(sourceOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	workerOpts := base
	workerOpts.Self = workerID
	workerOpts.Guard = guards[1]
	workerOpts.Roles = config.NodeRoles{config.RoleTranscode}
	workerOpts.Encoder = encoder
	worker, err := NewRuntime(workerOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer worker.Close()
	ownerOpts := base
	ownerOpts.Self = ownerID
	ownerOpts.Guard = guards[2]
	ownerOpts.Roles = config.NodeRoles{config.RoleServer, config.RoleDelivery}
	owner, err := NewRuntime(ownerOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if err = verifier.Bind(owner); err != nil {
		t.Fatal(err)
	}
	senderVerifier := NewVerifier()
	senderLedger, err := placement.New(db, placement.Options{MaxSessions: 32, MaxLease: 10 * time.Minute, LockTimeout: time.Second}, senderVerifier)
	if err != nil {
		t.Fatal(err)
	}
	senderOpts := base
	senderOpts.Placement = senderLedger
	senderOpts.Self = senderID
	senderOpts.Guard = guards[3]
	senderOpts.Roles = config.NodeRoles{config.RoleServer, config.RoleDelivery}
	sender, err := NewRuntime(senderOpts)
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	if err := senderVerifier.Bind(sender); err != nil {
		t.Fatal(err)
	}
	for i, r := range []*Runtime{source, worker, owner, sender} {
		server := httptest.NewUnstartedServer(r.Handler())
		server.TLS = guards[i].ServerTLS()
		server.StartTLS()
		defer server.Close()
		endpoints[r.o.Self] = server.URL
	}
	if err = ledger.SetPool(ctx, "plex-worker", config.RoleTranscode, 1); err != nil {
		t.Fatal(err)
	}
	if err = ledger.SetTarget(ctx, placement.Target{NodeID: workerID.NodeID, Boot: workerID.Boot}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"plex-worker"}}); err != nil {
		t.Fatal(err)
	}

	sourceEndpoint := endpoints[sourceID]
	// A one-slot pool must sustain repeated lifetimes with a different control
	// frontend receiving Stop. Backend fakes own actual input/gateway lifetimes.
	for n := 0; n < 8; n++ {
		starter, stopper := owner, sender
		if n%2 == 1 {
			starter, stopper = sender, owner
		}
		prepared, err := starter.PrepareSource(ctx, sourceID, PrepareRequest{Operation: fmt.Sprintf("prepare-%d", n), Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: fmt.Sprintf("client-%d", n)}, Protocol: "hls"})
		if err != nil {
			t.Fatal(err)
		}
		id := fmt.Sprintf("sequential-%d", n)
		q := core.HLSRequest{SessionID: id, Info: prepared.Info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1}}
		if _, err := starter.StartPreparedHLS(ctx, 9, prepared, q); err != nil {
			t.Fatalf("session %d: %v", n, err)
		}
		backend := encoder.session
		if n < 6 {
			if !stopper.Allocated(id) {
				t.Fatal("other replica lost durable ownership")
			}
			stopper.Stop(id)
			// Duplicated Stop must not create a second obligation or replace a seal.
			stopper.Stop(id)
		} else {
			// Model backend idle timeout/client disappearance: the encoder exits
			// independently, leaving the worker to seal and drain its gateway input.
			if err := backend.RetireEncoder(); err != nil {
				t.Fatal(err)
			}
		}
		if n == 7 {
			// The worker is still on its original boot, but its source process
			// was replaced after backend exit. Normal exact encoder retirement
			// must combine with independent authenticated source-boot proof.
			life, ok := manager.SourceRetirement(prepared.PlayID)
			if !ok {
				t.Fatal("missing source outcome")
			}
			if err := manager.RetireSource(prepared.PlayID, life); err != nil {
				t.Fatal(err)
			}
			if _, err := db.ExecContext(ctx, `INSERT INTO cluster_nodes(id,incarnation,started_at,last_seen) VALUES(?,?,?,?)`, sourceID.NodeID, "source-only-replacement", time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
				t.Fatal(err)
			}
			delete(endpoints, sourceID)
			preparedLife := &remotePreparation{owner: sourceID, operation: "source-only-preparation", retiring: true, done: make(chan struct{})}
			remote := &RemotePlayback{r: starter, o: RemotePlaybackOptions{Now: time.Now}, entries: map[string]*remotePreparation{"source-only": preparedLife}}
			if err := remote.tick(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case <-preparedLife.done:
			default:
				t.Fatal("replaced source preparation retained retirement wait")
			}
		}
		for i := 0; i < 100; i++ {
			_ = starter.Tick(ctx)
			worker.Cleanup()
			source.Cleanup()
			record, err := ledger.Lookup(ctx, id)
			if err == nil && record.Retired {
				break
			}
			if i == 99 {
				t.Fatalf("session %d retirement leaked: %+v %v", n, record, err)
			}
			time.Sleep(time.Millisecond)
		}
		select {
		case <-backend.EncoderDone():
		default:
			t.Fatal("backend process still owned")
		}
		if backend.EncoderErr() != nil {
			t.Fatal("backend retirement unverified")
		}
		for _, table := range []string{"placement_claims", "placement_displaced_claims"} {
			var count int
			if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("%s leaked %d claims: %v", table, count, err)
			}
		}
		t.Logf("session %d retired; full capacity restored; backend and input joined", n)
		if n == 7 {
			// Restore the independent fake source for the next crash scenario.
			if _, err := db.ExecContext(ctx, `DELETE FROM cluster_nodes WHERE id=?`, sourceID.NodeID); err != nil {
				t.Fatal(err)
			}
			endpoints[sourceID] = sourceEndpoint
		}
	}
	// Crash before Stop: lose all coordinator memory and both exact worker/source
	// incarnations. Age, absence and worker replacement alone retain the charge.
	prepared, err := owner.PrepareSource(ctx, sourceID, PrepareRequest{Operation: "crash-prepare", Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: "crash-client"}, Protocol: "hls"})
	if err != nil {
		t.Fatal(err)
	}
	id := "crash-before-stop"
	if _, err = owner.StartPreparedHLS(ctx, 9, prepared, core.HLSRequest{SessionID: id, Info: prepared.Info, Decision: core.Decision{Kind: core.DecisionRemux, VideoCopy: true, AudioCopy: true, SubtitleStream: -1}}); err != nil {
		t.Fatal(err)
	}
	route, err := owner.route(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	// Explicitly join fake backend work before simulating dead process memory.
	_ = worker.Close()
	_ = manager.Close()
	owner.mu.Lock()
	delete(owner.executions, id)
	owner.mu.Unlock()
	delete(endpoints, workerID)
	delete(endpoints, sourceID)
	if _, err = db.ExecContext(ctx, `UPDATE placement_sessions SET deadline=1 WHERE session=?`, id); err != nil {
		t.Fatal(err)
	}
	assertHeld := func() {
		t.Helper()
		_ = owner.reconcileBootRetirements(ctx)
		rec, err := ledger.Lookup(ctx, id)
		if err != nil || rec.Retired {
			t.Fatalf("absence/expiry freed claim: %+v %v", rec, err)
		}
	}
	assertHeld()
	nextWorker := placement.Target{NodeID: workerID.NodeID, Boot: "replacement-worker-boot"}
	if err = ledger.SetTarget(ctx, nextWorker, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {"plex-worker"}}); err != nil {
		t.Fatal(err)
	}
	assertHeld() // source outcome remains independently required
	nextSource := nodewire.Identity{Cluster: sourceID.Cluster, NodeID: sourceID.NodeID, Boot: "replacement-source-boot"}
	if _, err = db.ExecContext(ctx, `INSERT INTO cluster_nodes(id,incarnation,started_at,last_seen) VALUES(?,?,?,?)`, nextSource.NodeID, nextSource.Boot, time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	owner.o.MemberRole = func(ctx context.Context, id nodewire.Identity, role config.NodeRole) error {
		if id == nextSource {
			return ErrDenied
		}
		return nil
	}
	assertHeld() // a replacement row without authenticated membership is insufficient
	owner.o.MemberRole = eligible
	if err = owner.reconcileBootRetirements(ctx); err != nil {
		t.Fatal(err)
	}
	rec, err := ledger.Lookup(ctx, id)
	if err != nil || !rec.Retired {
		t.Fatalf("authenticated new boots retained claim: %+v %v", rec, err)
	}
	h, err := owner.readOperationGate(ctx, operationKey{sourceID.Cluster, id, route.Fence.Generation})
	if err != nil || h.Gate.Seal == nil || h.Gate.Seal.Reason != sealAuthorityLoss {
		t.Fatalf("dead boot gate unsealed: %+v %v", h, err)
	}
	var claims int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM placement_claims`).Scan(&claims); err != nil || claims != 0 {
		t.Fatalf("dead boot claim leaked: %d %v", claims, err)
	}
	t.Log("crash before Stop: timeout/absence/unauthenticated boots retained claim; authenticated worker+source replacements sealed gate and restored full capacity")

}
