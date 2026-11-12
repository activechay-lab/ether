package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/remotetest"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/safefetch"
	"github.com/coder/websocket"
)

// publicGuard maps fake public and private hostnames to a local test
// server's listener, while keeping safefetch's real public-vs-private
// IP validation in place.
func publicGuard(t *testing.T, srv *httptest.Server) *safefetch.Guard {
	t.Helper()
	targetAddr := srv.Listener.Addr().String()
	_, srvPort, err := net.SplitHostPort(targetAddr)
	if err != nil {
		t.Fatalf("split server addr: %v", err)
	}
	return &safefetch.Guard{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "addon.example.test", "second.example.test":
				return []netip.Addr{netip.MustParseAddr("93.184.216.34")}, nil
			case "lan.example.test":
				return []netip.Addr{netip.MustParseAddr("192.168.1.50")}, nil
			case "loopback.example.test":
				return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
			case "metadata.example.test":
				return []netip.Addr{netip.MustParseAddr("169.254.169.254")}, nil
			default:
				return nil, fmt.Errorf("unknown test host %q", host)
			}
		},
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, net.JoinHostPort("127.0.0.1", srvPort))
		},
	}
}

func TestURLClientHeadersDeadlinesAndMethods(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath, gotQuery, gotReqID, gotLocale, gotCred, gotPick, gotDeadline, gotCT string
	var gotBody []byte

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotReqID = r.Header.Get(HeaderRequestID)
		gotLocale = r.Header.Get(HeaderLocale)
		gotCred = r.Header.Get(HeaderCredential)
		gotPick = r.Header.Get(HeaderPick)
		gotDeadline = r.Header.Get(HeaderDeadline)
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Plugin-Reply", "ok")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client, err := NewURLClient(URLOptions{
		Base:       "http://addon.example.test/prefix/manifest.json",
		Credential: "cred-secret-1",
		Locale:     "en-CA",
		Guard:      publicGuard(t, srv),
	})
	if err != nil {
		t.Fatalf("NewURLClient: %v", err)
	}
	defer client.Close()

	if client.Base() != "http://addon.example.test/prefix" {
		t.Fatalf("Base() = %q, want stripped manifest.json", client.Base())
	}

	resp, err := client.Call(context.Background(), Request{
		Method:    http.MethodPost,
		Path:      "/ether/test",
		Query:     map[string][]string{"mode": {"live"}},
		Headers:   map[string][]string{HeaderPick: {"pick-tok"}, HeaderCredential: {"forged"}},
		Body:      []byte(`{"apiKey":"abc"}`),
		RequestID: "req-42",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.Status != http.StatusOK || string(resp.Body) != `{"ok":true}` {
		t.Fatalf("unexpected response: status=%d body=%s", resp.Status, resp.Body)
	}
	if resp.Header("X-Plugin-Reply") != "ok" {
		t.Fatalf("Header(X-Plugin-Reply) = %q, want ok", resp.Header("X-Plugin-Reply"))
	}
	if gotMethod != http.MethodPost || gotPath != "/prefix/ether/test" || gotQuery != "mode=live" {
		t.Fatalf("got %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	if gotReqID != "req-42" || gotLocale != "en-CA" || gotPick != "pick-tok" {
		t.Fatalf("headers: reqID=%q locale=%q pick=%q", gotReqID, gotLocale, gotPick)
	}
	if gotCred != "cred-secret-1" {
		t.Fatalf("credential header = %q, caller-supplied header must not override client credential", gotCred)
	}
	if gotCT != "application/json" || string(gotBody) != `{"apiKey":"abc"}` {
		t.Fatalf("content-type=%q body=%q", gotCT, gotBody)
	}
	dl, err := time.Parse(time.RFC3339, gotDeadline)
	if err != nil || time.Until(dl) <= 0 || time.Until(dl) > 6*time.Second {
		t.Fatalf("deadline header = %q (err=%v)", gotDeadline, err)
	}
}

func TestURLClientDeadlineExpiryAndSizeCap(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/slow":
			select {
			case <-r.Context().Done():
			case <-time.After(2 * time.Second):
			}
		case "/huge":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(bytes.Repeat([]byte("a"), 2048))
		}
	}))
	defer srv.Close()

	client, err := NewURLClient(URLOptions{
		Base:             "http://addon.example.test",
		Guard:            publicGuard(t, srv),
		MaxResponseBytes: 1024,
	})
	if err != nil {
		t.Fatalf("NewURLClient: %v", err)
	}
	defer client.Close()

	_, err = client.Call(context.Background(), Request{
		Path:    "/slow",
		Timeout: 50 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("slow Call err = %v, want DeadlineExceeded", err)
	}

	_, err = client.Call(context.Background(), Request{Path: "/huge"})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("huge Call err = %v, want ErrTooLarge", err)
	}

	// Streamed responses also enforce MaxStreamBytes when configured.
	streamClient, err := NewURLClient(URLOptions{
		Base:           "http://addon.example.test",
		Guard:          publicGuard(t, srv),
		MaxStreamBytes: 512,
	})
	if err != nil {
		t.Fatalf("NewURLClient stream: %v", err)
	}
	defer streamClient.Close()

	sresp, err := streamClient.CallStream(context.Background(), Request{Path: "/huge"})
	if err != nil {
		t.Fatalf("CallStream: %v", err)
	}
	_, err = io.ReadAll(sresp.Body)
	_ = sresp.Body.Close()
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("stream ReadAll err = %v, want ErrTooLarge", err)
	}
}

