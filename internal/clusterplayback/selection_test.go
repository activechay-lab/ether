package clusterplayback

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
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

type scoredFixtureEncoder struct {
	*fixtureEncoder
	hardware  string
	incapable bool
}

func (e *scoredFixtureEncoder) SupportsDistributedRequest(core.HLSRequest) bool   { return !e.incapable }
func (e *scoredFixtureEncoder) DistributedHardwareEncoder(core.HLSRequest) string { return e.hardware }

func TestTranscodePlacementScoresVerifiedHardwareAndCapacity(t *testing.T) {
	for _, tc := range []struct {
		name                               string
		hardware                           string
		full, incapable, ineligible, local bool
		cpuSlots                           int64
		want                               string
	}{
		{name: "hardware beats spare CPU and lexical order", hardware: "h264_nvenc", cpuSlots: 8, want: "z-gpu"},
		{name: "full GPU falls back to CPU", hardware: "h264_nvenc", full: true, cpuSlots: 8, want: "a-cpu"},
		{name: "incapable GPU falls back to CPU", hardware: "h264_nvenc", incapable: true, cpuSlots: 8, want: "a-cpu"},
		{name: "ineligible GPU excluded", hardware: "h264_nvenc", ineligible: true, cpuSlots: 8, want: "a-cpu"},
		{name: "software fallback ranks by spare slots", cpuSlots: 8, want: "a-cpu"},
		{name: "equal scores have stable tie break", cpuSlots: 1, want: "a-cpu"},
		{name: "equal scores prefer source locality", cpuSlots: 1, local: true, want: "z-gpu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixtureStarted := time.Now()
			db := openFixtureDB(t)
			ctx, cancel := context.WithTimeout(context.Background(), fixtureExecutionBudget(fixtureStarted, 10*time.Second))
			defer cancel()
			ledger, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: time.Minute, LockTimeout: fixtureSQLBudget()}, NewVerifier())
			if err != nil {
				t.Fatal(err)
			}
			frontID := nodewire.Identity{Cluster: "offline", NodeID: "controller", Boot: "front-boot"}
			cpuID := nodewire.Identity{Cluster: "offline", NodeID: "a-cpu", Boot: "cpu-boot"}
			gpuID := nodewire.Identity{Cluster: "offline", NodeID: "z-gpu", Boot: "gpu-boot"}
			guards := fixtureGuards(t, frontID, cpuID, gpuID)
			endpoints := map[nodewire.Identity]string{}
			eligible := func(_ context.Context, id nodewire.Identity, _ config.NodeRole) error {
				if tc.ineligible && id == gpuID {
					return ErrDenied
				}
				return nil
			}
			base := Options{DB: db, Placement: ledger, Capacity: 16, ClockBoundKnown: true, ClockSkew: time.Millisecond, Eligible: eligible, MemberRole: func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }, Endpoint: func(_ context.Context, id nodewire.Identity) (string, error) {
				if ep, ok := endpoints[id]; ok {
					return ep, nil
				}
				return "", errors.New("missing fixture endpoint")
			}}
			for i, id := range []nodewire.Identity{frontID, cpuID, gpuID} {
				opts := base
				opts.Self = id
				opts.Guard = guards[i]
				if i > 0 {
					enc := &scoredFixtureEncoder{fixtureEncoder: &fixtureEncoder{}}
					if i == 2 {
						enc.hardware = tc.hardware
						enc.incapable = tc.incapable
					}
					opts.Encoder = enc
				}
				runtime, err := NewRuntime(opts)
				if err != nil {
					t.Fatal(err)
				}
				defer runtime.Close()
				if i == 0 {
					continue
				}
				server := httptest.NewUnstartedServer(runtime.Handler())
				server.TLS = guards[i].ServerTLS()
				server.StartTLS()
				defer server.Close()
				endpoints[id] = server.URL
				slots := int64(1)
				if i == 1 {
					slots = tc.cpuSlots
				}
				scope := id.NodeID + "/streams"
				if err = ledger.SetPool(ctx, scope, config.RoleTranscode, slots); err != nil {
					t.Fatal(err)
				}
				target := placement.Target{NodeID: id.NodeID, Boot: id.Boot}
				if err = ledger.SetTarget(ctx, target, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {scope}}); err != nil {
					t.Fatal(err)
				}
				if i == 2 && tc.full {
					_, err = ledger.Reserve(ctx, placement.Request{Session: "occupied", Operation: "occupy", Controller: placement.Controller{ID: frontID.NodeID, Boot: frontID.Boot}, Target: target, Role: config.RoleTranscode, Claims: []placement.Claim{{Scope: scope, Units: 1}}, Lease: time.Minute})
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			opts := base
			opts.Self = frontID
			opts.Guard = guards[0]
			front, err := NewRuntime(opts)
			if err != nil {
				t.Fatal(err)
			}
			defer front.Close()
			req := core.HLSRequest{SessionID: "scored-playback", Info: core.MediaInfo{Container: "mkv", Streams: []core.StreamInfo{{Type: "video", Codec: "h264", Width: 1920, Height: 1080}}}, Decision: core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", Container: "mpegts", SubtitleStream: -1}}
			source := nodewire.Identity{}
			if tc.local {
				source = gpuID
			}
			target, claims, _, _, err := front.selectTarget(ctx, req, source)
			if err != nil || target.NodeID != tc.want || len(claims) != 1 || claims[0].Scope != tc.want+"/streams" {
				t.Fatalf("target=%+v claims=%+v err=%v; want %s", target, claims, err, tc.want)
			}
		})
	}
}

