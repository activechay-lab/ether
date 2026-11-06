package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"github.com/braedonsaunders/ether/internal/store"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/cluster"
	"github.com/braedonsaunders/ether/internal/clusterconfig"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/nodewire"
)

func TestPrivateIssuerUsesMembershipIncarnation(t *testing.T) {
	dir := t.TempDir()
	cfg, err := config.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	caPath, keyPath := privateIssuerFiles(t, dir)
	c := cluster.NewSolo(cluster.Local{Identity: cluster.Identity{NodeID: "node1"}}, nil)
	a := &App{cfg: cfg, cluster: c, deployment: &deploymentRuntime{settings: clusterconfig.Deployment{CAFile: caPath, CAKeyFile: keyPath, RootsFile: caPath, InternalAddress: "https://private.example:32401"}}}
	if err := a.buildPrivateCredentials(); err != nil {
		t.Fatal(err)
	}
	self := nodewire.Identity{Cluster: cfg.Get().Server.MachineIdentifier, NodeID: c.Self().ID, Boot: c.Self().Incarnation}
	tc, err := a.deployment.guard.ClientTLS(self, "private.example")
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(tc.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	got, err := nodewire.CertificateIdentity(cert)
	if err != nil || got != self {
		t.Fatalf("leaf membership mismatch: %v", err)
	}
	if _, err := a.privateMember(context.Background(), self); err != nil {
		t.Fatal(err)
	}
	self.Boot = "oldboot"
	if _, err := a.privateMember(context.Background(), self); err == nil {
		t.Fatal("stale incarnation accepted")
	}
	if a.privateProtocol() != 0 {
		t.Fatal("credentials alone advertised protocol")
	}
}

func privateIssuerFiles(t *testing.T, dir string) (string, string) {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	caPath, keyPath := filepath.Join(dir, "issuer.crt"), filepath.Join(dir, "issuer.key")
	if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), 0600); err != nil {
		t.Fatal(err)
	}

	return caPath, keyPath
}

// PostgreSQL owns the freshness clock; no host clock is changed by this test.
// Clock admission remains separately fenced after a step. Local heartbeat
// readiness and authenticated peer membership must not flap with it.
func TestPrivateMembershipTLSAcrossClockSteps(t *testing.T) {
	db := store.OpenTest(t)
	if store.DialectOf(db) != store.Postgres {
		t.Skip("requires ETHER_TEST_POSTGRES")
	}
	// Override only this throwaway schema's SQL clock, not the host clock.
	// Keep one pooled connection so the explicit test search_path is stable.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxIdleTime(0)
	for _, query := range []string{
		`CREATE TABLE test_clock(offset_ms BIGINT NOT NULL)`,
		`INSERT INTO test_clock VALUES(0)`,
		`CREATE FUNCTION clock_timestamp() RETURNS timestamptz LANGUAGE sql VOLATILE AS $$ SELECT pg_catalog.clock_timestamp() + offset_ms * INTERVAL '1 millisecond' FROM test_clock $$`,
		`SELECT set_config('search_path', current_schema() || ', pg_catalog', false)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cfg, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cfg.Close()
	var step atomic.Int64
	step.Store(int64(3 * time.Second))
	clock := func() time.Time { return time.Now().Add(time.Duration(step.Load())) }
	p, err := cluster.NewPostgres(cluster.Options{DB: db, Local: cluster.Local{Identity: cluster.Identity{NodeID: "clock-node"}}, Now: clock, ClockBoundKnown: true, ClockSkew: 10 * time.Second, HeartbeatInterval: time.Hour, LiveAfter: 20 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ca, key := privateIssuerFiles(t, t.TempDir())
	a := &App{cfg: cfg, cluster: p, deployment: &deploymentRuntime{settings: clusterconfig.Deployment{CAFile: ca, CAKeyFile: key, RootsFile: ca, InternalAddress: "https://127.0.0.1:32401", ClockSkewMillis: 10000}}}
	self := nodewire.Identity{Cluster: cfg.Get().Server.MachineIdentifier, NodeID: p.Self().ID, Boot: p.Self().Incarnation}
	snapshot, err := issuePrivateSnapshot(a.deployment.settings, self)
	if err != nil {
		t.Fatal(err)
	}
	guard, err := nodewire.NewGuard(snapshot, func(ctx context.Context, id nodewire.Identity, _ *x509.Certificate) error {
		_, err := a.privateMember(ctx, id)
		return err
	}, clock)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := guard.Request(r); err != nil {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	ts.TLS = guard.ServerTLS()
	ts.StartTLS()
	defer ts.Close()
	get := func() (int, error) {
		tc, err := guard.ClientTLS(self, "127.0.0.1")
		if err != nil {
			return 0, err
		}
		tr := &http.Transport{TLSClientConfig: tc, DisableKeepAlives: true}
		defer tr.CloseIdleConnections()
		resp, err := (&http.Client{Transport: tr, Timeout: 2 * time.Second}).Get(ts.URL)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return resp.StatusCode, nil
	}
	for _, offset := range []time.Duration{3 * time.Second, -3 * time.Second, 0} {
		step.Store(int64(offset)) // no heartbeat between these requests
		if _, err := a.privateMember(ctx, self); err != nil {
			t.Fatalf("membership step %s: %v", offset, err)
		}
		if err := a.privateSelfReady(self, &privateReadinessSnapshot{}); err != nil {
			t.Fatalf("heartbeat readiness step %s: %v", offset, err)
		}
		if status, err := get(); err != nil || status != 204 {
			t.Fatalf("TLS step %s: status %d, %v", offset, status, err)
		}
	}
	// Step PostgreSQL's observed clock backward between heartbeat writes.
	// Existing trusted last_seen values now appear three seconds in the future.
	if _, err := db.ExecContext(ctx, `UPDATE test_clock SET offset_ms=-3000`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.privateMember(ctx, self); err != nil {
		t.Fatalf("DB backward step changed membership: %v", err)
	}
	if err := a.privateSelfReady(self, &privateReadinessSnapshot{}); err != nil {
		t.Fatal(err)
	}
	if status, err := get(); err != nil || status != 204 {
		t.Fatalf("TLS after DB step: %d %v", status, err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE test_clock SET offset_ms=-60000`); err != nil {
		t.Fatal(err)
	}
	if _, err := a.privateMember(ctx, self); err == nil {
		t.Fatal("unbounded backward DB age accepted")
	}
	if _, err := db.ExecContext(ctx, `UPDATE test_clock SET offset_ms=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE cluster_nodes SET last_seen=last_seen-21000 WHERE id=?`, self.NodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.privateMember(ctx, self); err == nil {
		t.Fatal("genuinely stale membership accepted")
	}
	if status, err := get(); err == nil {
		t.Fatalf("TLS accepted stale peer: %d", status)
	}
}
