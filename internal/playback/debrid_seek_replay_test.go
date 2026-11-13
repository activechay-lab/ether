package playback

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontends/mediabrowser"
	"github.com/braedonsaunders/ether/internal/sessions"
)

type replayOutageTransport struct {
	next     http.RoundTripper
	outage   *atomic.Bool
	failures *atomic.Int64
}

func (t replayOutageTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.outage.Load() {
		var start int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &start); err == nil && start >= 1_800_000_000 {
			t.failures.Add(1)
		}
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
	}
	return t.next.RoundTrip(r)
}

// A sparse CDN simulates a large unindexed BD stream without allocating it.
// Connections are held after one block to expose overlapping seek and
// read-ahead work, and counted at the socket rather than request level.
func TestDebridSeekConnectionReplay(t *testing.T) {
	const size int64 = 2_000_000_000
	var mu sync.Mutex
	var current, peak int
	var ranges []string
	var opened []time.Time
	active := make(map[int64]int)
	started := make(chan int64, 8)
	cdn := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			t.Errorf("bad range: %v", err)
			return
		}
		mu.Lock()
		active[start]++
		ranges = append(ranges, fmt.Sprintf("%d-%d", start, end))
		mu.Unlock()
		defer func() { mu.Lock(); active[start]--; mu.Unlock() }()
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, size))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		n := min(end-start+1, int64(512<<10))
		if start > 900_000_000 && start < 1_500_000_000 {
			n = min(n, int64(64<<10))
			started <- start
		}
		if _, err := w.Write(make([]byte, n)); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		if start+n <= end {
			<-r.Context().Done()
		}
	}))
	cdn.Config.ConnState = func(_ net.Conn, st http.ConnState) {
		mu.Lock()
		defer mu.Unlock()
		if st == http.StateNew {
			current++
			peak = max(peak, current)
			opened = append(opened, time.Now())
		}
		if st == http.StateClosed || st == http.StateHijacked {
			current--
		}
	}
	cdn.Start()
	defer cdn.Close()
	svc := debrid.NewService(config.Debrid{}, debrid.Options{})
	var outage atomic.Bool
	var injectedFailures atomic.Int64
	eng := debrid.NewEngine(svc, debrid.EngineOptions{WrapMediaTransport: func(next http.RoundTripper) http.RoundTripper {
		return replayOutageTransport{next: next, outage: &outage, failures: &injectedFailures}
	}})
	defer eng.Close()
	payload := debrid.Payload{ReleaseID: "A", Provider: "torbox-0", Kind: "torbox", InfoHash: "0123456789012345678901234567890123456789", Path: "00001.m2ts", Size: size, URL: cdn.URL, Expires: time.Now().Add(time.Hour)}
	src, err := eng.Open(context.Background(), "A", payload.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	rel := mkv("A", size, 2*time.Hour)
	rel.Name = "00001.m2ts"
	h := newHarness(t, []*fakeRelease{rel}, []string{"A"})
	ctx := context.Background()
	device := core.Device{ID: "replay"}
	caps := defaultCaps(device.ID)
	caps.Protocol = "jellyfin"
	caps.ClientID = "8:jellyfin" + device.ID
	ps, err := h.m.PrepareSurface(ctx, friend, movieKey, caps, "", false)
	if err != nil {
		t.Fatal(err)
	}
	g := h.m.lookup(ps.ID).current()
	<-g.indexReady
	g.src = src
	activeViewer := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := sessions.NewEventBus(nil, activeViewer)
	defer events.Close()
	lookup := func(ctx context.Context, _ *core.Viewer, _ core.Device, key core.RatingKey) (*core.Title, error) {
		return h.catalog.Get(ctx, key)
	}
	broker, err := sessions.NewBroker(sessions.BrokerDeps{SurfacePolicy: brokerTestPolicy, DB: h.m.resume.db, Events: events, Playback: h.m, Transcoder: h.tr, Progress: h.state, ActiveViewer: activeViewer, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 0 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Lookup: lookup, MediaLookup: lookup})
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close(ctx)
	lazy, err := sessions.NewLazyStreams(h.m.resume.db, broker, "jellyfin", &hlsFixtureGrants{}, time.Hour, func(*http.Request) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	plan := frontend.PlanRequest{Viewer: friend, Device: device, Protocol: "jellyfin", Item: movieKey, Caps: caps, Intent: frontend.PlayIntent{Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}}}
	path, err := lazy.Issue(ctx, "account-secret", plan)
	if err != nil {
		t.Fatal(err)
	}
	host := frontend.Host{LazyStreams: lazy, Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*core.Viewer, error) { return friend, nil }}}
	inst, err := (mediabrowser.Jellyfin{MachineID: "offline"}).Mount(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.StripPrefix("/jellyfin", inst.Handler()))
	defer srv.Close()
	client := &http.Client{Timeout: 5 * time.Second}
	request := func(ctx context.Context, off int64) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL+"/jellyfin"+path, nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+4095))
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.Copy(io.Discard, resp.Body)
		if err != nil {
			return err
		}
		if resp.StatusCode != http.StatusPartialContent {
			return fmt.Errorf("range status %d", resp.StatusCode)
		}
		return nil
	}
	if err := request(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	seekCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	for _, off := range []int64{1_010_000_000, 1_040_000_000} {
		go func(off int64) { done <- request(seekCtx, off) }(off)
		select {
		case <-started:
		case <-time.After(4 * time.Second):
			t.Fatal("seek did not reach CDN")
		}
	}
	// A client retires its obsolete requests explicitly. A new range alone
	// must not cancel still-live direct-play reads (including Cues).
	cancel()
	if err := request(context.Background(), 1_640_000_000); err != nil {
		t.Fatal(err)
	}
	time.Sleep(600 * time.Millisecond)
	mu.Lock()
	burst := 0
	for i, at := range opened {
		n := 0
		for j := i; j < len(opened) && opened[j].Sub(at) < time.Second; j++ {
			n++
		}
		burst = max(burst, n)
	}
	old := 0
	for off, n := range active {
		if off < 1_500_000_000 {
			old += n
		}
	}
	t.Logf("seek replay: peak_open=%d new_per_second=%d total_new=%d superseded_inflight=%d ranges=%v", peak, burst, len(opened), old, ranges)
	mu.Unlock()
	cancel()
	for i := 0; i < 2; i++ {
		<-done
	}
	g.mu.Lock()
	pending := g.retryPending
	g.mu.Unlock()
	if pending {
		t.Fatal("superseded seeks were counted as a source outage")
	}
	if old != 0 {
		t.Fatalf("%d superseded upstream requests remain active", old)
	}
	// Keep the seek phase on the real capped transports. For the outage,
	// close the real CDN and inject a prompt refusal at the transport boundary;
	// OS-specific closed-listener timing must not dictate this recovery test.
	outage.Store(true)
	cdn.Listener.Close()
	cdn.CloseClientConnections()
	client.Timeout = 15 * time.Second
	req, _ := http.NewRequest("GET", srv.URL+"/jellyfin"+path, nil)
	req.Header.Set("Range", "bytes=1900000000-1900004095")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("outage status=%d", response.StatusCode)
	}
	if injectedFailures.Load() == 0 {
		t.Fatal("outage did not reach the injected transport failure")
	}
	g.mu.Lock()
	if !g.retryPending {
		g.mu.Unlock()
		t.Fatal("outage did not start bounded recovery")
	}
	g.retryPending = false
	g.retryEpoch++
	g.mu.Unlock()
	h.m.limitDirectRetryAfter(g.sess, g, time.Minute, 20*time.Millisecond)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := h.m.Session(ps.ID); !ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, ok := h.m.Session(ps.ID); ok {
		t.Fatal("unavailable Jellyfin session still active")
	}
	req, _ = http.NewRequest("GET", srv.URL+"/jellyfin"+path, nil)
	req.Header.Set("Range", "bytes=1900000000-1900004095")
	response, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode == http.StatusServiceUnavailable {
		t.Fatal("Jellyfin retried unavailable source indefinitely")
	}

}
