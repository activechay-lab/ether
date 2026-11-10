package listcache

import (
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/frontend"
)

type renewals struct{ items []frontend.ImageBatchItem }

func (r *renewals) RenewPage(_ context.Context, req frontend.ImagePageRenewal) error {
	if req.Credential != "secret" {
		return errors.New("wrong credential")
	}
	r.items = append(r.items, req.Items...)
	return nil
}

func settle(t *testing.T, s *Store) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); s.Busy() > 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("background work did not finish")
		}
	}
}

func serve(t *testing.T, s *Store, req Request, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/list", nil)
	for k, v := range header {
		r.Header[k] = v
	}
	if !s.Serve(w, r, req) {
		t.Fatal("store declined")
	}
	return w
}

// A listing is built once, then served as a file with its tag, gzip
// encoding and artwork renewal; staleness is bounded and failures answer
// as an encoding request would.
func TestListingLifecycle(t *testing.T) {
	s := New(t.TempDir())
	t.Cleanup(s.Close)
	var clock atomic.Int64
	clock.Store(time.Unix(1_800_000_000, 0).UnixNano())
	s.now = func() time.Time { return time.Unix(0, clock.Load()) }
	var built atomic.Int64
	body := `[` + strings.Repeat(`{"name":"Film"},`, 5000) + `{"name":"Last"}]`
	renewer := &renewals{}
	req := func(stamp string, catalog uint64, judged bool) Request {
		return Request{Slot: "movie", State: State{Stamp: stamp, Catalog: catalog, Judged: judged, At: s.now()}, Owner: "device",
			Build: func(_ context.Context, e *Entry) error {
				built.Add(1)
				if err := e.KeepImages([]frontend.ImageBatchItem{{Item: 7, Images: []frontend.GrantedImage{{Secret: "tag", Image: frontend.Image{Kind: "poster", Path: "/p.jpg"}}}}}); err != nil {
					return err
				}
				_, err := io.WriteString(e, body)
				return err
			},
			Renew: func(ctx context.Context, e *Entry) error {
				return RenewImages(ctx, e, renewer, "secret", frontend.Device{})
			},
		}
	}
	w := serve(t, s, req("a", 1, false), nil)
	if w.Body.String() != body || built.Load() != 1 || w.Header().Get("ETag") == "" {
		t.Fatalf("followed build: %d builds, %q", built.Load(), w.Header())
	}
	settle(t, s)
	etag := w.Header().Get("ETag")
	if w = serve(t, s, req("a", 1, false), http.Header{"If-None-Match": {etag}}); w.Code != http.StatusNotModified || built.Load() != 1 {
		t.Fatalf("revalidation answered %d after %d builds", w.Code, built.Load())
	}
	w = serve(t, s, req("a", 1, false), http.Header{"Accept-Encoding": {"gzip"}})
	zr, err := gzip.NewReader(w.Body)
	if err != nil || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("gzip: %v %q", err, w.Header())
	}
	if got, _ := io.ReadAll(zr); string(got) != body {
		t.Fatal("gzip body differs")
	}
	// The builder's grants are fresh; another credential renews them.
	if len(renewer.items) != 0 {
		t.Fatal("renewed the builder's own grants")
	}
	other := req("a", 1, false)
	other.Owner = "other"
	serve(t, s, other, nil)
	if len(renewer.items) != 1 || renewer.items[0].Item != 7 || renewer.items[0].Images[0].Secret != "tag" || renewer.items[0].Images[0].Image.Path != "/p.jpg" {
		t.Fatalf("renewal %+v", renewer.items)
	}
	// Another policy is never served this listing.
	if w = serve(t, s, req("b", 1, false), nil); built.Load() != 2 {
		t.Fatal("served a listing built under another stamp")
	}
	settle(t, s)
	// A catalogue write: an unjudged listing is served within the bound and
	// rebuilt behind it; past the bound it is not served.
	if stale := serve(t, s, req("b", 2, false), nil); stale.Header().Get("ETag") != w.Header().Get("ETag") {
		t.Fatal("unjudged listing not served within its bound")
	}
	settle(t, s)
	if built.Load() != 3 {
		t.Fatalf("stale listing not rebuilt (%d builds)", built.Load())
	}
	clock.Add(int64(Stale + time.Second))
	if serve(t, s, req("b", 2, false), nil); built.Load() != 4 {
		t.Fatal("served a listing past its staleness bound")
	}
	// A judged listing is never served across a catalogue write.
	settle(t, s)
	if serve(t, s, req("b", 3, true), nil); built.Load() != 5 {
		t.Fatal("judged listing served across a catalogue write")
	}
	settle(t, s)
	// A build that fails before its first byte answers 503; after it, the
	// response is cut short.
	for _, late := range []bool{false, true} {
		failing := req("c", 3, true)
		failing.Slot = "failing"
		failing.Build = func(_ context.Context, e *Entry) error {
			if late {
				_, _ = io.WriteString(e, "[")
			}
			return errors.New("catalogue unavailable")
		}
		aborted := false
		func() {
			defer func() {
				if p := recover(); p != nil {
					aborted = p == http.ErrAbortHandler
				}
			}()
			if w = serve(t, s, failing, nil); w.Code != http.StatusServiceUnavailable {
				t.Fatalf("early failure answered %d", w.Code)
			}
		}()
		if aborted != late {
			t.Fatalf("late=%v aborted=%v", late, aborted)
		}
		settle(t, s)
	}
	if !acceptsGzip("deflate, gzip;q=0.5") || acceptsGzip("gzip;q=0, *") || !acceptsGzip("*") || acceptsGzip("") {
		t.Fatal("Accept-Encoding negotiation")
	}
}

