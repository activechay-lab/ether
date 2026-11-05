package app

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
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
