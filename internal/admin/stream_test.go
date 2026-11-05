package admin

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
)

func TestStreamHubPublishAssignsIDs(t *testing.T) {
	hub := NewStreamHub()
	for want := uint64(1); want <= 3; want++ {
		ev, err := hub.Publish(StreamNotice, json.RawMessage(`{"n":1}`))
		if err != nil {
			t.Fatal(err)
		}
		if ev.ID != want || ev.Type != StreamNotice {
			t.Fatalf("event %+v, want id %d type notice", ev, want)
		}
	}
	if _, err := hub.Publish("bogus.type", nil); err == nil {
		t.Fatal("expected an error for an unknown event type")
	}
}

func TestStreamHubSubscribeResume(t *testing.T) {
	hub := NewStreamHub()
	for i := 0; i < 3; i++ {
		if _, err := hub.Publish(StreamNotice, json.RawMessage(fmt.Sprintf(`{"n":%d}`, i))); err != nil {
			t.Fatal(err)
		}
	}
	ch, cancel := hub.Subscribe(2)
	defer cancel()
	select {
	case ev := <-ch:
		if ev.ID != 3 {
			t.Fatalf("resumed at id %d, want 3", ev.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no replayed event")
	}
	if _, err := hub.Publish(StreamNotice, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		if ev.ID != 4 {
			t.Fatalf("live event id %d, want 4", ev.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no live event")
	}
}

func TestStreamHubReplayTrimsToConnCap(t *testing.T) {
	hub := NewStreamHub()
	const total = 200
	for i := 0; i < total; i++ {
		if _, err := hub.Publish(StreamNotice, nil); err != nil {
			t.Fatal(err)
		}
	}
	ch, cancel := hub.Subscribe(0)
	defer cancel()
	for i := 0; i < streamConnCap; i++ {
		select {
		case ev := <-ch:
			if i == 0 && ev.ID != total-streamConnCap+1 {
				t.Fatalf("first replayed id %d, want %d", ev.ID, total-streamConnCap+1)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("only got %d of %d replayed events", i, streamConnCap)
		}
	}
	select {
	case ev := <-ch:
		t.Fatalf("extra replayed event %d", ev.ID)
	default:
	}
}

func TestStreamHubSlowConsumerEvicted(t *testing.T) {
	hub := NewStreamHub()
	ch, cancel := hub.Subscribe(0)
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < streamConnCap+10; i++ {
			if _, err := hub.Publish(StreamNotice, nil); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publish blocked on a slow consumer")
	}
	// The 64 buffered events still drain; then the channel is closed.
	for i := 0; i < streamConnCap; i++ {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatalf("only drained %d of %d buffered events", i, streamConnCap)
		}
	}
	if _, ok := <-ch; ok {
		t.Fatal("expected the evicted consumer channel to be closed")
	}
}

// readLine reads one line or fails the test after a grace period.
func readLine(t *testing.T, r *bufio.Reader) string {
	t.Helper()
	type result struct {
		line string
		err  error
	}
	out := make(chan result, 1)
	go func() {
		line, err := r.ReadString('\n')
		out <- result{line, err}
	}()
	select {
	case res := <-out:
		if res.err != nil {
			t.Fatalf("reading stream: %v", res.err)
		}
		return strings.TrimSuffix(res.line, "\n")
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a stream line")
		return ""
	}
}

func openStream(t *testing.T, srv *httptest.Server, target string, header map[string]string) (*http.Response, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+target, nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	return resp, cancel
}

func TestStreamHandlerFraming(t *testing.T) {
	hub := NewStreamHub()
	if _, err := hub.Publish(StreamNotice, json.RawMessage(`{"msg":"before connect"}`)); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(StreamHandler(hub))
	defer srv.Close()

	resp, cancel := openStream(t, srv, "/", nil)
	defer cancel()
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q, want text/event-stream", ct)
	}
	r := bufio.NewReader(resp.Body)
	if got := readLine(t, r); got != "retry: 10000" {
		t.Fatalf("first line %q, want retry", got)
	}
	if got := readLine(t, r); got != "" {
		t.Fatalf("expected a blank line, got %q", got)
	}
	// The pre-connect event replays.
	for _, want := range []string{"id: 1", "event: notice", `data: {"msg":"before connect"}`, ""} {
		if got := readLine(t, r); got != want {
			t.Fatalf("line %q, want %q", got, want)
		}
	}
	// A live event arrives within a second of publishing.
	if _, err := hub.Publish(StreamAgentApproval, json.RawMessage(`{"id":"a1"}`)); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"id: 2", "event: agent.approval", `data: {"id":"a1"}`, ""} {
		if got := readLine(t, r); got != want {
			t.Fatalf("line %q, want %q", got, want)
		}
	}
}

func TestStreamHandlerResume(t *testing.T) {
	hub := NewStreamHub()
	for _, typ := range []string{StreamNotice, StreamPluginsChange, StreamStudioReload} {
		if _, err := hub.Publish(typ, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(StreamHandler(hub))
	defer srv.Close()

	cases := map[string]struct {
		target string
		header map[string]string
	}{
		"header": {target: "/", header: map[string]string{"Last-Event-ID": "1"}},
		"query":  {target: "/?lastEventId=1"},
	}
	for name, tc := range cases {
		resp, cancel := openStream(t, srv, tc.target, tc.header)
		r := bufio.NewReader(resp.Body)
		if got := readLine(t, r); got != "retry: 10000" {
			cancel()
			t.Fatalf("%s: first line %q, want retry", name, got)
		}
		if got := readLine(t, r); got != "" {
			cancel()
			t.Fatalf("%s: expected a blank line, got %q", name, got)
		}
		// Event 1 is skipped; resume starts at event 2.
		if got := readLine(t, r); got != "id: 2" {
			cancel()
			t.Fatalf("%s: resumed at %q, want id 2", name, got)
		}
		if got := readLine(t, r); got != "event: plugins.changed" {
			cancel()
			t.Fatalf("%s: resumed type %q, want plugins.changed", name, got)
		}
		cancel()
		resp.Body.Close()
	}
}

// syncFlushWriter is a ResponseWriter the serve loop can write to while the
// test reads: writes are serialized with reads, and every Flush signals the
// reader instead of the reader polling a shared buffer.
type syncFlushWriter struct {
	header  http.Header
	mu      sync.Mutex
	body    strings.Builder
	flushed chan struct{}
}

func newSyncFlushWriter() *syncFlushWriter {
	return &syncFlushWriter{header: http.Header{}, flushed: make(chan struct{}, 1)}
}

func (w *syncFlushWriter) Header() http.Header { return w.header }
func (w *syncFlushWriter) WriteHeader(int)     {}
func (w *syncFlushWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.Write(p)
}
func (w *syncFlushWriter) Flush() {
	select {
	case w.flushed <- struct{}{}:
	default:
	}
}
func (w *syncFlushWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.body.String()
}

func TestStreamHandlerHeartbeat(t *testing.T) {
	hub := NewStreamHub()
	w := newSyncFlushWriter()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/api/stream/", nil).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		serveStreamEvents(w, req, hub, 0, 15*time.Millisecond, w)
	}()
	timeout := time.After(10 * time.Second)
	for !strings.Contains(w.String(), ": heartbeat") {
		select {
		case <-w.flushed:
		case <-timeout:
			cancel()
			<-done
			t.Fatalf("no heartbeat in body %q", w.String())
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve loop did not stop after cancel")
	}
}

func TestStreamHandlerRejectsNonGET(t *testing.T) {
	hub := NewStreamHub()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/stream/", nil)
	StreamHandler(hub).ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status %d, want 405", rec.Code)
	}
}

func TestStreamAuthBehindExtensionsMount(t *testing.T) {
	hub := NewStreamHub()
	store, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(c *config.Config) error {
		c.SetupComplete = true
		c.Plex.Token = "owner-secret-token"
		c.Plex.Claimed = true
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	isOwner := func(_ context.Context, tok string) bool { return tok == "owner-secret-token" }
	mux := NewHandler(Deps{Config: store, IsOwnerToken: isOwner, Extensions: map[string]http.Handler{
		"stream": StreamHandler(hub),
	}})

	// No token on a closed setup: unauthorized, and nothing streams.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stream/", nil)
	req.RemoteAddr = "192.168.1.50:49152"
	req.Host = "192.168.1.10:32400"
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status %d, want 401", rec.Code)
	}

	// Slashless extension roots now dispatch directly through authentication,
	// avoiding redirects that drop the outer /ether console prefix.
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/ether/api/stream", nil)
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || rec.Header().Get("Location") != "" {
		t.Fatalf("slashless unauthenticated stream: status=%d location=%q", rec.Code, rec.Header().Get("Location"))
	}

	// An owner token opens the stream; the token travels in a header.
	srv := httptest.NewServer(mux)
	defer srv.Close()
	resp, cancel := openStream(t, srv, "/api/stream/", map[string]string{
		"X-Ether-Admin-Token": "owner-secret-token",
	})
	defer cancel()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authenticated status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q, want text/event-stream", ct)
	}
	r := bufio.NewReader(resp.Body)
	if got := readLine(t, r); got != "retry: 10000" {
		t.Fatalf("first line %q, want retry", got)
	}
}
