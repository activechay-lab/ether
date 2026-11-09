package debrid

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Issue #396, maintenance variant: the provider pins the failed node into
// every fresh link for as long as the download's link lives, so no API
// answer is a new route. The link path and token still stand on any edge of
// the provider (observed 2026-11-09), so the file must replay the SAME link
// on an edge the engine has seen answer live — same bytes, no second
// provider, no waiting for the link to expire.
func TestRerouteReplaysPinnedLinkOnAnotherEdge(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	live := newMediaServer(t, 4<<20)
	h.cacheEverywhere(testHash)
	cfg := h.svc.Config()
	cfg.ReadAheadChunks = -1
	h.svc.UpdateConfig(cfg)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("pinned-node"))
	if err != nil {
		t.Fatal(err)
	}
	pl, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	deadToken := pl.URL[strings.LastIndex(pl.URL, "/")+1:]
	live.issue(deadToken) // the live edge will serve this link's token

	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()

	// The engine learns the live edge from any file it serves: a witness
	// title is already streaming there.
	witness := pl
	witness.URL = live.issue("witness")
	wsrc, err := e.Open(context.Background(), "witness-file", witness.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer wsrc.Close()
	wbuf := make([]byte, 4096)
	if _, err := wsrc.(readAtContexter).ReadAtContext(context.Background(), wbuf, 0); err != nil {
		t.Fatal(err)
	}

	var links atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		links.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": pl.URL}) // pinned forever
	}))
	defer api.Close()
	h.svc.entryByID("torbox-0").p = newTorBox(testKey, fastOpts(api.URL))

	source, err := e.Open(context.Background(), "pinned-node", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	buf := make([]byte, 4096)
	if _, err := source.(readAtContexter).ReadAtContext(context.Background(), buf, 0); err != nil {
		t.Fatal(err)
	}
	h.media.Close() // the node dies; the provider keeps pinning it

	for _, read := range []struct {
		name string
		off  int64
		fn   func(context.Context, []byte, int64) (int, error)
	}{
		{"background", 1 << 20, source.(*httpSource).ReadAtPrefetchContext},
		{"viewer", 3 << 20, source.(readAtContexter).ReadAtContext},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n, err := read.fn(ctx, buf, read.off)
		cancel()
		if err != nil || n != len(buf) || !bytes.Equal(buf, live.data[read.off:read.off+int64(len(buf))]) {
			t.Fatalf("%s read after the pinned node died: n=%d err=%v", read.name, n, err)
		}
	}
	if got := links.Load(); got != rerouteLinks {
		t.Fatalf("requestdl calls=%d, want %d (all pinned; the replay itself must ask the API nothing)", got, rerouteLinks)
	}
}

// The replay's edges belong to the provider account that minted the token:
// a file on another provider may stream from a fresher edge, yet that host
// must not be handed the signed path and query while the wave replays.
func TestRerouteNeverHandsTheTokenToAnotherProvidersEdge(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox", "realdebrid")
	live := newMediaServer(t, 4<<20)
	foreign := newMediaServer(t, 4<<20)
	h.provs["realdebrid-1"].media = foreign
	tor := h.provs["torbox-0"]
	tor.mu.Lock()
	tor.cached[testHash] = true // only TorBox holds this file; there is no second route but an edge
	tor.mu.Unlock()
	cfg := h.svc.Config()
	cfg.ReadAheadChunks = -1
	h.svc.UpdateConfig(cfg)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("pinned-node"))
	if err != nil {
		t.Fatal(err)
	}
	pl, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	deadToken := pl.URL[strings.LastIndex(pl.URL, "/")+1:]
	live.issue(deadToken) // the live edge will serve this link's token

	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()

	// A witness title is already streaming on the live edge: a TorBox edge.
	witness := pl
	witness.URL = live.issue("witness")
	wsrc, err := e.Open(context.Background(), "witness-file", witness.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer wsrc.Close()
	wbuf := make([]byte, 4096)
	if _, err := wsrc.(readAtContexter).ReadAtContext(context.Background(), wbuf, 0); err != nil {
		t.Fatal(err)
	}

	source, err := e.Open(context.Background(), "pinned-node", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	buf := make([]byte, 4096)
	if _, err := source.(readAtContexter).ReadAtContext(context.Background(), buf, 0); err != nil {
		t.Fatal(err)
	}

	// The other provider's file now completes a block on its own edge:
	// fresher than live, but not this link's provider.
	rd := pl
	rd.Provider = "realdebrid-1"
	rd.URL = foreign.issue("foreign")
	rsrc, err := e.Open(context.Background(), "foreign-file", rd.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer rsrc.Close()
	if _, err := rsrc.(readAtContexter).ReadAtContext(context.Background(), wbuf, 0); err != nil {
		t.Fatal(err)
	}
	foreignSeen := foreign.requests.Load()

	var links atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		links.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": pl.URL}) // pinned forever
	}))
	defer api.Close()
	h.svc.entryByID("torbox-0").p = newTorBox(testKey, fastOpts(api.URL))
	h.media.Close() // the node dies; the provider keeps pinning it

	for _, read := range []struct {
		name string
		off  int64
		fn   func(context.Context, []byte, int64) (int, error)
	}{
		{"background", 1 << 20, source.(*httpSource).ReadAtPrefetchContext},
		{"viewer", 3 << 20, source.(readAtContexter).ReadAtContext},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		n, err := read.fn(ctx, buf, read.off)
		cancel()
		if err != nil || n != len(buf) || !bytes.Equal(buf, live.data[read.off:read.off+int64(len(buf))]) {
			t.Fatalf("%s read after the pinned node died: n=%d err=%v", read.name, n, err)
		}
	}
	if got := links.Load(); got != rerouteLinks {
		t.Fatalf("requestdl calls=%d, want %d (the replay itself must ask the API nothing)", got, rerouteLinks)
	}
	if got := foreign.requests.Load() - foreignSeen; got != 0 {
		t.Fatalf("another account's edge answered %d replay requests; the signed path left its provider", got)
	}
}

