package plextv

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/remote"
)

func testPlexLeaf(t *testing.T, hash string, now time.Time, notAfter time.Time) (*x509.Certificate, *tls.Certificate, *x509.CertPool) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test plex.direct CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	name := "*." + hash + ".plex.direct"
	leafTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: name},
		DNSNames:     []string{name},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTmpl, ca, leafKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(leafDER)
	if err != nil {
		t.Fatal(err)
	}
	return leaf, &tls.Certificate{Certificate: [][]byte{leafDER, caDER}, PrivateKey: leafKey, Leaf: leaf}, pool
}

func installLeaf(m *CertManager, pair *tls.Certificate, leaf *x509.Certificate) {
	m.install(pair, leaf, "cert.pem", "key.pem", false)
}

func TestVerifyPlexDirectCertificate(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	const hash = "abc123def456abc123def456abc123de"
	leaf, pair, pool := testPlexLeaf(t, hash, now, now.Add(180*24*time.Hour))
	m := &CertManager{o: CertManagerOptions{Now: func() time.Time { return now }}, trustRoots: pool}
	installLeaf(m, pair, leaf)
	dials := 0
	m.setTrustDial(func(context.Context, string, string, *tls.Config) error {
		dials++
		return nil
	})
	ok, reason := m.VerifyTrust(context.Background(), hash, "x."+hash+".plex.direct", "x."+hash+".plex.direct:32400")
	if !ok || reason != "" || dials != 1 {
		t.Fatalf("trusted: ok=%v reason=%q dials=%d", ok, reason, dials)
	}
	info := m.Info()
	if !info.Verified || info.VerifyReason != "" {
		t.Fatalf("cached: %+v", info)
	}
	m.Info()
	if dials != 1 {
		t.Fatalf("status poll redialed: %d", dials)
	}

	ok, reason = m.VerifyTrust(context.Background(), "ffffffffffffffffffffffffffffffff", "x."+hash+".plex.direct", "x."+hash+".plex.direct:32400")
	if ok || reason != CertReasonIdentity || dials != 1 {
		t.Fatalf("identity: ok=%v reason=%q dials=%d", ok, reason, dials)
	}

	untrusted := &CertManager{o: CertManagerOptions{Now: func() time.Time { return now }}}
	installLeaf(untrusted, pair, leaf)
	untrusted.setTrustDial(func(context.Context, string, string, *tls.Config) error {
		t.Fatal("handshake ran for an untrusted chain")
		return nil
	})
	ok, reason = untrusted.VerifyTrust(context.Background(), hash, "x."+hash+".plex.direct", "x."+hash+".plex.direct:32400")
	if ok || reason != CertReasonChain {
		t.Fatalf("chain: ok=%v reason=%q", ok, reason)
	}

	expiredLeaf, expiredPair, expiredPool := testPlexLeaf(t, hash, now, now.Add(-time.Minute))
	expired := &CertManager{o: CertManagerOptions{Now: func() time.Time { return now }}, trustRoots: expiredPool}
	installLeaf(expired, expiredPair, expiredLeaf)
	ok, reason = expired.VerifyTrust(context.Background(), hash, "x."+hash+".plex.direct", "x."+hash+".plex.direct:32400")
	if ok || reason != CertReasonDates {
		t.Fatalf("dates: ok=%v reason=%q", ok, reason)
	}

	if ok, _ := m.VerifyTrust(context.Background(), hash, "x."+hash+".plex.direct", "x."+hash+".plex.direct:32400"); !ok || dials != 2 {
		t.Fatalf("re-verify: ok=%v dials=%d", ok, dials)
	}
	m.setTrustDial(func(context.Context, string, string, *tls.Config) error {
		dials++
		return context.DeadlineExceeded
	})
	// One failed handshake is not proof (a restart checks before its
	// listener serves): the earlier result stands until a second confirms.
	ok, reason = m.VerifyTrust(context.Background(), hash, "x."+hash+".plex.direct", "x."+hash+".plex.direct:32400")
	if !ok || reason != "" || dials != 4 || !m.trustPending(m.Info().Fingerprint) {
		t.Fatalf("first failure: ok=%v reason=%q dials=%d", ok, reason, dials)
	}
	ok, reason = m.VerifyTrust(context.Background(), hash, "x."+hash+".plex.direct", "x."+hash+".plex.direct:32400")
	if ok || reason != CertReasonHandshake || dials != 6 || m.trustPending(m.Info().Fingerprint) {
		t.Fatalf("handshake: ok=%v reason=%q dials=%d", ok, reason, dials)
	}
	if m.Info().Verified {
		t.Fatal("failed handshake stayed verified")
	}
}