// Catalogue writes during a scan must not queue another whole-library build
// behind every poll: the first write rebuilds promptly, and the rest of
// the write stream coalesces until the rebuild floor passes.
func TestCatalogueChurnDoesNotRebuildHotListing(t *testing.T) {
	s := New(t.TempDir())
	t.Cleanup(s.Close)
	var clock atomic.Int64
	clock.Store(time.Unix(1_800_000_000, 0).UnixNano())
	s.now = func() time.Time { return time.Unix(0, clock.Load()) }
	var built atomic.Int64
	req := func(catalog uint64) Request {
		return Request{Slot: "movie", State: State{Stamp: "a", Catalog: catalog, At: s.now()}, Owner: "device",
			Build: func(_ context.Context, e *Entry) error {
				built.Add(1)
				_, err := io.WriteString(e, "[]")
				return err
			}}
	}
	serve(t, s, req(1), nil)
	settle(t, s)
	// The catalogue generation moves under every poll, seconds apart.
	for gen := uint64(2); gen <= 6; gen++ {
		clock.Add(int64(5 * time.Second))
		serve(t, s, req(gen), nil)
		settle(t, s)
	}
	if built.Load() != 2 {
		t.Fatalf("catalogue churn rebuilt a hot listing: %d builds, want the prompt first only", built.Load())
	}
	// Once the listing ages out, one write revalidates it once.
	clock.Add(int64(Revalidate))
	serve(t, s, req(7), nil)
	settle(t, s)
	if built.Load() != 3 {
		t.Fatalf("aged listing not revalidated: %d builds", built.Load())
	}
}