// A probe is the evidence a route is taken on: only a whole 206 for the
// asked offset of the expected file size proves an edge carries the link.
func TestProbeLinkRejectsFalsePositiveAnswers(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	const off, size = int64(1 << 20), int64(4 << 20)
	exact := fmt.Sprintf("bytes %d-%d/%d", off, off+probeSample-1, size)
	for _, c := range []struct {
		name    string
		handler http.HandlerFunc
		want    bool
	}{
		{"204 no content", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}, false},
		{"200 HTML error page", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<html>gone</html>"))
		}, false},
		{"206 for another offset", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", probeSample-1, size))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(make([]byte, probeSample))
		}, false},
		{"206 for another file size", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, off+probeSample-1, size/4))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(make([]byte, probeSample))
		}, false},
		{"206 truncated short", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Range", exact)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("five"))
		}, false},
		{"302 to an unapproved host", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "http://127.0.0.1:1/hop")
			w.WriteHeader(http.StatusFound)
		}, false},
		{"a whole 206 wins", func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Range"); got != fmt.Sprintf("bytes=%d-%d", off, off+probeSample-1) {
				t.Errorf("probe asked for %s, want the failed read's offset", got)
			}
			w.Header().Set("Content-Range", exact)
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(make([]byte, probeSample))
		}, true},
	} {
		srv := httptest.NewServer(c.handler)
		if got := e.probeLink(context.Background(), srv.URL+"/dl/link?auth=xyz", off, size); got != c.want {
			t.Errorf("%s: probeLink=%v, want %v", c.name, got, c.want)
		}
		srv.Close()
	}
}

// TorBox keeps a link's token inside its own CDN: candidates are vetted
// against the host the link itself names, before any byte is sent.
func TestTorBoxApprovesCDNEdge(t *testing.T) {
	p := newTorBox(testKey, fastOpts("http://api.invalid"))
	raw := "http://nexus-096.torboxcdn.example/dl/abc?auth=1"
	for _, c := range []struct {
		host string
		want bool
	}{
		{"nexus-132.torboxcdn.example:443", true}, // the incident's live edges
		{"deep.edge.torboxcdn.example", true},     // any host under the parent domain
		{"nexus-096.torboxcdn.example:8443", true},
		{"torboxcdn.example:443", false},          // the parent is not claimed by its subdomains alone
		{"torboxcdn.example.evil.net:443", false}, // …nor wears the parent as a suffix
		{"another-cdn.net:443", false},
		{"127.0.0.1:9999", false}, // IP literals match host-to-host only
		{"", false},
	} {
		if got := p.approvesCDNEdge(raw, c.host); got != c.want {
			t.Errorf("approvesCDNEdge(%q)=%v, want %v", c.host, got, c.want)
		}
	}
}

// Edge evidence belongs to the account that watched the block complete:
// freshest-first, minus the failed host, minus stale and down hosts.
func TestRecentEdgesKeepEvidencePerProviderAccount(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	var r recentEdges
	r.record("torbox-0", "nexus-132:443", now.Add(-time.Minute))
	r.record("realdebrid-1", "spare-edge:443", now)
	if got := r.freshest("torbox-0", "", nil, now); len(got) != 1 || got[0] != "nexus-132:443" {
		t.Fatalf("another account's edge leaked into the candidate list: %v", got)
	}
	r.record("torbox-0", "nexus-096:443", now)
	if got := r.freshest("torbox-0", "nexus-096:443", func(string) bool { return false }, now); len(got) != 1 || got[0] != "nexus-132:443" {
		t.Fatalf("freshest-first or exclude broken: %v", got)
	}
	if got := r.freshest("torbox-0", "nexus-096:443", func(h string) bool { return h == "nexus-132:443" }, now); len(got) != 0 {
		t.Fatalf("a down host counted as a route on freshness alone: %v", got)
	}
	if got := r.freshest("torbox-0", "", nil, now.Add(recentEdgeTTL+time.Minute)); len(got) != 0 {
		t.Fatalf("stale edges outlived the TTL: %v", got)
	}
}