// A restart checks the certificate before its listener serves. That refused
// dial must never become a warning: the next tick looks again and clears it.
func TestRegistrarStartupHandshakeIsRecheckedNotReported(t *testing.T) {
	now := time.Now()
	const hash = "abc123def456abc123def456abc123de"
	leaf, pair, roots := testPlexLeaf(t, hash, now, now.Add(time.Hour))
	m := &CertManager{trustRoots: roots}
	installLeaf(m, pair, leaf)
	serving := false
	m.setTrustDial(func(context.Context, string, string, *tls.Config) error {
		if !serving {
			return errors.New("connection refused")
		}
		return nil
	})
	cfg := testConfig()
	cfg.Plex.CertHash = hash
	r := &Registrar{o: RegistrarOptions{Config: &memConfig{cfg: cfg}, Certs: m, Now: time.Now, CertInterval: 7 * 24 * time.Hour}}
	r.refreshCertTrust(context.Background())
	if got := m.Info(); got.Verified || got.VerifyReason != "" {
		t.Fatalf("startup check reported %+v", got)
	}
	serving = true
	r.refreshCertTrust(context.Background())
	if got := m.Info(); !got.Verified {
		t.Fatalf("next tick did not clear the startup failure: %+v", got)
	}
}

func TestLocalPlexDirectDialPicksThePublishedLANAddress(t *testing.T) {
	hash := "abc123def456abc123def456abc123de"
	conns := remote.BuildConnections(remote.BuildOptions{
		LAN: []net.IP{net.ParseIP("192.168.1.5")}, WAN: net.ParseIP("203.0.113.7"), LocalPort: 32400, CertHash: hash,
	})
	got := localPlexDirectDial(conns)
	want := "192-168-1-5." + hash + ".plex.direct:32400"
	if got != want {
		t.Fatalf("dial %q, want %q", got, want)
	}
}

// The handshake check dials Ether's own listener first, with the plex.direct
// name for SNI, so no DNS lookup is needed. A router that blocks
// *.plex.direct names, or a Docker host without hairpin NAT, cannot make a
// working certificate look broken. The advertised address is the fallback
// (#251).
func TestVerifyTrustDialsOwnListenerFirst(t *testing.T) {
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	const hash = "abc123def456abc123def456abc123de"
	leaf, pair, pool := testPlexLeaf(t, hash, now, now.Add(180*24*time.Hour))
	m := &CertManager{o: CertManagerOptions{Now: func() time.Time { return now }}, trustRoots: pool}
	installLeaf(m, pair, leaf)
	host := "192-168-1-5." + hash + ".plex.direct"
	var addrs, names []string
	m.setTrustDial(func(_ context.Context, _, addr string, cfg *tls.Config) error {
		addrs, names = append(addrs, addr), append(names, cfg.ServerName)
		if addr == host+":32400" {
			return errors.New("no such host") // DNS rebinding protection
		}
		return nil
	})
	if ok, reason := m.VerifyTrust(context.Background(), hash, host, "127.0.0.1:32400", host+":32400"); !ok || reason != "" {
		t.Fatalf("own listener: ok=%v reason=%q", ok, reason)
	}
	if len(addrs) != 1 || addrs[0] != "127.0.0.1:32400" || names[0] != host {
		t.Fatalf("dialed %v with names %v", addrs, names)
	}
	// Own listener refused: the advertised address is still tried.
	addrs, names = nil, nil
	m.setTrustDial(func(_ context.Context, _, addr string, cfg *tls.Config) error {
		addrs = append(addrs, addr)
		if addr == "127.0.0.1:32400" {
			return errors.New("connection refused")
		}
		return nil
	})
	if ok, _ := m.VerifyTrust(context.Background(), hash, host, "127.0.0.1:32400", host+":32400"); !ok || addrs[len(addrs)-1] != host+":32400" {
		t.Fatalf("fallback: ok=%v dialed %v", ok, addrs)
	}
}

func TestRegistrarTrustWithoutAdvertisedHTTPS(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "no LAN", true: "HTTPS disabled"}[disabled], func(t *testing.T) {
			now := time.Now()
			const hash = "abc123def456abc123def456abc123de"
			leaf, pair, roots := testPlexLeaf(t, hash, now, now.Add(time.Hour))
			m := &CertManager{trustRoots: roots}
			installLeaf(m, pair, leaf)
			ln, err := tls.Listen("tcp", "127.0.0.1:0", m.TLSConfig())
			if err != nil {
				t.Fatal(err)
			}
			defer ln.Close()
			go func() {
				for {
					c, err := ln.Accept()
					if err != nil {
						return
					}
					_ = c.(*tls.Conn).Handshake()
					_ = c.Close()
				}
			}()
			cfg := testConfig()
			cfg.Server.Port = ln.Addr().(*net.TCPAddr).Port
			cfg.Plex.CertHash = hash
			if disabled {
				cfg.Plex.SecureConnections = remote.SecureDisabled
			}
			r := &Registrar{o: RegistrarOptions{Config: &memConfig{cfg: cfg}, Certs: m, Now: time.Now}}
			if disabled {
				r.lastLAN = []string{"192.168.1.5"}
			}
			r.refreshCertTrust(context.Background())
			if got := m.Info(); !got.Verified {
				t.Fatalf("local TLS check: %+v", got)
			}
		})
	}
}
