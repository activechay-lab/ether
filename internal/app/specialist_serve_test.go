package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/clusterplayback"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/nodewire"
	"github.com/braedonsaunders/ether/internal/placement"
	"github.com/braedonsaunders/ether/internal/store"
)

type supervisorMembership struct{ cluster.Cluster }

func (supervisorMembership) Start(context.Context) error { return nil }

type supervisorEncoder struct{}

func (supervisorEncoder) StartHLSOwned(context.Context, core.HLSRequest) (core.HLSSession, core.EncoderRetirement, error) {
	return nil, nil, core.ErrNotAvailable
}

// Exercise the executable Serve supervisor through private-listener startup,
// cancellation, drain and Close with explicit offline worker dependencies.
// Encoding acceptance belongs to the runtime lab; this fixture starts no child.
func TestSpecialistExecutableSupervisorLifecycle(t *testing.T) {
	testSpecialistExecutableSupervisor(t, config.RoleTranscode)
}

func TestSourceExecutableSupervisorLifecycle(t *testing.T) {
	testSpecialistExecutableSupervisor(t, config.RoleSource)
}

func testSpecialistExecutableSupervisor(t *testing.T, role config.NodeRole) {
	t.Helper()
	dir := t.TempDir()
	db := store.OpenTest(t)
	ca, key := privateIssuerFiles(t, dir)
	created := make(chan *App, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- serveWithConstructor(ctx, Options{DataDir: dir, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), NotificationExecutable: "must-never-spawn-a-monitor"}, func(_ context.Context, opts Options) (*App, error) {
			cfg, err := config.Open(dir)
			if err != nil {
				return nil, err
			}
			roles := config.NodeRoles{role}
			if err = cfg.Update(func(c *config.Config) error { c.Server.NodeRoles = roles; return nil }); err != nil {
				return nil, err
			}
			a := &App{opts: opts, cfg: cfg, db: db, log: opts.Logger, deployment: &deploymentRuntime{settings: clusterconfig.Deployment{Roles: roles, LedgerCapacity: 4096, Capacity: 1, ClockBoundKnown: true, PrivateListen: "127.0.0.1:0", InternalAddress: "https://127.0.0.1:32401", CAFile: ca, CAKeyFile: key, RootsFile: ca, DrainSeconds: 1}, errors: make(chan error, 3)}}
			a.closers = append(a.closers, cfg.Close)
			t.Cleanup(a.Close)
			if role == config.RoleSource {
				if err := a.buildCluster(store.SQLite); err != nil {
					return nil, err
				}
				a.cluster = supervisorMembership{a.cluster}
				probePath, err := os.Executable()
				if err != nil {
					return nil, err
				}
				sourceCfg := cfg.Get()
				sourceCfg.Transcode.FFprobePath = probePath
				if err := a.buildSourceRole(ctx, sourceCfg); err != nil {
					return nil, err
				}
				if err := a.buildPrivateCredentials(); err != nil {
					return nil, err
				}
				if err := a.buildClusterPlayback(ctx, cfg.Get()); err != nil {
					return nil, err
				}
				created <- a
				return a, nil
			}
			a.cluster = supervisorMembership{cluster.NewSolo(cluster.Local{Identity: cluster.Identity{NodeID: "worker"}, Roles: roles, Protocol: a.privateProtocol, Caps: func() cluster.Caps { return cluster.Caps{Transcode: true} }}, opts.Logger)}
			if err = a.buildPrivateCredentials(); err != nil {
				return nil, err
			}
			verifier := clusterplayback.NewVerifier()
			ledger, err := placement.New(db, placement.Options{MaxSessions: 32, MaxLease: 10 * time.Minute, LockTimeout: time.Second}, verifier)
			if err != nil {
				return nil, err
			}
			if err = ledger.EnsureLedgerQuota(ctx); err != nil {
				return nil, err
			}
			self := a.cluster.Self()
			runtime, err := clusterplayback.NewRuntime(clusterplayback.Options{SurfacePolicy: sessionPolicy, Delivery: deliveryPolicy(), DB: db, Guard: a.deployment.guard, Self: nodewire.Identity{Cluster: cfg.Get().Server.MachineIdentifier, NodeID: self.ID, Boot: self.Incarnation}, Roles: roles, Placement: ledger, Endpoint: a.privateEndpoint, Eligible: a.privateEligible, MemberRole: a.privateMemberRole, Encoder: supervisorEncoder{}, Capacity: 1, ClockBoundKnown: true})
			if err != nil {
				return nil, err
			}
			if err = verifier.Bind(runtime); err != nil {
				return nil, err
			}
			a.deployment.playback = runtime
			a.deployment.placement = ledger
			a.closers = append(a.closers, func() { _ = runtime.Close() })
			created <- a
			return a, nil
		})
	}()
	var a *App
	select {
	case a = <-created:
	case err := <-result:
		t.Fatalf("supervisor failed before construction: %v", err)
	case <-ctx.Done():
		t.Fatal("construction timed out")
	}
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !a.deployment.started.Load() {
		select {
		case err := <-result:
			t.Fatalf("specialist startup failed: %v", err)
		case <-ctx.Done():
			t.Fatal("startup timed out")
		case <-tick.C:
		}
	}
	if a.notifications != nil || a.frontends != nil || a.registrar != nil {
		t.Fatal("specialist supervisor constructed unrelated services")
	}
	if a.privateProtocol() != 1 {
		t.Fatal("real private worker did not become ready")
	}
	cancel()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("specialist supervisor failed to stop")
	}
	if a.privateProtocol() != 0 {
		t.Fatal("stopped worker advertised protocol")
	}
}
