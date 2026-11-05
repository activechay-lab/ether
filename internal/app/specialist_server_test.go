package app

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestServerRoleConstructionBoundary(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	ca, key := privateIssuerFiles(t, dir)
	a := &App{opts: Options{DataDir: dir}, cfg: cfg, db: store.OpenTest(t), log: slog.New(slog.NewTextHandler(io.Discard, nil)), deployment: &deploymentRuntime{settings: clusterconfig.Deployment{Roles: config.NodeRoles{config.RoleServer}, LedgerCapacity: 4096, Capacity: 1, ClockBoundKnown: true, CAFile: ca, CAKeyFile: key, RootsFile: ca, InternalAddress: "https://127.0.0.1:32401"}}}
	a.perf = newPerfObserver(a.log)
	a.closers = append(a.closers, a.perf.close)
	if err = a.buildCluster(store.SQLite); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.buildServerRole(context.Background(), cfg.Get()); err != nil {
		t.Fatal(err)
	}
	if a.frontends == nil || a.frontends.broker == nil || a.brokerPlayback == nil || a.brokerTranscoder == nil || a.registrar == nil || a.handler == nil {
		t.Fatal("real server dependencies missing")
	}
	if a.fetcher != nil || a.engine != nil || a.cache != nil || a.indexers != nil || a.debrid != nil || a.media != nil || a.transcoder != nil || a.deployment.encoder != nil {
		t.Fatal("server constructed source transport or encoder")
	}
	if !a.deployment.playback.Ready() {
		t.Fatal("broker delivery binding missing")
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "http://localhost:32400/ether/api/bootstrap", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	a.handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatalf("owner console bootstrap unavailable: %d", response.Code)
	}
	if a.privateProtocol() != 0 {
		t.Fatal("constructed graph advertised before listeners")
	}
}
