package app

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestDeliveryRoleConstructionBoundary(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	ca, key := privateIssuerFiles(t, dir)
	a := &App{opts: Options{DataDir: dir}, cfg: cfg, db: store.OpenTest(t), log: slog.New(slog.NewTextHandler(io.Discard, nil)), deployment: &deploymentRuntime{settings: clusterconfig.Deployment{Roles: config.NodeRoles{config.RoleDelivery}, LedgerCapacity: 4096, Capacity: 1, ClockBoundKnown: true, CAFile: ca, CAKeyFile: key, RootsFile: ca, InternalAddress: "https://127.0.0.1:32401"}}}
	if err = a.buildCluster(store.SQLite); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err = a.buildDeliveryRole(context.Background(), cfg.Get()); err != nil {
		t.Fatal(err)
	}
	if a.handler == nil || a.users == nil || !a.deployment.playback.Ready() {
		t.Fatal("real public delivery dependencies absent")
	}
	if a.catalog != nil || a.frontends != nil || a.registrar != nil || a.playback != nil || a.fetcher != nil || a.resolver != nil || a.cache != nil || a.transcoder != nil || a.deployment.encoder != nil {
		t.Fatal("delivery constructed another role service")
	}
	for _, path := range []string{"/library/metadata/1", "/ether/", "/video/:/transcode/universal/start.m3u8"} {
		response := httptest.NewRecorder()
		a.handler.ServeHTTP(response, httptest.NewRequest("GET", path, nil))
		if response.Code != 404 {
			t.Fatalf("non-delivery path mounted %s: %d", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	a.handler.ServeHTTP(response, httptest.NewRequest("GET", "/video/:/transcode/universal/session/id/base/init.mp4", nil))
	if response.Code != 401 {
		t.Fatalf("unauthenticated Plex forwarding admitted: %d", response.Code)
	}
	if a.privateServicesReady() {
		t.Fatal("delivery ready before public listener installation")
	}
	if a.privateProtocol() != 0 {
		t.Fatal("delivery advertised before listeners bound")
	}
}

func TestDeliveryPlexUsesShippingTokenParserAndLiveReceipt(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	primary, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer primary.Close()
	account := plextv.Account{ID: 142, Username: "lab-owner"}
	if err = primary.SetOwner(ctx, account, "old-lab-token"); err != nil {
		t.Fatal(err)
	}
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer users.Close()
	policy, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer policy.Close()
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	a := &App{db: db, cfg: cfg, users: users, safety: policy}
	for _, uri := range []string{
		"/video/:/transcode/universal/session/id/base/init.mp4?X-Plex-Token=old-lab-token",
		"/video/:/transcode/universal/session/id/base/init.mp4?x-plex-token=old-lab-token",
	} {
		r := httptest.NewRequest("GET", uri, nil)
		r.RemoteAddr = "127.0.0.1:12345"
		if err := a.authorizeDeliveryPlex(ctx, r, "id"); err != nil {
			t.Fatalf("shipping query token denied: %v", err)
		}
		r.Header.Set("X-Plex-Token", "invalid-header-token")
		if err := a.authorizeDeliveryPlex(ctx, r, "id"); err == nil {
			t.Fatal("query token overrode invalid header")
		}
	}
	if err = primary.SetOwner(ctx, account, "new-lab-token"); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/video/:/transcode/universal/session/id/base/init.mp4?X-Plex-Token=old-lab-token", nil)
	r.RemoteAddr = "127.0.0.1:12345"
	if err := a.authorizeDeliveryPlex(ctx, r, "id"); err == nil {
		t.Fatal("revoked cached query token admitted")
	}
}