// A refresh can bump the generation while a probe is in flight and leave
// the file on the same dead host with a newer token. The stale probe's 206
// must not count as a win: the fresh link runs through the same usable
// predicate as the rest of the wave, and a still-pinned file gets its new
// link tried against the remembered edges rather than a claimed replay.
func TestResumeOnAnotherEdgeDiscardsProbeStaledByRefresh(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	live := newMediaServer(t, 4<<20)
	tor := h.provs["torbox-0"]
	tor.mu.Lock()
	tor.cached[testHash] = true
	tor.mu.Unlock()
	cfg := h.svc.Config()
	cfg.ReadAheadChunks = -1
	h.svc.UpdateConfig(cfg)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("stale-probe"))
	if err != nil {
		t.Fatal(err)
	}
	pl, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	tok1 := pl.URL[strings.LastIndex(pl.URL, "/")+1:]
	live.issue(tok1) // the live edge carries the pinned link's token
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()

	// A witness title streams on the live edge: it is the remembered edge
	// for this account's files.
	witness := pl
	witness.URL = live.issue("witness")
	wsrc, err := e.Open(context.Background(), "witness-file", witness.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer wsrc.Close()
	wbuf := make([]byte, 4096)
	if _, err := wsrc.(readAtContexter).ReadAtContext(context.Background(), wbuf, 0); err != nil {
		t.Fatal(err)
	}

	h.svc.entryByID("torbox-0").p = newTorBox(testKey, fastOpts("http://api.invalid")) // the API is never asked here
	source, err := e.Open(context.Background(), "stale-probe", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	f := source.(*httpSource).f

	failed := pl.URL
	failedAddr := dialAddr(failed)
	usable := func(u string) bool {
		return u != "" && dialAddr(u) != failedAddr && !e.backoff.down(urlHost(u))
	}
	const off = int64(1 << 20)
	probeRange := fmt.Sprintf("bytes=%d-%d", off, off+probeSample-1)
	// The edge carries the old token but stalls its probes for this offset,
	// so the test can move the link while a probe is in flight.
	live.mu.Lock()
	live.stall[tok1+"|"+probeRange] = 400 * time.Millisecond
	live.mu.Unlock()
	res := make(chan [2]bool, 1)

	// Phase 1: the refresh re-pins the file to the dead host. No move, no
	// replay claim — and the fresh token does get its chance on the edge.
	inflight := func(before int64) {
		for now := time.Now(); time.Now().Before(now.Add(3 * time.Second)); time.Sleep(5 * time.Millisecond) {
			if live.requests.Load() > before {
				return
			}
		}
		t.Fatal("the probe never reached the live edge")
	}
	seen := live.requests.Load()
	go func() {
		moved, replayed := f.resumeOnAnotherEdge(context.Background(), failed, usable, off)
		res <- [2]bool{moved, replayed}
	}()
	inflight(seen)
	q := pl
	q.URL = h.media.URL + "/m/repinned" // the provider pins again: same host, newer token
	q.Expires = q.Expires.Add(time.Hour)
	f.adopt(q)
	if got := <-res; got != [2]bool{false, false} {
		t.Fatalf("moved/replayed=%v, want false/false: a stale-probed refresh that stayed pinned is no replay", got)
	}
	if p, _ := f.payload(); p.URL != q.URL {
		t.Fatalf("the helper overwrote the refresh: %s, want %s", p.URL, q.URL)
	}
	if got := live.requests.Load() - seen; got != 2 {
		t.Fatalf("probe requests=%d, want 2: the stale 206 discarded, then the fresh link tried on the edge once", got)
	}

	// Phase 2: a refresh moves the file to the live edge while the second
	// probe is in flight. The mover won; the helper reports the win without
	// claiming the replay.
	live.issue("repinned") // this time that token does answer on the edge
	live.mu.Lock()
	live.stall["repinned|"+probeRange] = 400 * time.Millisecond
	live.mu.Unlock()
	seen = live.requests.Load()
	go func() {
		moved, replayed := f.resumeOnAnotherEdge(context.Background(), failed, usable, off)
		res <- [2]bool{moved, replayed}
	}()
	inflight(seen)
	q2 := q
	q2.URL = live.issue("moved")
	q2.Expires = q2.Expires.Add(time.Hour)
	f.adopt(q2)
	if got := <-res; got != [2]bool{true, false} {
		t.Fatalf("moved/replayed=%v, want true/false: the mover won the move, not this replay", got)
	}
	if p, _ := f.payload(); p.URL != q2.URL {
		t.Fatalf("the helper overwrote the move: %s, want %s", p.URL, q2.URL)
	}
	if got := live.requests.Load() - seen; got != 1 {
		t.Fatalf("probe requests=%d, want 1: a won move asks the edges nothing more", got)
	}
}