// Synchronize the first capability replies after all callers have observed the
// spare GPU slot. Every start must therefore contend for that same reservation.
type contendedGPUEncoder struct {
	*scoredFixtureEncoder
	arrived atomic.Int32
	ready   chan struct{}
	callers int32
	ctx     context.Context
}

func (e *contendedGPUEncoder) SupportsDistributedRequest(core.HLSRequest) bool {
	n := e.arrived.Add(1)
	if n == e.callers {
		close(e.ready)
	}
	if n <= e.callers {
		select {
		case <-e.ready:
		case <-e.ctx.Done():
			return false
		}
	}
	return true
}

func TestConcurrentPlacementFallbackInvariant(t *testing.T) {
	t.Run("uncertainty retains the exact operation", checkPlacementRetryUncertainty)
	const starts = 3 // One GPU slot plus two independent CPU slots.
	db := store.OpenTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ledger, err := placement.New(db, placement.Options{MaxSessions: 16, MaxLease: 10 * time.Minute, LockTimeout: 5 * time.Second}, NewVerifier())
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Defaults(t.TempDir())
	cfg.Server.DefaultStreamLimit = starts
	manager, err := playback.New(playback.Deps{Catalog: fixtureCatalog{}, Resolver: fixtureResolver{}, Engine: fixtureEngine{}, Transcoder: &fixtureEncoder{}, SelectProfile: func(core.ClientCaps, *core.User, string) core.QualityProfile { return core.QualityProfile{} }}, cfg, playback.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	access, err := playback.NewSourceAccess(manager, 16, 16)
	if err != nil {
		t.Fatal(err)
	}
	ids := []nodewire.Identity{{Cluster: "contention", NodeID: "front", Boot: "front-boot"}, {Cluster: "contention", NodeID: "a-cpu", Boot: "cpu-a"}, {Cluster: "contention", NodeID: "b-cpu", Boot: "cpu-b"}, {Cluster: "contention", NodeID: "z-gpu", Boot: "gpu"}}
	guards := fixtureGuards(t, ids...)
	endpoints := map[nodewire.Identity]string{}
	allow := func(context.Context, nodewire.Identity, config.NodeRole) error { return nil }
	decision := core.Decision{Kind: core.DecisionTranscode, VideoCodec: "h264", AudioCopy: true, Container: "mpegts", SubtitleStream: -1}
	base := Options{DB: db, Placement: ledger, Capacity: 16, ClockBoundKnown: true, ClockSkew: time.Millisecond, Eligible: allow, MemberRole: allow, Endpoint: func(_ context.Context, id nodewire.Identity) (string, error) { return endpoints[id], nil }, Decide: func(core.MediaInfo, core.ClientCaps, core.DecideOptions) core.Decision { return decision }}
	var front *Runtime
	for i, id := range ids {
		opts := base
		opts.Self = id
		opts.Guard = guards[i]
		if i == 0 {
			opts.Playback = manager
			opts.SourceAccess = access
			opts.ActiveViewer = func(context.Context, int64) (*core.User, error) { return &core.User{ID: 9}, nil }
			opts.SourcePolicy = func(context.Context, *core.User, *core.PlaySession) error { return nil }
		} else {
			enc := &scoredFixtureEncoder{fixtureEncoder: &fixtureEncoder{}}
			opts.Encoder = enc
			if i == 3 {
				enc.hardware = "h264_nvenc"
				opts.Encoder = &contendedGPUEncoder{scoredFixtureEncoder: enc, ready: make(chan struct{}), callers: starts, ctx: ctx}
			}
			scope := id.NodeID + "/streams"
			if err = ledger.SetPool(ctx, scope, config.RoleTranscode, 1); err != nil {
				t.Fatal(err)
			}
			if err = ledger.SetTarget(ctx, placement.Target{NodeID: id.NodeID, Boot: id.Boot}, config.NodeRoles{config.RoleTranscode}, map[config.NodeRole][]string{config.RoleTranscode: {scope}}); err != nil {
				t.Fatal(err)
			}
		}
		runtime, err := NewRuntime(opts)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		if i == 0 {
			front = runtime
		}
		server := httptest.NewUnstartedServer(runtime.Handler())
		server.TLS = guards[i].ServerTLS()
		server.StartTLS()
		defer server.Close()
		endpoints[id] = server.URL
	}
	prepared := make([]PreparedSource, starts)
	for i := range prepared {
		prepared[i], err = front.PrepareSource(ctx, ids[0], PrepareRequest{Operation: fmt.Sprintf("prepare-%d", i), Viewer: 9, Key: core.MovieKey(1), Caps: core.ClientCaps{ClientID: fmt.Sprintf("client-%d", i)}, Protocol: "hls"})
		if err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < starts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			q := core.HLSRequest{SessionID: fmt.Sprintf("concurrent-%d", i), Info: prepared[i].Info, Decision: decision}
			if _, err := front.StartPreparedHLS(ctx, 9, prepared[i], q); err != nil {
				t.Errorf("start %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	var sessions, gpu, units int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*),SUM(CASE WHEN node='z-gpu' THEN 1 ELSE 0 END) FROM placement_sessions WHERE retired=0`).Scan(&sessions, &gpu); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COALESCE(SUM(units),0) FROM placement_claims`).Scan(&units); err != nil {
		t.Fatal(err)
	}
	if sessions != starts || gpu != 1 || units != starts {
		t.Fatalf("sessions=%d GPU=%d units=%d", sessions, gpu, units)
	}
	// The durable route must identify the encoder from the winning target,
	// including CPU fallbacks after a rejected GPU reservation.
	for i := 0; i < starts; i++ {
		route, err := front.route(ctx, fmt.Sprintf("concurrent-%d", i))
		if err != nil {
			t.Fatal(err)
		}
		want := "cpu"
		if route.Fence.Target.NodeID == "z-gpu" {
			want = "h264_nvenc"
		}
		if route.Encoder != want {
			t.Errorf("target=%s encoder=%q want=%q", route.Fence.Target.NodeID, route.Encoder, want)
		}
	}
	var over int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT c.scope FROM placement_claims c JOIN placement_pools p ON p.scope=c.scope GROUP BY c.scope,p.capacity HAVING SUM(c.units)>p.capacity) exhausted`).Scan(&over); err != nil || over != 0 {
		t.Fatalf("over-reserved pools=%d err=%v", over, err)
	}
	t.Logf("concurrent starts=%d succeeded; GPU=%d CPU=%d charged=%d over-reserved=%d", sessions, gpu, sessions-gpu, units, over)
}

func checkPlacementRetryUncertainty(t *testing.T) {
	// Even a wrapped capacity error must not permit changing an operation whose
	// transaction outcome is unknown. No database is wired: rescoring is forbidden.
	r := &Runtime{}
	unknown := &placement.UnknownOutcome{Err: placement.ErrCapacity}
	calls := 0
	attempt := func(placement.Target, []placement.Claim, remotetranscode.Capabilities, string) error {
		calls++
		return unknown
	}
	err := r.retryTargetCapacity(context.Background(), core.HLSRequest{}, nodewire.Identity{}, "", placement.Target{NodeID: "gpu", Boot: "boot"}, nil, remotetranscode.Capabilities{}, "cpu", attempt)
	if err != unknown || calls != 1 {
		t.Fatalf("unknown outcome: calls=%d err=%v", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = r.retryTargetCapacity(ctx, core.HLSRequest{}, nodewire.Identity{}, "", placement.Target{}, nil, remotetranscode.Capabilities{}, "cpu", attempt)
	if !errors.Is(err, context.Canceled) || calls != 1 {
		t.Fatalf("deadline: calls=%d err=%v", calls, err)
	}
}