func TestURLClientSafefetchAndTrustedLANOrigin(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-private" {
			http.Redirect(w, r, "http://lan.example.test/secret", http.StatusFound)
			return
		}
		if r.URL.Path == "/redirect-other-lan" {
			http.Redirect(w, r, "http://loopback.example.test/secret", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"lan":true}`))
	}))
	defer srv.Close()

	guard := publicGuard(t, srv)

	// Private, loopback and cloud-metadata hosts are refused in standard URL mode.
	for _, host := range []string{"http://lan.example.test", "http://loopback.example.test", "http://metadata.example.test"} {
		c, err := NewURLClient(URLOptions{Base: host, Guard: guard})
		if err != nil {
			t.Fatalf("NewURLClient(%s): %v", host, err)
		}
		_, err = c.Call(context.Background(), Request{Path: "/manifest.json"})
		_ = c.Close()
		if !errors.Is(err, safefetch.ErrBlocked) {
			t.Fatalf("Call(%s) err = %v, want safefetch.ErrBlocked", host, err)
		}
	}

	// Public host redirecting to a private host is refused on the redirect hop.
	pubClient, err := NewURLClient(URLOptions{Base: "http://addon.example.test", Guard: guard})
	if err != nil {
		t.Fatalf("NewURLClient public: %v", err)
	}
	defer pubClient.Close()
	if _, err := pubClient.Call(context.Background(), Request{Path: "/redirect-private"}); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("redirect to private err = %v, want safefetch.ErrBlocked", err)
	}

	// Mismatched TrustedOrigin is refused at construction.
	if _, err := NewURLClient(URLOptions{
		Base:          "http://lan.example.test",
		TrustedOrigin: "http://other.example.test",
		Guard:         guard,
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("mismatched TrustedOrigin err = %v, want ErrInvalid", err)
	}

	// Owner-confirmed LAN URL mode allows that exact origin, and still refuses
	// redirects to any other private origin.
	lanClient, err := NewURLClient(URLOptions{
		Base:          "http://lan.example.test",
		TrustedOrigin: "http://lan.example.test",
		Guard:         guard,
	})
	if err != nil {
		t.Fatalf("NewURLClient LAN: %v", err)
	}
	defer lanClient.Close()

	resp, err := lanClient.Call(context.Background(), Request{Path: "/manifest.json"})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("LAN Call: status=%d err=%v", resp.Status, err)
	}
	if _, err := lanClient.Call(context.Background(), Request{Path: "/redirect-other-lan"}); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("LAN redirect to another private origin err = %v, want safefetch.ErrBlocked", err)
	}

	// Path validation prevents escaping the base URL.
	for _, bad := range []string{"", "manifest.json", "//evil.test/x", "/../secret", "http://evil.test/x"} {
		if _, err := lanClient.Call(context.Background(), Request{Path: bad}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("Call(%q) err = %v, want ErrInvalid", bad, err)
		}
	}
}

func TestTunnelAuthMultiplexDeadlineBackpressureAndReconnect(t *testing.T) {
	t.Parallel()

	creds := remotetest.Credentials{"cred-alpha": "org.example.alpha"}
	tunnel := NewTunnelServer(TunnelOptions{
		Auth:         creds,
		Locale:       "fr-CA",
		MaxInFlight:  4,
		ChunkBytes:   4096,
		PingInterval: 40 * time.Millisecond,
	})
	defer tunnel.Close()

	mux := http.NewServeMux()
	mux.Handle("GET "+TunnelPath, tunnel.Handler())
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1. Auth failures: missing credential and wrong credential get 401 before upgrade.
	for _, cred := range []string{"", "wrong-cred"} {
		badClient, err := NewTunnelClient(TunnelClientOptions{
			URL:        srv.URL + TunnelPath,
			Credential: "wrong-cred",
			Handler: func(_ context.Context, _ Request) (Response, error) {
				return Response{Status: http.StatusOK}, nil
			},
		})
		if err != nil {
			t.Fatalf("NewTunnelClient: %v", err)
		}
		if cred == "" {
			resp, err := http.Get(srv.URL + TunnelPath)
			if err != nil {
				t.Fatalf("GET tunnel: %v", err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("unauthenticated GET status = %d, want 401", resp.StatusCode)
			}
			continue
		}
		if err := badClient.ServeOnce(context.Background()); !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("ServeOnce with wrong credential err = %v, want ErrUnauthorized", err)
		}
	}

	// 2. Plugin ID mismatch in hello frame closes the connection.
	mismatchClient, err := NewTunnelClient(TunnelClientOptions{
		URL:        srv.URL + TunnelPath,
		Credential: "cred-alpha",
		Plugin:     "org.example.other",
		Handler: func(_ context.Context, _ Request) (Response, error) {
			return Response{Status: http.StatusOK}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewTunnelClient mismatch: %v", err)
	}
	if err := mismatchClient.ServeOnce(context.Background()); err == nil {
		t.Fatalf("expected error when hello plugin id mismatches credential")
	}

	// 3. Connect a real sidecar, test concurrency/multiplexing, backpressure, and deadlines.
	var active, maxSeen atomic.Int32
	gate := make(chan struct{})
	allStarted := make(chan struct{})
	var startedOnce sync.Once
	canceled := make(chan string, 4)

	sidecarHandler := func(ctx context.Context, req Request) (Response, error) {
		cur := active.Add(1)
		for {
			prev := maxSeen.Load()
			if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
				break
			}
		}
		defer active.Add(-1)

		switch req.Path {
		case "/hang":
			<-ctx.Done()
			canceled <- req.RequestID
			return Response{}, ctx.Err()
		case "/gated":
			if cur == 4 {
				startedOnce.Do(func() { close(allStarted) })
			}
			select {
			case <-gate:
			case <-ctx.Done():
				return Response{}, ctx.Err()
			}
		}
		out, _ := json.Marshal(map[string]any{
			"path":   req.Path,
			"seq":    firstHeader(req.Headers, "X-Seq"),
			"locale": req.Locale,
			"reqId":  req.RequestID,
		})
		return Response{
			Status:  http.StatusOK,
			Headers: map[string][]string{"Content-Type": {"application/json"}},
			Body:    out,
		}, nil
	}

	sidecar, err := NewTunnelClient(TunnelClientOptions{
		URL:        srv.URL + TunnelPath,
		Credential: "cred-alpha",
		Plugin:     "org.example.alpha",
		Handler:    sidecarHandler,
	})
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	done1 := make(chan error, 1)
	go func() { done1 <- sidecar.ServeOnce(ctx1) }()

	waitConnected(t, tunnel, "org.example.alpha")
	rt := tunnel.RuntimeFor("org.example.alpha")

	// Multiplex 16 concurrent calls across a MaxInFlight=4 connection.
	const totalCalls = 16
	var wg sync.WaitGroup
	errs := make(chan error, totalCalls)
	go func() {
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case <-allStarted:
		case <-timer.C:
			t.Error("four gated handlers did not start")
		}
		close(gate)
	}()
	for i := range totalCalls {
		wg.Add(1)
		go func(seq int) {
			defer wg.Done()
			s := fmt.Sprintf("%d", seq)
			resp, err := rt.Call(context.Background(), Request{
				Path:    "/gated",
				Headers: map[string][]string{"X-Seq": {s}},
			})
			if err != nil {
				errs <- fmt.Errorf("seq %d: %w", seq, err)
				return
			}
			var body map[string]string
			if err := json.Unmarshal(resp.Body, &body); err != nil {
				errs <- fmt.Errorf("seq %d unmarshal: %w", seq, err)
				return
			}
			if body["seq"] != s || body["locale"] != "fr-CA" || body["reqId"] == "" {
				errs <- fmt.Errorf("seq %d mismatched reply: %+v", seq, body)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent call failed: %v", err)
	}
	if got := maxSeen.Load(); got < 2 || got > 4 {
		t.Fatalf("max concurrent on sidecar = %d, want between 2 and MaxInFlight(4)", got)
	}

	// Deadline expiry cancels the sidecar's request context via a cancel frame.
	_, err = rt.Call(context.Background(), Request{
		Path:      "/hang",
		RequestID: "hang-1",
		Timeout:   60 * time.Millisecond,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("hang Call err = %v, want DeadlineExceeded", err)
	}
	select {
	case id := <-canceled:
		if id != "hang-1" {
			t.Fatalf("canceled requestId = %q, want hang-1", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("sidecar did not observe cancel after deadline")
	}

	// 4. Reconnect: while a call is in flight on connection 1, a second sidecar
	// connects for the same plugin; the in-flight call on connection 1 fails
	// cleanly with ErrDisconnected, and subsequent calls succeed on connection 2.
	inFlightStarted := make(chan struct{})
	blockConn1 := make(chan struct{})
	defer close(blockConn1)

	sidecar1b, _ := NewTunnelClient(TunnelClientOptions{
		URL:        srv.URL + TunnelPath,
		Credential: "cred-alpha",
		Handler: func(ctx context.Context, req Request) (Response, error) {
			if req.Path == "/block" {
				close(inFlightStarted)
				select {
				case <-blockConn1:
				case <-ctx.Done():
					return Response{}, ctx.Err()
				}
			}
			return Response{Status: http.StatusOK, Body: []byte(`{"conn":1}`)}, nil
		},
	})

	ctx1b, cancel1b := context.WithCancel(context.Background())
	defer cancel1b()
	done1b := make(chan error, 1)
	go func() { done1b <- sidecar1b.ServeOnce(ctx1b) }()
	// Wait until sidecar1b has replaced sidecar1 on the server.
	<-done1

	inFlightErr := make(chan error, 1)
	go func() {
		_, err := rt.Call(context.Background(), Request{Path: "/block", Timeout: 5 * time.Second})
		inFlightErr <- err
	}()
	<-inFlightStarted

	// Connect replacement sidecar.
	sidecar2, _ := NewTunnelClient(TunnelClientOptions{
		URL:        srv.URL + TunnelPath,
		Credential: "cred-alpha",
		Handler: func(_ context.Context, _ Request) (Response, error) {
			return Response{Status: http.StatusOK, Body: []byte(`{"conn":2}`)}, nil
		},
	})
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go func() { _ = sidecar2.ServeOnce(ctx2) }()

	select {
	case err := <-inFlightErr:
		if !errors.Is(err, ErrDisconnected) {
			t.Fatalf("in-flight call across reconnect err = %v, want ErrDisconnected", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("in-flight call did not fail promptly on reconnect")
	}
	<-done1b
	waitConnected(t, tunnel, "org.example.alpha")

	resp, err := rt.Call(context.Background(), Request{Path: "/manifest.json"})
	if err != nil || string(resp.Body) != `{"conn":2}` {
		t.Fatalf("post-reconnect Call: body=%s err=%v", resp.Body, err)
	}
}

func TestSamePluginOverURLAndTunnelPlusRangeRelay(t *testing.T) {
	t.Parallel()

	// Create a deterministic 200 KiB media file to test multi-chunk streaming and byte ranges.
	tmpDir := t.TempDir()
	fileBytes := make([]byte, 200*1024+37)
	if _, err := rand.Read(fileBytes); err != nil {
		t.Fatalf("rand.Read: %v", err)
	}
	mediaPath := filepath.Join(tmpDir, "movies", "sample.mkv")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(mediaPath, fileBytes, 0o644); err != nil {
		t.Fatalf("write media file: %v", err)
	}

	fileStreamer := FileHandler(tmpDir)
	simpleHandler := func(_ context.Context, req Request) (Response, error) {
		switch req.Path {
		case "/manifest.json":
			return Response{
				Status:  http.StatusOK,
				Headers: map[string][]string{"Content-Type": {"application/json"}},
				Body:    []byte(`{"id":"org.example.simple","version":"1.0.0","name":"Simple"}`),
			}, nil
		case "/stream/movie/tt0012345.json":
			return Response{
				Status:  http.StatusOK,
				Headers: map[string][]string{"Content-Type": {"application/json"}},
				Body:    []byte(`{"streams":[{"name":"Simple","ether":{"id":"s1","file":"movies/sample.mkv"}}]}`),
			}, nil
		case "/ether/health":
			return Response{
				Status:  http.StatusOK,
				Headers: map[string][]string{"Content-Type": {"application/json"}},
				Body:    []byte(`{"status":"ok"}`),
			}, nil
		default:
			return Response{Status: http.StatusNotFound, Body: []byte(`{"error":{"code":"not_found","message":"not found"}}`)}, nil
		}
	}

	// Serve the exact same plugin over HTTP (URL mode) and over the websocket tunnel.
	urlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := Request{
			Method:    r.Method,
			Path:      r.URL.Path,
			Query:     r.URL.Query(),
			Headers:   r.Header,
			Locale:    r.Header.Get(HeaderLocale),
			RequestID: r.Header.Get(HeaderRequestID),
		}
		req.Body, _ = io.ReadAll(r.Body)
		if sresp, err := fileStreamer(r.Context(), req); err == nil && sresp != nil {
			defer sresp.Body.Close()
			for k, vs := range sresp.Headers {
				for _, v := range vs {
					w.Header().Add(k, v)
				}
			}
			w.WriteHeader(sresp.Status)
			_, _ = io.Copy(w, sresp.Body)
			return
		} else if err != nil && !errors.Is(err, ErrStreamNotHandled) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		resp, err := simpleHandler(r.Context(), req)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		for k, vs := range resp.Headers {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.Status)
		_, _ = w.Write(resp.Body)
	}))
	defer urlSrv.Close()

	urlRT, err := NewURLClient(URLOptions{
		Base:  "http://addon.example.test",
		Guard: publicGuard(t, urlSrv),
	})
	if err != nil {
		t.Fatalf("NewURLClient: %v", err)
	}
	defer urlRT.Close()

	tunnel := NewTunnelServer(TunnelOptions{
		Auth:       remotetest.Credentials{"cred-simple": "org.example.simple"},
		ChunkBytes: 16 * 1024, // force multiple chunks
	})
	defer tunnel.Close()
	tunnelSrv := httptest.NewServer(tunnel.Handler())
	defer tunnelSrv.Close()

	sidecar, err := NewTunnelClient(TunnelClientOptions{
		URL:           tunnelSrv.URL,
		Credential:    "cred-simple",
		Plugin:        "org.example.simple",
		Handler:       simpleHandler,
		StreamHandler: fileStreamer,
	})
	if err != nil {
		t.Fatalf("NewTunnelClient: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sidecar.ServeOnce(ctx) }()
	waitConnected(t, tunnel, "org.example.simple")
	tunnelRT := tunnel.RuntimeFor("org.example.simple")
	defer tunnelRT.Close()
	duplicate := tunnel.RuntimeFor("org.example.simple")
	if err := duplicate.Close(); err != nil || !tunnel.IsConnected("org.example.simple") {
		t.Fatalf("closing duplicate runtime disconnected active tunnel: %v", err)
	}

	// Verify identical JSON responses across URL mode and tunnel mode.
	for _, p := range []string{"/manifest.json", "/stream/movie/tt0012345.json", "/ether/health", "/missing"} {
		uResp, err := urlRT.Call(context.Background(), Request{Path: p})
		if err != nil {
			t.Fatalf("URL Call(%s): %v", p, err)
		}
		tResp, err := tunnelRT.Call(context.Background(), Request{Path: p})
		if err != nil {
			t.Fatalf("Tunnel Call(%s): %v", p, err)
		}
		if uResp.Status != tResp.Status || !bytes.Equal(uResp.Body, tResp.Body) {
			t.Fatalf("route %s differed: URL=(%d, %s) Tunnel=(%d, %s)", p, uResp.Status, uResp.Body, tResp.Status, tResp.Body)
		}
	}

	// Verify byte-identical full file and byte-range relays across URL mode and tunnel mode.
	type rangeCase struct {
		header     string
		wantStatus int
		wantSlice  []byte
		wantRange  string
	}
	size := len(fileBytes)
	cases := []rangeCase{
		{
			header:     "",
			wantStatus: http.StatusOK,
			wantSlice:  fileBytes,
		},
		{
			header:     "bytes=0-1023",
			wantStatus: http.StatusPartialContent,
			wantSlice:  fileBytes[:1024],
			wantRange:  fmt.Sprintf("bytes 0-1023/%d", size),
		},
		{
			header:     "bytes=50000-140000",
			wantStatus: http.StatusPartialContent,
			wantSlice:  fileBytes[50000 : 140000+1],
			wantRange:  fmt.Sprintf("bytes 50000-140000/%d", size),
		},
		{
			header:     "bytes=-4096",
			wantStatus: http.StatusPartialContent,
			wantSlice:  fileBytes[size-4096:],
			wantRange:  fmt.Sprintf("bytes %d-%d/%d", size-4096, size-1, size),
		},
		{
			header:     fmt.Sprintf("bytes=%d-", size-513),
			wantStatus: http.StatusPartialContent,
			wantSlice:  fileBytes[size-513:],
			wantRange:  fmt.Sprintf("bytes %d-%d/%d", size-513, size-1, size),
		},
		{
			header:     fmt.Sprintf("bytes=%d-%d", size+10, size+20),
			wantStatus: http.StatusRequestedRangeNotSatisfiable,
			wantSlice:  nil,
			wantRange:  fmt.Sprintf("bytes */%d", size),
		},
	}

	for _, tc := range cases {
		for mode, streamer := range map[string]Streamer{"url": urlRT, "tunnel": tunnelRT.(Streamer)} {
			var hdrs map[string][]string
			if tc.header != "" {
				hdrs = map[string][]string{"Range": {tc.header}}
			}
			sr, err := streamer.CallStream(context.Background(), Request{
				Path:    "/ether/file/movies/sample.mkv",
				Headers: hdrs,
			})
			if err != nil {
				t.Fatalf("%s CallStream(%q): %v", mode, tc.header, err)
			}
			got, err := io.ReadAll(sr.Body)
			_ = sr.Body.Close()
			if err != nil {
				t.Fatalf("%s ReadAll(%q): %v", mode, tc.header, err)
			}
			if sr.Status != tc.wantStatus {
				t.Fatalf("%s Range(%q) status = %d, want %d", mode, tc.header, sr.Status, tc.wantStatus)
			}
			if tc.wantRange != "" && sr.Header("Content-Range") != tc.wantRange {
				t.Fatalf("%s Range(%q) Content-Range = %q, want %q", mode, tc.header, sr.Header("Content-Range"), tc.wantRange)
			}
			if !bytes.Equal(got, tc.wantSlice) {
				t.Fatalf("%s Range(%q) body len=%d did not match expected slice len=%d", mode, tc.header, len(got), len(tc.wantSlice))
			}
		}
	}

	// Path traversal attempt on /ether/file/ is rejected.
	if _, err := tunnelRT.(Streamer).CallStream(context.Background(), Request{
		Path: "/ether/file/%2e%2e/secret.txt",
	}); err == nil {
		t.Fatalf("expected path escape in /ether/file/ to fail")
	}
}

type fakeHost struct {
	mu          sync.Mutex
	kv          map[string]map[string][]byte
	denyCatalog bool
	denyHistory bool
	denyWatch   bool
	denyColl    bool
	denyKinds   bool
	denySources bool
	denyCache   bool
	watchCalls  []string
	collections []string
	kindItems   map[string][]core.KindItem
	blobs       map[string][]byte
	notices     []string
	logs        []string
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		kv:        map[string]map[string][]byte{},
		kindItems: map[string][]core.KindItem{},
		blobs:     map[string][]byte{},
	}
}

func (f *fakeHost) KVGet(_ context.Context, pluginID, key string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.kv[pluginID][key]
	return append([]byte(nil), v...), ok, nil
}

func (f *fakeHost) KVPut(_ context.Context, pluginID, key string, value []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(value) > 1024 {
		return plugins.ErrQuotaExceeded
	}
	if f.kv[pluginID] == nil {
		f.kv[pluginID] = map[string][]byte{}
	}
	f.kv[pluginID][key] = append([]byte(nil), value...)
	return nil
}

func (f *fakeHost) KVDelete(_ context.Context, pluginID, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.kv[pluginID], key)
	return nil
}

func (f *fakeHost) KVList(_ context.Context, pluginID, prefix string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var keys []string
	for k := range f.kv[pluginID] {
		if strings.HasPrefix(k, prefix) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys, nil
}

func (f *fakeHost) CatalogSearch(_ context.Context, _, query string, limit, offset int) (json.RawMessage, error) {
	if f.denyCatalog {
		return nil, ErrPermissionDenied
	}
	out, _ := json.Marshal(map[string]any{"q": query, "limit": limit, "offset": offset})
	return out, nil
}

func (f *fakeHost) CatalogGet(_ context.Context, _, id string) (json.RawMessage, error) {
	if f.denyCatalog {
		return nil, ErrPermissionDenied
	}
	if id == "missing" {
		return nil, ErrNotFound
	}
	out, _ := json.Marshal(map[string]any{"id": id})
	return out, nil
}

func (f *fakeHost) ViewerGranted(context.Context, string) (json.RawMessage, error) {
	return json.RawMessage(`[]`), nil
}
func (f *fakeHost) ViewerHistory(_ context.Context, _ string, viewerID int64, limit, offset int) (json.RawMessage, error) {
	if f.denyHistory {
		return nil, ErrPermissionDenied
	}
	out, _ := json.Marshal(map[string]any{"viewerId": viewerID, "limit": limit, "offset": offset})
	return out, nil
}

func (f *fakeHost) SetWatchState(_ context.Context, pluginID string, viewerID int64, titleID string, watched bool) error {
	if f.denyWatch {
		return ErrPermissionDenied
	}
	f.mu.Lock()
	f.watchCalls = append(f.watchCalls, fmt.Sprintf("%s:%d:%s:%v", pluginID, viewerID, titleID, watched))
	f.mu.Unlock()
	return nil
}

func (f *fakeHost) CollectionsUpsert(_ context.Context, pluginID string, collection json.RawMessage) error {
	if f.denyColl {
		return ErrPermissionDenied
	}
	f.mu.Lock()
	f.collections = append(f.collections, pluginID+":"+string(collection))
	f.mu.Unlock()
	return nil
}

func (f *fakeHost) KindUpsert(_ context.Context, pluginID, kindKey string, items []core.KindItem) (int, error) {
	if f.denyKinds {
		return 0, ErrPermissionDenied
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := pluginID + ":" + kindKey
	f.kindItems[k] = append(f.kindItems[k], items...)
	return len(items), nil
}

func (f *fakeHost) KindDelete(_ context.Context, pluginID, kindKey string, externalIDs []string) error {
	if f.denyKinds {
		return ErrPermissionDenied
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	k := pluginID + ":" + kindKey
	drop := map[string]bool{}
	for _, id := range externalIDs {
		drop[id] = true
	}
	var kept []core.KindItem
	for _, it := range f.kindItems[k] {
		if !drop[it.ID] {
			kept = append(kept, it)
		}
	}
	f.kindItems[k] = kept
	return nil
}

func (f *fakeHost) KindItems(_ context.Context, pluginID, kindKey, _ string, _ int) (core.KindPage, error) {
	if f.denyKinds {
		return core.KindPage{}, ErrPermissionDenied
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return core.KindPage{Items: append([]core.KindItem(nil), f.kindItems[pluginID+":"+kindKey]...)}, nil
}

func (f *fakeHost) ResolveSources(_ context.Context, _ string, q core.SourceQuery) ([]core.SourceCandidate, error) {
	if f.denySources {
		return nil, ErrPermissionDenied
	}
	return []core.SourceCandidate{{InfoHash: "cand-1", Title: q.Query, Cached: true}}, nil
}

func (f *fakeHost) CachePut(_ context.Context, pluginID, libraryKey, _ string, r io.Reader) (string, error) {
	if f.denyCache {
		return "", ErrPermissionDenied
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return "", err
	}
	ref := "blob:" + pluginID + ":" + libraryKey
	f.mu.Lock()
	f.blobs[ref] = data
	f.mu.Unlock()
	return ref, nil
}

func (f *fakeHost) CacheOpen(_ context.Context, _, blobRef string) (io.ReadSeekCloser, error) {
	if f.denyCache {
		return nil, ErrPermissionDenied
	}
	f.mu.Lock()
	data, ok := f.blobs[blobRef]
	f.mu.Unlock()
	if !ok {
		return nil, ErrNotFound
	}
	return struct {
		*bytes.Reader
		io.Closer
	}{Reader: bytes.NewReader(data), Closer: io.NopCloser(nil)}, nil
}

func (f *fakeHost) Settings(_ context.Context, pluginID string) (map[string]any, error) {
	return map[string]any{"plugin": pluginID}, nil
}

func (f *fakeHost) StreamURL(_ context.Context, pluginID string, viewerID int64, itemRef string) (string, error) {
	return fmt.Sprintf("/ether/v1/kind-media/%s-%d-%s", pluginID, viewerID, itemRef), nil
}

func (f *fakeHost) NotifyOwner(_ context.Context, pluginID, text string) error {
	f.mu.Lock()
	f.notices = append(f.notices, pluginID+":"+text)
	f.mu.Unlock()
	return nil
}

func (f *fakeHost) Log(_ context.Context, pluginID, level, msg string, _ map[string]any) error {
	f.mu.Lock()
	f.logs = append(f.logs, fmt.Sprintf("%s:%s:%s", pluginID, level, msg))
	f.mu.Unlock()
	return nil
}

func TestPluginHostAPIEndpointsAuthAndGrants(t *testing.T) {
	t.Parallel()

	fh := newFakeHost()
	handler := NewHostHandler(HostOptions{
		Host: fh,
		Auth: remotetest.Credentials{
			"cred-one": "plugin.one",
			"cred-two": "plugin.two",
		},
	})
	srv := httptest.NewServer(handler)
	defer srv.Close()

	doHost := func(method, path, cred, body string) (int, map[string]any) {
		t.Helper()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, rdr)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		if cred != "" {
			req.Header.Set(HeaderCredential, cred)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("Do(%s %s): %v", method, path, err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out
	}

	// Unauthenticated or wrong credential returns 401.
	if status, _ := doHost(http.MethodGet, "/ether/plugin-host/kv", "", ""); status != http.StatusUnauthorized {
		t.Fatalf("no cred status = %d, want 401", status)
	}
	if status, _ := doHost(http.MethodGet, "/ether/plugin-host/kv", "bad", ""); status != http.StatusUnauthorized {
		t.Fatalf("bad cred status = %d, want 401", status)
	}

	// KV put/get/list/delete are isolated per plugin credential.
	if status, _ := doHost(http.MethodPut, "/ether/plugin-host/kv/state/cursor", "cred-one", `{"value":"42"}`); status != http.StatusOK {
		t.Fatalf("kv put status = %d", status)
	}
	status, kvOut := doHost(http.MethodGet, "/ether/plugin-host/kv/state/cursor", "cred-one", "")
	if status != http.StatusOK || kvOut["value"] != "42" {
		t.Fatalf("kv get = %d %+v", status, kvOut)
	}
	if status, _ := doHost(http.MethodGet, "/ether/plugin-host/kv/state/cursor", "cred-two", ""); status != http.StatusNotFound {
		t.Fatalf("kv get from other plugin status = %d, want 404", status)
	}
	status, listOut := doHost(http.MethodGet, "/ether/plugin-host/kv?prefix=state/", "cred-one", "")
	if status != http.StatusOK || len(listOut["keys"].([]any)) != 1 {
		t.Fatalf("kv list = %d %+v", status, listOut)
	}
	if status, _ := doHost(http.MethodDelete, "/ether/plugin-host/kv/state/cursor", "cred-one", ""); status != http.StatusOK {
		t.Fatalf("kv delete status = %d", status)
	}

	// Catalog search and get (and permission revocation -> 403).
	status, catOut := doHost(http.MethodGet, "/ether/plugin-host/catalog/search?q=matrix&page=2&pageSize=25", "cred-one", "")
	if status != http.StatusOK || catOut["q"] != "matrix" || catOut["limit"] != float64(25) || catOut["offset"] != float64(25) {
		t.Fatalf("catalog search = %d %+v", status, catOut)
	}
	status, itemOut := doHost(http.MethodGet, "/ether/plugin-host/catalog/tmdb:603", "cred-one", "")
	if status != http.StatusOK || itemOut["id"] != "tmdb:603" {
		t.Fatalf("catalog get = %d %+v", status, itemOut)
	}
	fh.denyCatalog = true
	if status, _ := doHost(http.MethodGet, "/ether/plugin-host/catalog/tmdb:603", "cred-one", ""); status != http.StatusForbidden {
		t.Fatalf("denied catalog get status = %d, want 403", status)
	}

	// Viewer history and watch state.
	if status, _ := doHost(http.MethodGet, "/ether/plugin-host/viewers/granted", "cred-one", ""); status != http.StatusOK {
		t.Fatalf("granted viewers: status %d", status)
	}
	status, histOut := doHost(http.MethodGet, "/ether/plugin-host/viewers/7/history?limit=10", "cred-one", "")
	if status != http.StatusOK || histOut["viewerId"] != float64(7) {
		t.Fatalf("viewer history = %d %+v", status, histOut)
	}
	if status, _ := doHost(http.MethodPost, "/ether/plugin-host/viewers/watch-state", "cred-one", `{"viewerId":7,"titleId":"tmdb:603","watched":true}`); status != http.StatusOK {
		t.Fatalf("setWatchState status = %d", status)
	}
	fh.denyWatch = true
	if status, _ := doHost(http.MethodPost, "/ether/plugin-host/viewers/watch-state", "cred-one", `{"viewerId":7,"titleId":"tmdb:603","watched":true}`); status != http.StatusForbidden {
		t.Fatalf("denied setWatchState status = %d, want 403", status)
	}

	// Collections upsert, notify owner, and log.
	if status, _ := doHost(http.MethodPost, "/ether/plugin-host/collections/upsert", "cred-one", `{"id":"scifi","title":"Sci-Fi"}`); status != http.StatusOK {
		t.Fatalf("collections upsert status = %d", status)
	}
	if status, _ := doHost(http.MethodPost, "/ether/plugin-host/notify", "cred-one", `{"text":"Sync complete"}`); status != http.StatusOK {
		t.Fatalf("notify status = %d", status)
	}
	if status, _ := doHost(http.MethodPost, "/ether/plugin-host/log", "cred-one", `{"level":"info","message":"hello"}`); status != http.StatusOK {
		t.Fatalf("log status = %d", status)
	}
	if len(fh.watchCalls) != 1 || len(fh.collections) != 1 || len(fh.notices) != 1 || len(fh.logs) != 1 {
		t.Fatalf("unexpected recorded host calls: watch=%v coll=%v notices=%v logs=%v", fh.watchCalls, fh.collections, fh.notices, fh.logs)
	}

	// Kind upsert, list, delete, sources resolve, and blob cache put/open (plus 403 on revocation).
	status, upOut := doHost(http.MethodPost, "/ether/plugin-host/kinds/rom/upsert", "cred-one", `{"items":[{"id":"smw","level":"game","title":"Super Mario World"}]}`)
	if status != http.StatusOK || upOut["accepted"] != float64(1) {
		t.Fatalf("kind upsert = %d %+v", status, upOut)
	}
	status, listKindOut := doHost(http.MethodGet, "/ether/plugin-host/kinds/rom/items", "cred-one", "")
	if status != http.StatusOK || len(listKindOut["items"].([]any)) != 1 {
		t.Fatalf("kind items = %d %+v", status, listKindOut)
	}
	if status, _ := doHost(http.MethodPost, "/ether/plugin-host/kinds/rom/delete", "cred-one", `{"ids":["smw"]}`); status != http.StatusOK {
		t.Fatalf("kind delete status = %d", status)
	}
	fh.denyKinds = true
	if status, _ := doHost(http.MethodPost, "/ether/plugin-host/kinds/rom/upsert", "cred-one", `{"items":[{"id":"smw","level":"game","title":"Super Mario World"}]}`); status != http.StatusForbidden {
		t.Fatalf("denied kind upsert status = %d, want 403", status)
	}

	status, srcOut := doHost(http.MethodPost, "/ether/plugin-host/sources/resolve", "cred-one", `{"query":"Mario"}`)
	if status != http.StatusOK || len(srcOut["candidates"].([]any)) != 1 {
		t.Fatalf("sources resolve = %d %+v", status, srcOut)
	}
	fh.denySources = true
	if status, _ := doHost(http.MethodPost, "/ether/plugin-host/sources/resolve", "cred-one", `{"query":"Mario"}`); status != http.StatusForbidden {
		t.Fatalf("denied sources resolve status = %d, want 403", status)
	}

	status, cacheOut := doHost(http.MethodPut, "/ether/plugin-host/cache/pk-plugin-one-rom/smw.sfc", "cred-one", "ROMDATA")
	if status != http.StatusOK || cacheOut["blob"] != "blob:plugin.one:pk-plugin-one-rom" {
		t.Fatalf("cache put = %d %+v", status, cacheOut)
	}
	cacheReq, _ := http.NewRequest(http.MethodGet, srv.URL+"/ether/plugin-host/cache/blob:plugin.one:pk-plugin-one-rom", nil)
	cacheReq.Header.Set(HeaderCredential, "cred-one")
	cacheResp, err := http.DefaultClient.Do(cacheReq)
	if err != nil {
		t.Fatalf("cache open: %v", err)
	}
	cacheBody, _ := io.ReadAll(cacheResp.Body)
	_ = cacheResp.Body.Close()
	if cacheResp.StatusCode != http.StatusOK || string(cacheBody) != "ROMDATA" {
		t.Fatalf("cache open = %d %q", cacheResp.StatusCode, cacheBody)
	}
}

func TestFactoryAndContractAdapter(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	tunnel := NewTunnelServer(TunnelOptions{
		Auth: remotetest.Credentials{"cred-tun": "org.example.tun"},
	})
	defer tunnel.Close()
	tunSrv := httptest.NewServer(tunnel.Handler())
	defer tunSrv.Close()

	sidecar, _ := NewTunnelClient(TunnelClientOptions{
		URL:        tunSrv.URL,
		Credential: "cred-tun",
		Handler: func(_ context.Context, _ Request) (Response, error) {
			return Response{Status: http.StatusOK, Body: []byte(`{"status":"ok","via":"tunnel"}`)}, nil
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = sidecar.ServeOnce(ctx) }()
	waitConnected(t, tunnel, "org.example.tun")

	factory := &Factory{
		Tunnel:     tunnel,
		Guard:      publicGuard(t, srv),
		TrustedLAN: func(id string) bool { return id == "org.example.lan" },
	}
	open := factory.RuntimeFactory()

	// LAN URL plugin with TrustedLAN=true succeeds.
	lanRT, err := open(plugins.Installed{
		ID:        "org.example.lan",
		SourceURL: "http://lan.example.test/manifest.json",
	})
	if err != nil {
		t.Fatalf("open LAN runtime: %v", err)
	}
	defer lanRT.Close()
	resp, err := lanRT.Call(context.Background(), plugins.Request{Path: "/ether/health"})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("lanRT.Call: status=%d err=%v", resp.Status, err)
	}

	// An Ether plugin from a GitHub source URL selects the tunnel runtime.
	tunRT, err := open(plugins.Installed{
		ID:        "org.example.tun",
		SourceURL: "https://github.com/example/ether-plugin",
		Manifest:  plugins.Manifest{Ether: &plugins.EtherBlock{}},
	})
	if err != nil {
		t.Fatalf("open tunnel runtime: %v", err)
	}
	defer tunRT.Close()
	resp, err = tunRT.Call(context.Background(), plugins.Request{Path: "/ether/health"})
	if err != nil || !strings.Contains(string(resp.Body), `"via":"tunnel"`) {
		t.Fatalf("tunRT.Call: body=%s err=%v", resp.Body, err)
	}
}

func TestPythonReferenceTunnelClientFixture(t *testing.T) {
	t.Parallel()

	pythonBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not on PATH")
	}

	tmpDir := t.TempDir()
	sample := bytes.Repeat([]byte("0123456789abcdef"), 6000) // 96 KiB
	if err := os.WriteFile(filepath.Join(tmpDir, "clip.bin"), sample, 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}

	tunnel := NewTunnelServer(TunnelOptions{
		Auth:         remotetest.Credentials{"py-cred": "org.example.py"},
		ChunkBytes:   8192,
		PingInterval: 30 * time.Millisecond,
	})
	defer tunnel.Close()
	mux := http.NewServeMux()
	mux.Handle(TunnelPath, tunnel.Handler())
	mux.HandleFunc(PairExchangePath, func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil || input.Code != "TESTCODE" {
			http.Error(w, "invalid", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"credential": "py-cred", "pluginId": "org.example.py"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	fixtureDir, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatalf("abs testdata: %v", err)
	}
	script := `
import sys
sys.path.insert(0, sys.argv[1])
from ether_tunnel import Tunnel, Response

def handle(req):
    if req.path == "/manifest.json":
        return Response.json({"id": "org.example.py", "locale": req.header("X-Ether-Locale")})
    if req.path == "/echo":
        return Response.json({"got": req.json()})
    return Response(404, b'{"error":{"code":"not_found","message":"missing"}}')

t = Tunnel(sys.argv[2], pair_code="TESTCODE", handler=handle, file_root=sys.argv[3])
t.serve_once()
`
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, pythonBin, "-B", "-c", script, fixtureDir, srv.URL+TunnelPath, tmpDir)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start python sidecar: %v", err)
	}
	defer func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	waitConnected(t, tunnel, "org.example.py")
	rt := tunnel.RuntimeFor("org.example.py")

	resp, err := rt.Call(ctx, Request{Path: "/manifest.json", Locale: "de"})
	if err != nil || !strings.Contains(string(resp.Body), `"locale": "de"`) {
		t.Fatalf("python /manifest.json: body=%s err=%v stderr=%s", resp.Body, err, stderr.String())
	}
	resp, err = rt.Call(ctx, Request{Method: http.MethodPost, Path: "/echo", Body: []byte(`{"hello":"world"}`)})
	if err != nil || !strings.Contains(string(resp.Body), `"hello": "world"`) {
		t.Fatalf("python /echo: body=%s err=%v stderr=%s", resp.Body, err, stderr.String())
	}

	// Range relay from Python sidecar.
	sresp, err := rt.(Streamer).CallStream(ctx, Request{
		Path:    "/ether/file/clip.bin",
		Headers: map[string][]string{"Range": {"bytes=100-20099"}},
	})
	if err != nil {
		t.Fatalf("python CallStream: %v (stderr=%s)", err, stderr.String())
	}
	got, err := io.ReadAll(sresp.Body)
	_ = sresp.Body.Close()
	if err != nil || sresp.Status != http.StatusPartialContent || !bytes.Equal(got, sample[100:20100]) {
		t.Fatalf("python range relay: status=%d len=%d err=%v", sresp.Status, len(got), err)
	}
}

func TestSubprotocolCredentialAuth(t *testing.T) {
	t.Parallel()

	// Node's global WebSocket authenticates via the ether-credential.<token>
	// subprotocol entry (tested directly here so CI without a TS runner
	// still exercises the exact wire path ether_tunnel.ts uses).
	tunnel := NewTunnelServer(TunnelOptions{
		Auth: remotetest.Credentials{"eg1_subproto_token": "org.example.ts"},
	})
	defer tunnel.Close()
	srv := httptest.NewServer(tunnel.Handler())
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws://" + strings.TrimPrefix(srv.URL, "http://")
	ws, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		Subprotocols: []string{TunnelProtocol, credentialSubprotocolPrefix + "eg1_subproto_token"},
	})
	if err != nil {
		t.Fatalf("Dial with subprotocol credential: %v", err)
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	if err := ws.Write(ctx, websocket.MessageText, []byte(`{"type":"hello","protocol":"ether-tunnel-v1","plugin":"org.example.ts"}`)); err != nil {
		t.Fatalf("write hello: %v", err)
	}
	_, raw, err := ws.Read(ctx)
	if err != nil || !strings.Contains(string(raw), `"type":"welcome"`) {
		t.Fatalf("read welcome: raw=%s err=%v", raw, err)
	}
}

func waitConnected(t *testing.T, tunnel *TunnelServer, pluginID string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if tunnel.IsConnected(pluginID) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for plugin %q to connect to tunnel", pluginID)
}

func TestURLClientRejectsInsecureConfigurationAndCrossOriginRedirect(t *testing.T) {
	var targetCalled atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalled.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	client, err := NewURLClient(URLOptions{Base: source.URL, TrustedOrigin: source.URL, Credential: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := client.Call(context.Background(), Request{Method: http.MethodPost, Path: "/ether/configure", Body: []byte(`{}`)}); err == nil {
		t.Fatal("insecure configuration accepted")
	}
	if _, err := client.Call(context.Background(), Request{Path: "/redirect"}); err == nil {
		t.Fatal("cross-origin redirect accepted")
	}
	if targetCalled.Load() {
		t.Fatal("redirect target received credential")
	}
}

func TestTypeScriptReferencePairing(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node unavailable")
	}
	fixture, err := filepath.Abs("testdata/ether_tunnel.ts")
	if err != nil {
		t.Fatal(err)
	}
	probe := exec.Command(node, "--input-type=module", "-e", "if(typeof WebSocket==='undefined') process.exit(1); await import(process.argv[1])", "file://"+fixture)
	if err := probe.Run(); err != nil {
		t.Skip("node lacks native TypeScript or WebSocket support")
	}
	tunnel := NewTunnelServer(TunnelOptions{Auth: remotetest.Credentials{"ts-credential": "org.example.ts.pair"}})
	defer tunnel.Close()
	mux := http.NewServeMux()
	mux.Handle(TunnelPath, tunnel.Handler())
	mux.HandleFunc(PairExchangePath, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Code string `json:"code"`
		}
		if json.NewDecoder(r.Body).Decode(&req) != nil || req.Code != "TESTCODE" {
			http.Error(w, "invalid", 401)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"credential": "ts-credential", "pluginId": "org.example.ts.pair"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	script := `const {Tunnel,json}=await import(process.argv[1]); const t=new Tunnel({url:process.argv[2],pairCode:"TESTCODE",handler:()=>json({ok:true})}); await t.serveOnce();`
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--input-type=module", "-e", script, "file://"+fixture, "ws"+strings.TrimPrefix(srv.URL, "http")+TunnelPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	waitConnected(t, tunnel, "org.example.ts.pair")
	resp, err := tunnel.RuntimeFor("org.example.ts.pair").Call(ctx, Request{Path: "/manifest.json"})
	if err != nil || !bytes.Contains(resp.Body, []byte(`"ok":true`)) {
		t.Fatalf("response %s: %v; stderr %s", resp.Body, err, stderr.String())
	}
}

func TestURLClientConfigurationOverTLS(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ether/configure" || r.Header.Get(HeaderCredential) != "credential" {
			http.Error(w, "unexpected request", 400)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c, err := NewURLClient(URLOptions{Base: srv.URL, TrustedOrigin: srv.URL, BaseTransport: srv.Client().Transport, Credential: "credential"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	resp, err := c.Call(context.Background(), Request{Method: http.MethodPost, Path: "/ether/configure", Body: []byte(`{"secret":"value"}`)})
	if err != nil || resp.Status != http.StatusNoContent {
		t.Fatalf("configuration status=%d error=%v", resp.Status, err)
	}
}

func TestURLClientErrorDoesNotExposeURLSecrets(t *testing.T) {
	client, err := NewURLClient(URLOptions{Base: "http://127.0.0.1/sensitive-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.Call(context.Background(), Request{Path: "/manifest.json"})
	if !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("expected blocked error, got %v", err)
	}
	if strings.Contains(err.Error(), "sensitive-token") {
		t.Fatalf("error leaked URL: %v", err)
	}
}

func TestFileHandlerRejectsSymlinkOutsideRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	stream, err := FileHandler(root)(context.Background(), Request{Path: "/ether/file/escape"})
	if stream != nil {
		stream.Body.Close()
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("symlink escape: %v", err)
	}
	stream, err = FileHandler(root)(context.Background(), Request{Path: "/ether/file/escape", Headers: map[string][]string{"X-Ether-File-Root": {outside}}})
	if stream != nil {
		stream.Body.Close()
	}
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("mismatched root: %v", err)
	}
}

func TestCancelledStreamUnblocksTunnelDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &pendingCall{chunks: make(chan []byte, 1), start: make(chan startInfo, 1), done: make(chan error, 1), ctxDone: ctx.Done()}
	p.chunks <- []byte("occupied")
	conn := &tunnelConn{pending: map[string]*pendingCall{"1": p}}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn.dispatch(context.Background(), frame{ID: "1", Type: "response_chunk", Data: encodeBody([]byte("more"))})
	}()
	cancel()
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled stream left the tunnel reader blocked")
	}
}

func TestTunnelRejectsCallsAfterCredentialRevocation(t *testing.T) {
	var revoked atomic.Bool
	auth := CredentialResolverFunc(func(_ context.Context, credential string) (string, error) {
		if credential != "test-credential" || revoked.Load() {
			return "", ErrUnauthorized
		}
		return "org.example.revocation", nil
	})
	tunnel := NewTunnelServer(TunnelOptions{Auth: auth})
	defer tunnel.Close()
	srv := httptest.NewServer(tunnel.Handler())
	defer srv.Close()
	client, err := NewTunnelClient(TunnelClientOptions{URL: srv.URL + TunnelPath, Credential: "test-credential", Plugin: "org.example.revocation", Handler: func(context.Context, Request) (Response, error) { return Response{Status: 200}, nil }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.ServeOnce(ctx) }()
	defer func() { cancel(); tunnel.Close(); <-done }()
	waitConnected(t, tunnel, "org.example.revocation")
	rt := tunnel.RuntimeFor("org.example.revocation")
	if _, err := rt.Call(ctx, Request{Path: "/test"}); err != nil {
		t.Fatal(err)
	}
	revoked.Store(true)
	if _, err := rt.Call(ctx, Request{Path: "/test"}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("revoked tunnel call: %v", err)
	}
}

func TestFileHandlerRejectsTraversalEvenIfTargetExistsInRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "video"), []byte("video"), 0600); err != nil {
		t.Fatal(err)
	}
	stream, err := FileHandler(root)(context.Background(), Request{Path: "/ether/file/../video"})
	if stream != nil {
		stream.Body.Close()
	}
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("traversal accepted: %v", err)
	}
}

func TestPlainStremioPublicRedirectsAndEncoding(t *testing.T) {
	var receivedPath, auth, cookie, referer, pick string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/catalog/movie/top.json":
			http.Redirect(w, r, "http://second.example.test/catalog/movie/top/search=space%20travel.json", http.StatusTemporaryRedirect)
		case "/private":
			http.Redirect(w, r, "http://lan.example.test/secret", http.StatusFound)
		default:
			receivedPath = r.URL.EscapedPath()
			auth = r.Header.Get("Authorization")
			cookie = r.Header.Get("Cookie")
			referer = r.Header.Get("Referer")
			pick = r.Header.Get(HeaderPick)
			_, _ = w.Write([]byte(`{"metas":[]}`))
		}
	}))
	defer srv.Close()
	c, err := NewURLClient(URLOptions{Base: "http://addon.example.test", Guard: publicGuard(t, srv), AllowPublicRedirects: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.Call(context.Background(), Request{Path: "/catalog/movie/top.json", Headers: map[string][]string{"Authorization": {"secret"}, "Cookie": {"secret"}, "Referer": {"secret"}, HeaderPick: {"secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if auth != "" || cookie != "" || referer != "" || pick != "" {
		t.Fatalf("sensitive headers reached CDN")
	}
	if receivedPath != "/catalog/movie/top/search=space%20travel.json" {
		t.Fatalf("redirect path %q", receivedPath)
	}
	if _, err = c.Call(context.Background(), Request{Path: "/private"}); !errors.Is(err, safefetch.ErrBlocked) {
		t.Fatalf("private redirect allowed: %v", err)
	}
	_, err = c.Call(context.Background(), Request{Path: "/catalog/movie/top/search=space%20travel.json"})
	if err != nil {
		t.Fatal(err)
	}
	if receivedPath != "/catalog/movie/top/search=space%20travel.json" {
		t.Fatalf("path escaped twice: %q", receivedPath)
	}
	if _, err = c.Call(context.Background(), Request{Path: "/%2e%2e/secret"}); err == nil {
		t.Fatal("encoded traversal accepted")
	}
}

// Ether identifies itself honestly to a third-party add-on and sends it
// no X-Ether-* header.
func TestPlainStremioClientSendsHonestUserAgent(t *testing.T) {
	var ua, ether string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua, ether = r.UserAgent(), r.Header.Get(HeaderRequestID)
		_, _ = w.Write([]byte(`{"streams":[]}`))
	}))
	defer srv.Close()
	c, err := NewURLClient(URLOptions{Base: "http://addon.example.test/SECRETKEY123/manifest.json", Guard: publicGuard(t, srv), Plain: true})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Call(context.Background(), Request{Path: "/stream/movie/tt1.json", RequestID: "req-1"}); err != nil {
		t.Fatal(err)
	}
	if ua != "Ether (+https://github.com/braedonsaunders/ether)" || ether != "" {
		t.Fatalf("user agent %q, X-Ether-Request-ID %q", ua, ether)
	}
}
