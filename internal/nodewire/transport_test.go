package nodewire

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

type issuer struct {
	der, pem []byte
	key      ed25519.PrivateKey
}

func ca(t *testing.T, now time.Time) issuer {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	c := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "offline"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(48 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, c, c, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	return issuer{der, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key}
}
func credential(t *testing.T, c issuer, id Identity, now time.Time) ([]byte, []byte) {
	t.Helper()
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	_ = pub
	der, e := Issue(c.der, c.key, csr, id, []string{"127.0.0.1"}, now, time.Hour, 0)
	if e != nil {
		t.Fatal(e)
	}
	k, _ := x509.MarshalPKCS8PrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: k})
}
func snapshot(t *testing.T, c issuer, id Identity, now time.Time) *Snapshot {
	t.Helper()
	cert, key := credential(t, c, id, now)
	s, e := NewSnapshot(id, cert, key, c.pem, now)
	if e != nil {
		t.Fatal(e)
	}
	return s
}
func TestTLSAndLiveRotation(t *testing.T) {
	now := time.Now()
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	clockFn := func() time.Time { return time.Unix(0, clock.Load()) }
	a := ca(t, now)
	b := ca(t, now)
	server := Identity{"cluster", "source", "serverboot"}
	client := Identity{"cluster", "worker", "workerboot"}
	var revoked atomic.Bool
	policy := func(_ context.Context, id Identity, _ *x509.Certificate) error {
		if id != client || revoked.Load() {
			return ErrDenied
		}
		return nil
	}
	sg, _ := NewGuard(snapshot(t, a, server, now), policy, clockFn)
	cg, _ := NewGuard(snapshot(t, a, client, now), func(context.Context, Identity, *x509.Certificate) error { return nil }, clockFn)
	var connections atomic.Int64
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, e := sg.Request(r); e != nil {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	ts.TLS = sg.ServerTLS()
	ts.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			connections.Add(1)
		}
	}
	ts.StartTLS()
	defer ts.Close()
	config, e := cg.ClientTLS(server, "127.0.0.1")
	if e != nil {
		t.Fatal(e)
	}
	tr := &http.Transport{TLSClientConfig: config}
	defer tr.CloseIdleConnections()
	h := &http.Client{Transport: tr, Timeout: time.Second}
	get := func(want int) {
		t.Helper()
		r, e := h.Get(ts.URL)
		if e != nil {
			t.Fatal(e)
		}
		r.Body.Close()
		if r.StatusCode != want {
			t.Fatalf("status %d want %d", r.StatusCode, want)
		}
	}
	get(204)
	revoked.Store(true)
	get(403)
	revoked.Store(false)
	get(204)
	if connections.Load() != 1 {
		t.Fatal("fixture did not reuse connection")
	}
	// Existing connection fails current trust after atomic replacement.
	if e := sg.Replace(snapshot(t, b, server, now)); e != nil {
		t.Fatal(e)
	}
	get(403)
	// Rotate both sides and rebuild client transport; new handshake uses new roots.
	if e := cg.Replace(snapshot(t, b, client, now)); e != nil {
		t.Fatal(e)
	}
	tr.CloseIdleConnections()
	config, _ = cg.ClientTLS(server, "127.0.0.1")
	tr = &http.Transport{TLSClientConfig: config}
	defer tr.CloseIdleConnections()
	h.Transport = tr
	get(204)
	clock.Store(now.Add(2 * time.Hour).UnixNano())
	get(403)
}
func TestBindingAndIsolation(t *testing.T) {
	now := time.Now()
	c := ca(t, now)
	local := Identity{"cluster", "local", "boot"}
	peer := Identity{"cluster", "peer", "peerboot"}
	s := snapshot(t, c, local, now)
	g, _ := NewGuard(s, func(context.Context, Identity, *x509.Certificate) error { return nil }, func() time.Time { return now })
	cert, key := credential(t, c, peer, now)
	pair, _ := tls.X509KeyPair(cert, key)
	leaf, _ := x509.ParseCertificate(pair.Certificate[0])
	cs := tls.ConnectionState{HandshakeComplete: true, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
	if e := g.RecheckServer(context.Background(), cs, peer, "127.0.0.1"); e != nil {
		t.Fatal(e)
	}
	for _, id := range []Identity{{"wrong", "peer", "peerboot"}, {"cluster", "peer", "oldboot"}, {"cluster", "other", "peerboot"}} {
		if g.RecheckServer(context.Background(), cs, id, "127.0.0.1") == nil {
			t.Fatal("accepted wrong peer")
		}
	}
	if g.RecheckServer(context.Background(), cs, peer, "wrong.example") == nil {
		t.Fatal("accepted wrong hostname")
	}
	config, _ := g.ClientTLS(peer, "127.0.0.1")
	config.Certificates[0].Certificate[0][0] ^= 0xff
	config.RootCAs.AddCert(leaf)
	other, _ := g.ClientTLS(peer, "127.0.0.1")
	if _, e := x509.ParseCertificate(other.Certificates[0].Certificate[0]); e != nil {
		t.Fatal("shared certificate bytes")
	}
	for _, raw := range []string{"ether-node://identity/v1/cluster/peer/peerboot?x=1", "ether-node://identity/v1/cluster/peer/peerboot#x", "ether-node://user@identity/v1/cluster/peer/peerboot", "ether-node://identity/v1/cl%75ster/peer/peerboot", "ether-node://identity/v1/cluster/../peerboot"} {
		u, _ := url.Parse(raw)
		copy := *leaf
		copy.URIs = []*url.URL{u}
		if _, e := CertificateIdentity(&copy); e == nil {
			t.Fatalf("accepted alias %s", raw)
		}
	}
	copy := *leaf
	copy.URIs = append(copy.URIs, copy.URIs[0])
	if _, e := CertificateIdentity(&copy); e == nil {
		t.Fatal("multiple identities")
	}
	if _, e := NewGuard(s, nil, time.Now); e == nil {
		t.Fatal("nil policy")
	}
}

func TestTLSClockStepsRespectCertificateValidity(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	c := ca(t, now)
	var clock atomic.Int64
	clock.Store(now.UnixNano())
	clockFn := func() time.Time { return time.Unix(0, clock.Load()) }
	makeSnapshot := func(id Identity) *Snapshot {
		_, key, _ := ed25519.GenerateKey(rand.Reader)
		csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
		if err != nil {
			t.Fatal(err)
		}
		der, err := Issue(c.der, c.key, csr, id, []string{"127.0.0.1"}, now, MaxLifetime, 10*time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		if !leaf.NotBefore.Equal(now.Add(-10*time.Minute)) || leaf.NotAfter.Sub(leaf.NotBefore) > MaxLifetime {
			t.Fatal("invalid backdated validity window")
		}
		private, _ := x509.MarshalPKCS8PrivateKey(key)
		certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
		keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
		var s *Snapshot
		for _, step := range []time.Duration{-3 * time.Second, 3 * time.Second} {
			s, err = NewSnapshot(id, certPEM, keyPEM, c.pem, now.Add(step))
			if err != nil {
				t.Fatalf("snapshot at %s: %v", step, err)
			}
		}
		return s
	}
	server, client := Identity{"cluster", "server", "boot"}, Identity{"cluster", "client", "boot"}
	allow := func(context.Context, Identity, *x509.Certificate) error { return nil }
	sg, err := NewGuard(makeSnapshot(server), allow, clockFn)
	if err != nil {
		t.Fatal(err)
	}
	cg, err := NewGuard(makeSnapshot(client), allow, clockFn)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := sg.Request(r); err != nil {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	ts.TLS = sg.ServerTLS()
	ts.StartTLS()
	defer ts.Close()
	leaf := sg.current.Load().credential().Leaf
	for _, at := range []time.Time{now.Add(-3 * time.Second), now.Add(3 * time.Second), leaf.NotBefore.Add(-time.Second), leaf.NotAfter.Add(time.Second)} {
		clock.Store(at.UnixNano())
		config, err := cg.ClientTLS(server, "127.0.0.1")
		if err != nil {
			t.Fatal(err)
		}
		tr := &http.Transport{TLSClientConfig: config, DisableKeepAlives: true}
		h := &http.Client{Transport: tr, Timeout: 2 * time.Second}
		r, err := h.Get(ts.URL)
		tr.CloseIdleConnections()
		outside := at.Before(leaf.NotBefore) || at.After(leaf.NotAfter)
		if r != nil {
			r.Body.Close()
		}
		if outside {
			if err == nil {
				t.Fatal("TLS accepted a certificate outside its real validity")
			}
		} else if err != nil || r.StatusCode != 204 {
			t.Fatalf("TLS after clock step %v: %v", at.Sub(now), err)
		}
	}
}
