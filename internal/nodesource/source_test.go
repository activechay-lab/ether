package nodesource

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/placement"
)

type testAuthority struct {
	mu      sync.Mutex
	grants  map[string]Grant
	revoked bool
}

func (a *testAuthority) Verify(_ context.Context, p Peer, key string) (Grant, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	g, ok := a.grants[key]
	if !ok || a.revoked || p != (Peer{g.Scope.Fence.Target.NodeID, g.Scope.Fence.Target.Boot}) {
		return Grant{}, errors.New("denied")
	}
	return g, nil
}

type testSource struct {
	data []byte
	read func(context.Context, []byte, int64) (int, error)
}

func (s *testSource) ReadAt(b []byte, o int64) (int, error) {
	return bytes.NewReader(s.data).ReadAt(b, o)
}
func (s *testSource) ReadAtContext(c context.Context, b []byte, o int64) (int, error) {
	if s.read != nil {
		return s.read(c, b, o)
	}
	return s.ReadAt(b, o)
}
func (s *testSource) Name() string      { return "secret-provider-name" }
func (s *testSource) ReleaseID() string { return "secret-provider-url" }
func (s *testSource) Size() int64       { return int64(len(s.data)) }
func (s *testSource) Close() error      { panic("transport must not close source") }
func certs(t *testing.T) (tls.Certificate, tls.Certificate, *x509.CertPool) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, e := x509.CreateCertificate(rand.Reader, ca, ca, pub, key)
	if e != nil {
		t.Fatal(e)
	}
	root, e := x509.ParseCertificate(der)
	if e != nil {
		t.Fatal(e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	leaf := func(n int64, name string, usage x509.ExtKeyUsage) tls.Certificate {
		p, k, e := ed25519.GenerateKey(rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		c := &x509.Certificate{SerialNumber: big.NewInt(n), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name}, NotBefore: ca.NotBefore, NotAfter: ca.NotAfter, ExtKeyUsage: []x509.ExtKeyUsage{usage}, KeyUsage: x509.KeyUsageDigitalSignature}
		d, e := x509.CreateCertificate(rand.Reader, c, root, p, key)
		if e != nil {
			t.Fatal(e)
		}
		return tls.Certificate{Certificate: [][]byte{d, der}, PrivateKey: k}
	}
	return leaf(2, "source", x509.ExtKeyUsageServerAuth), leaf(3, "worker", x509.ExtKeyUsageClientAuth), pool
}

type fixture struct {
	h        *Handler
	c        *Client
	a        *testAuthority
	g        Grant
	source   *testSource
	released atomic.Int32
	server   *httptest.Server
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{source: &testSource{data: []byte("0123456789abcdef")}}
	f.g = Grant{Scope: Scope{Fence: placement.Fence{Session: "session", Generation: 7, Controller: placement.Controller{ID: "controller", Boot: "cboot"}, Target: placement.Target{NodeID: "worker", Boot: "wboot"}}, Viewer: "viewer", Revision: "revision"}, Credential: "opaque", Expires: time.Now().Add(time.Minute)}
	f.a = &testAuthority{grants: map[string]Grant{"opaque": f.g}}
	var e error
	f.h, e = NewHandler(Options{Authority: f.a, Peer: func(c *x509.Certificate) (Peer, error) {
		if c.Subject.CommonName != "worker" {
			return Peer{}, errors.New("wrong peer")
		}
		return Peer{"worker", "wboot"}, nil
	}, Acquire: func(_ context.Context, s Scope) (Hold, error) {
		return Hold{s, f.source, func() { f.released.Add(1) }}, nil
	}, Now: time.Now, MaxRequests: 8, MaxLifetime: 2 * time.Minute, Revalidate: time.Millisecond})
	if e != nil {
		t.Fatal(e)
	}
	serverCert, clientCert, pool := certs(t)
	f.server = httptest.NewUnstartedServer(f.h)
	f.server.TLS = &tls.Config{Certificates: []tls.Certificate{serverCert}, ClientCAs: pool, ClientAuth: tls.RequireAndVerifyClientCert}
	f.server.StartTLS()
	t.Cleanup(f.server.Close)
	f.c, e = NewClient(ClientOptions{Endpoint: f.server.URL + Path, TLS: &tls.Config{RootCAs: pool, Certificates: []tls.Certificate{clientCert}, ServerName: "source"}, BindServer: func(s tls.ConnectionState) error {
		if s.PeerCertificates[0].Subject.CommonName != "source" {
			return errors.New("wrong source")
		}
		return nil
	}, Peer: Peer{"worker", "wboot"}, Now: time.Now, MaxLifetime: 2 * time.Minute, Timeout: time.Second})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(f.c.Close)
	return f
}
func TestRangesHEADConcurrentAndRevocation(t *testing.T) {
	f := setup(t)
	r, e := f.c.Open(context.Background(), f.g)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	if r.Size() != 16 {
		t.Fatal(r.Size())
	}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b := make([]byte, 3)
			n, e := r.ReadAt(b, 5)
			if e != nil || n != 3 || string(b) != "567" {
				t.Errorf("range %q %d %v", b, n, e)
			}
		}()
	}
	wg.Wait()
	b := make([]byte, 3)
	if n, e := r.ReadAt(b, 15); n != 1 || e != io.EOF || b[0] != 'f' {
		t.Fatalf("tail %d %v %q", n, e, b)
	}
	res, e := f.c.request(context.Background(), f.g, "GET", "bytes=99-100")
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != 416 || res.Header.Get("Content-Range") != "bytes */16" {
		t.Fatal(res.StatusCode, res.Header)
	}
	f.a.mu.Lock()
	f.a.revoked = true
	f.a.mu.Unlock()
	if _, e := r.ReadAt(b, 0); e == nil {
		t.Fatal("revoked source read")
	}
	if n := f.h.Reconcile(); n != 0 {
		t.Fatal("pending", n)
	}
}
func TestCancellationAndAcquireErrorRetainExactHold(t *testing.T) {
	f := setup(t)
	r, e := f.c.Open(context.Background(), f.g)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	entered := make(chan struct{})
	var producerDone func()
	f.source.read = func(ctx context.Context, _ []byte, _ int64) (int, error) {
		var ok bool
		producerDone, ok = core.ReadRetirementFrom(ctx).Begin()
		if !ok {
			panic("sealed")
		}
		close(entered)
		<-ctx.Done()
		return 0, ctx.Err()
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _, _ = r.ReadAtContext(ctx, make([]byte, 2), 0) }()
	<-entered
	cancel()
	<-done
	// Wait for the handler's synchronous source read to exit without sleeping.
	// Server.Close waits for handlers after closing active connections.
	f.server.Close()
	if n := f.h.Reconcile(); n != 1 {
		t.Fatalf("lost producer hold: %d", n)
	}
	before := f.released.Load()
	producerDone()
	if n := f.h.Reconcile(); n != 0 || f.released.Load() != before+1 {
		t.Fatal("exact hold not released")
	}
	var pendingRelease func()
	f.h.o.Acquire = func(ctx context.Context, s Scope) (Hold, error) {
		pendingRelease, _ = core.ReadRetirementFrom(ctx).Begin()
		return Hold{s, f.source, func() { f.released.Add(1) }}, context.Canceled
	}
	req := httptest.NewRequest("HEAD", Path, nil)
	req.Header.Set(CredentialHeader, f.g.Credential)
	// Reuse a verified state captured by the actual TLS client connection.
	req.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}, PeerCertificates: []*x509.Certificate{{Subject: pkix.Name{CommonName: "worker"}}}}
	w := httptest.NewRecorder()
	f.h.ServeHTTP(w, req)
	if w.Code != 503 || f.h.Reconcile() != 1 {
		t.Fatal("error acquisition lost hold", w.Code)
	}
	pendingRelease()
	if f.h.Reconcile() != 0 {
		t.Fatal("error hold stuck")
	}
}
func TestFencesRenewalAndChangedRepresentation(t *testing.T) {
	f := setup(t)
	r, e := f.c.Open(context.Background(), f.g)
	if e != nil {
		t.Fatal(e)
	}
	defer r.Close()
	wrong := f.g
	wrong.Scope.Fence.Generation++
	f.a.mu.Lock()
	f.a.grants["wrong"] = wrong
	f.a.mu.Unlock()
	wrong.Credential = "wrong"
	if _, e := f.c.Open(context.Background(), wrong); e == nil {
		t.Fatal("credential mismatch accepted")
	}
	wrong = f.g
	wrong.Scope.Fence.Target.Boot = "replacement"
	if _, e := f.c.Open(context.Background(), wrong); e == nil {
		t.Fatal("wrong boot accepted")
	}
	renew := func(_ context.Context, old Grant) (Grant, error) {
		next := old
		next.Credential = "renewed"
		next.Expires = old.Expires.Add(time.Second)
		f.a.mu.Lock()
		f.a.grants[next.Credential] = next
		f.a.mu.Unlock()
		return next, nil
	}
	if e := r.Renew(context.Background(), renew); e != nil {
		t.Fatal(e)
	}
	f.source.data = []byte("changed-size")
	if e := r.Renew(context.Background(), func(ctx context.Context, g Grant) (Grant, error) {
		g.Credential = "third"
		g.Expires = g.Expires.Add(time.Second)
		f.a.mu.Lock()
		f.a.grants[g.Credential] = g
		f.a.mu.Unlock()
		return g, nil
	}); e == nil {
		t.Fatal("changed size accepted")
	}
	if _, e := r.ReadAt(make([]byte, 2), 0); e == nil {
		t.Fatal("changed range size accepted")
	}
}

