package sourceadmin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/nodewire"
)

func guards(t *testing.T, policy nodewire.Policy) (*nodewire.Guard, *nodewire.Guard, nodewire.Identity, nodewire.Identity) {
	t.Helper()
	now := time.Now().Add(-time.Minute)
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: now, NotAfter: now.Add(24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	root := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	makeGuard := func(id nodewire.Identity, p nodewire.Policy) *nodewire.Guard {
		_, k, _ := ed25519.GenerateKey(rand.Reader)
		csr, _ := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, k)
		cert, e := nodewire.Issue(der, key, csr, id, []string{"127.0.0.1"}, now, time.Hour, 0)
		if e != nil {
			t.Fatal(e)
		}
		pk, _ := x509.MarshalPKCS8PrivateKey(k)
		s, e := nodewire.NewSnapshot(id, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk}), root, time.Now())
		if e != nil {
			t.Fatal(e)
		}
		g, e := nodewire.NewGuard(s, p, time.Now)
		if e != nil {
			t.Fatal(e)
		}
		return g
	}
	a := nodewire.Identity{Cluster: "cluster", NodeID: "server", Boot: "serverboot"}
	b := nodewire.Identity{Cluster: "cluster", NodeID: "source", Boot: "sourceboot"}
	return makeGuard(a, policy), makeGuard(b, func(context.Context, nodewire.Identity, *x509.Certificate) error { return nil }), a, b
}

// This covers composition invariants rather than individual service behavior:
// real TLS, per-operation owner/current-role checks, secret isolation, bounds,
// missing dependencies and cooperative cancellation.
func TestPrivateProtocol(t *testing.T) {
	var revoked, rolesDenied, policyBlocked atomic.Bool
	cg, sg, caller, local := guards(t, func(ctx context.Context, _ nodewire.Identity, _ *x509.Certificate) error {
		if policyBlocked.Load() {
			<-ctx.Done()
			return ctx.Err()
		}
		if revoked.Load() {
			return ErrDenied
		}
		return nil
	})
	var calls, owners atomic.Int64
	var mode atomic.Int64
	h, err := NewHandler(Options{Guard: sg, Local: local, Timeout: time.Second,
		VerifyOwner: func(_ context.Context, s string) error {
			owners.Add(1)
			if s != "original-owner-secret" {
				return errors.New("sensitive-owner-token")
			}
			return nil
		},
		Authorize: func(_ context.Context, a, b nodewire.Identity, r Request) error {
			if a != caller || b != local || rolesDenied.Load() || r.ResourceID != "provider1" {
				return ErrDenied
			}
			return nil
		},
		Services: Services{TestProvider: func(ctx context.Context, _ string) error {
			calls.Add(1)
			switch mode.Load() {
			case 1:
				return errors.New("https://user:password@host/?apikey=secret")
			case 2:
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewUnstartedServer(h)
	ts.TLS = sg.ServerTLS()
	ts.StartTLS()
	defer ts.Close()
	c, err := NewClient(ClientOptions{Guard: cg, Peer: local, Endpoint: ts.URL, Timeout: time.Second, Authorize: func(context.Context, nodewire.Identity, Request) error { return nil }})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseIdleConnections()
	r := Request{Operation: TestProvider, RequestID: "req1", ResourceID: "provider1"}
	var traced atomic.Bool
	ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{WroteHeaderField: func(string, []string) { traced.Store(true) }})
	if v, e := c.Call(ctx, "original-owner-secret", r); e != nil || !v.Tested {
		t.Fatal(v, e)
	}
	if traced.Load() {
		t.Fatal("upstream trace observed private headers")
	}
	if _, e := c.Call(ctx, "viewer-secret", r); e != ErrDenied || calls.Load() != 1 {
		t.Fatal("viewer reached service", e)
	}
	rolesDenied.Store(true)
	if _, e := c.Call(ctx, "original-owner-secret", r); e != ErrDenied || calls.Load() != 1 {
		t.Fatal("stale role authorized", e)
	}
	rolesDenied.Store(false)
	mode.Store(1)
	if _, e := c.Call(ctx, "original-owner-secret", r); e != ErrUnavailable {
		t.Fatal("raw dependency error", e)
	}
	r.Operation = TestIndexer
	if _, e := c.Call(ctx, "original-owner-secret", r); e != ErrUnsupported {
		t.Fatal("missing dependency", e)
	}
	r.Operation = TestProvider
	revoked.Store(true)
	before := owners.Load()
	if _, e := c.Call(ctx, "original-owner-secret", r); e != ErrUnavailable || owners.Load() != before {
		t.Fatal("revoked peer received credential", e)
	}
	revoked.Store(false)
	policyBlocked.Store(true)
	policyCtx, policyCancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	startPolicy := time.Now()
	if _, e := c.Call(policyCtx, "original-owner-secret", r); e != ErrUnavailable || time.Since(startPolicy) > 500*time.Millisecond || owners.Load() != before {
		t.Fatal("policy cancellation lost or headers released", e)
	}
	policyCancel()
	policyBlocked.Store(false)
	mode.Store(2)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := c.Call(ctx, "original-owner-secret", r); e != ErrUnavailable || time.Since(start) > 500*time.Millisecond {
		t.Fatal("cancellation lost", e)
	}
	// Exercise strict server parsing and response projection with the same mTLS.
	tc, _ := cg.ClientTLS(local, "127.0.0.1")
	tr := &http.Transport{TLSClientConfig: tc}
	defer tr.CloseIdleConnections()
	hc := &http.Client{Transport: tr, Timeout: time.Second}
	for _, body := range []string{
		`{"operation":"test-provider","requestID":"r","resourceID":"provider1","url":"https://evil"}`,
		`{"operation":"snapshot","requestID":"r","payload":{"limit":101}}`,
		strings.Repeat("x", maxRequest+1),
	} {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+Path, strings.NewReader(body))
		req.Header.Set(credentialHeader, "original-owner-secret")
		res, e := hc.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		data, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 400 || strings.Contains(string(data), "secret") {
			t.Fatal(res.StatusCode, string(data))
		}
	}
}

func TestResponseBounds(t *testing.T) {
	r := Request{Operation: Snapshot, RequestID: "r", Payload: &Query{Limit: 1}}
	if validResponse(r, Response{Diagnostics: &Diagnostics{Providers: []Provider{{State: Status("https://key:secret@host")}}}}) {
		t.Fatal("open response string")
	}
	if validResponse(r, Response{Diagnostics: &Diagnostics{Providers: []Provider{{State: Healthy}, {State: Healthy}}}}) {
		t.Fatal("response exceeds requested limit")
	}
	if validResponse(Request{Operation: TestProvider}, Response{Releases: []Release{{State: Healthy}}}) {
		t.Fatal("wrong response shape")
	}
}