// Concurrent cold requests for one slot follow a single build.
func TestConcurrentColdServesBuildOnce(t *testing.T) {
	s := New(t.TempDir())
	t.Cleanup(s.Close)
	var built atomic.Int64
	releaseBuild := make(chan struct{})
	req := Request{Slot: "movie", State: State{Stamp: "a", Catalog: 1, At: s.now()}, Owner: "device",
		Build: func(_ context.Context, e *Entry) error {
			built.Add(1)
			<-releaseBuild
			_, err := io.WriteString(e, "[]")
			return err
		}}
	const n = 16
	done := make(chan *httptest.ResponseRecorder, n)
	for range n {
		go func() {
			w := httptest.NewRecorder()
			if !s.Serve(w, httptest.NewRequest("GET", "/list", nil), req) {
				w.Body.WriteString("declined")
			}
			done <- w
		}()
	}
	// Let every request reach the build before it can finish.
	for deadline := time.Now().Add(10 * time.Second); built.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no build started")
		}
	}
	close(releaseBuild)
	for range n {
		if w := <-done; w.Body.String() != "[]" {
			t.Fatalf("cold serve body %q", w.Body.String())
		}
	}
	settle(t, s)
	if built.Load() != 1 {
		t.Fatalf("concurrent cold serves built %d listings", built.Load())
	}
}

// Simultaneous cold Serve and Prepare calls must claim the slot before opening
// files on disk so slow filesystem creation cannot race into multiple builds.
func TestConcurrentColdServeAndPrepareBuildOnce(t *testing.T) {
	s := New(t.TempDir())
	t.Cleanup(s.Close)
	var built atomic.Int64
	req := Request{Slot: "series", State: State{Stamp: "a", Catalog: 1, At: s.now()}, Owner: "device",
		Build: func(_ context.Context, e *Entry) error {
			built.Add(1)
			_, err := io.WriteString(e, `[{"series_id":1}]`)
			return err
		}}
	const serves, prepares = 16, 8
	var ready, done sync.WaitGroup
	ready.Add(serves + prepares)
	done.Add(serves + prepares)
	start := make(chan struct{})
	results := make(chan string, serves)
	for range serves {
		go func() {
			defer done.Done()
			w := httptest.NewRecorder()
			r := httptest.NewRequest("GET", "/list", nil)
			ready.Done()
			<-start
			if !s.Serve(w, r, req) {
				results <- "declined"
				return
			}
			results <- w.Body.String()
		}()
	}
	for range prepares {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			s.Prepare(req)
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
	close(results)
	for body := range results {
		if body != `[{"series_id":1}]` {
			t.Fatalf("cold serve body %q", body)
		}
	}
	settle(t, s)
	if built.Load() != 1 {
		t.Fatalf("concurrent cold serve/prepare built %d listings", built.Load())
	}
}

// Builds for distinct slots do not all encode at once.
func TestBuildsAreBounded(t *testing.T) {
	s := New(t.TempDir())
	t.Cleanup(s.Close)
	var live, peak atomic.Int64
	releaseBuild := make(chan struct{})
	build := func(_ context.Context, e *Entry) error {
		cur := live.Add(1)
		for p := peak.Load(); cur > p && !peak.CompareAndSwap(p, cur); p = peak.Load() {
		}
		<-releaseBuild
		live.Add(-1)
		_, err := io.WriteString(e, "[]")
		return err
	}
	const n = 6
	done := make(chan *httptest.ResponseRecorder, n)
	for i := range n {
		req := Request{Slot: "slot-" + string(rune('a'+i)), State: State{Stamp: "a", Catalog: 1, At: s.now()}, Owner: "device", Build: build}
		go func() {
			w := httptest.NewRecorder()
			if !s.Serve(w, httptest.NewRequest("GET", "/list", nil), req) {
				w.Body.WriteString("declined")
			}
			done <- w
		}()
	}
	for deadline := time.Now().Add(10 * time.Second); live.Load() == 0; time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("no build started")
		}
	}
	// Give queued builds a chance to (wrongly) start, then release all.
	time.Sleep(50 * time.Millisecond)
	got := peak.Load()
	close(releaseBuild)
	if got > 2 {
		t.Fatalf("%d builds encoded at once, bound is 2", got)
	}
	for range n {
		<-done
	}
	settle(t, s)
}