func TestSingleRangeSemanticsAndExpiry(t *testing.T) {
	f := setup(t)
	for _, tc := range []struct {
		method, ran, want string
		status            int
	}{{"GET", "bytes=-3", "def", 206}, {"GET", "bytes=13-", "def", 206}, {"HEAD", "bytes=5-7", "", 206}, {"GET", "bytes=0-1,4-5", "", 416}} {
		res, e := f.c.request(context.Background(), f.g, tc.method, tc.ran)
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(res.Body)
		res.Body.Close()
		if e != nil || res.StatusCode != tc.status {
			t.Fatalf("%s: %d %v", tc.ran, res.StatusCode, e)
		}
		if tc.status == 206 && string(b) != tc.want {
			t.Fatal(string(b))
		}
	}
	f.h.o.Now = func() time.Time { return f.g.Expires }
	if _, e := f.c.Open(context.Background(), f.g); e == nil {
		t.Fatal("expired grant accepted by server")
	}
}

type authorityFunc func(context.Context, Peer, string) (Grant, error)

func (f authorityFunc) Verify(ctx context.Context, p Peer, k string) (Grant, error) {
	return f(ctx, p, k)
}
func TestAcquireRejectsSealedSourceAfterVerification(t *testing.T) {
	f := setup(t)
	verified, proceed := make(chan struct{}), make(chan struct{})
	var sealed, acquired atomic.Bool
	f.h.o.Authority = authorityFunc(func(ctx context.Context, p Peer, k string) (Grant, error) {
		grant, err := f.a.Verify(ctx, p, k)
		close(verified)
		<-proceed
		return grant, err
	})
	f.h.o.Acquire = func(ctx context.Context, s Scope) (Hold, error) {
		// Real composition checks its manager lock/seal barrier while taking the
		// exact hold. This fixture seals in the Verify-to-Acquire race window.
		if sealed.Load() {
			return Hold{}, errors.New("source lifetime sealed")
		}
		acquired.Store(true)
		return Hold{s, f.source, func() {}}, nil
	}
	result := make(chan error, 1)
	go func() { _, e := f.c.Open(context.Background(), f.g); result <- e }()
	<-verified
	sealed.Store(true)
	close(proceed)
	if e := <-result; e == nil || acquired.Load() {
		t.Fatal("retired source acquired")
	}
	// Close waits for server-side defers before checking registry accounting.
	f.server.Close()
	if f.h.Reconcile() != 0 {
		t.Fatal("failed acquisition leaked slot")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestClientRejectsMalformedRepresentations(t *testing.T) {
	f := setup(t)
	for _, kind := range []string{"redirect", "multipart", "range", "length", "etag", "short", "status"} {
		t.Run(kind, func(t *testing.T) {
			c := *f.c
			c.http = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				header := http.Header{"Etag": {etag(f.g.Scope)}, "Content-Type": {"application/octet-stream"}}
				if req.Method == "HEAD" {
					return &http.Response{StatusCode: 200, Header: header, ContentLength: 16, Body: io.NopCloser(bytes.NewReader(nil))}, nil
				}
				header.Set("Content-Range", "bytes 3-4/16")
				res := &http.Response{StatusCode: 206, Header: header, ContentLength: 2, Body: io.NopCloser(bytes.NewBufferString("34"))}
				switch kind {
				case "redirect":
					res.StatusCode = 302
				case "multipart":
					header.Set("Content-Type", "multipart/byteranges")
				case "range":
					header.Set("Content-Range", "bytes 0-1/16")
				case "length":
					res.ContentLength = 3
				case "etag":
					header.Set("ETag", "changed")
				case "short":
					res.Body = io.NopCloser(bytes.NewBufferString("3"))
				case "status":
					res.StatusCode = 416
				}
				return res, nil
			})}
			r, e := c.Open(context.Background(), f.g)
			if e != nil {
				t.Fatal(e)
			}
			defer r.Close()
			if _, e := r.ReadAt(make([]byte, 2), 3); e == nil {
				t.Fatal("malformed response accepted")
			}
		})
	}
}
