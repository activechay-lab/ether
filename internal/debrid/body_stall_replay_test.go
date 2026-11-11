package debrid

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A node can accept every Range and return valid headers while repeatedly
// stalling before a complete block. A second provider is not required to
// recover if an approved edge can deliver the same signed file.
func TestPersistentBodyStallReplaysSameFileOnHealthyEdge(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	h.cacheEverywhere(testHash)
	cfg := h.svc.Config()
	cfg.ReadAheadChunks = -1
	h.svc.UpdateConfig(cfg)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("stalled-edge"))
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := DecodePayload(raw)
	live := newMediaServer(t, 4<<20)
	live.issue(pl.URL[strings.LastIndex(pl.URL, "/")+1:])
	h.media.stallEvery.Store(123)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": pl.URL})
	}))
	defer api.Close()
	h.svc.entryByID(pl.Provider).p = fixtureTorBox(api.URL)
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	e.stall = 80 * time.Millisecond
	e.recent.record(pl.Provider, urlHost(live.URL), e.now())
	src, err := e.Open(context.Background(), "stalled-edge", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const off = 1 << 20
	buf := make([]byte, 4096)
	n, err := src.(readAtContexter).ReadAtContext(ctx, buf, off)
	if err != nil || n != len(buf) || !bytes.Equal(buf, live.data[off:off+len(buf)]) {
		t.Fatalf("body-stall recovery n=%d err=%v", n, err)
	}
	p, _ := src.(*httpSource).f.payload()
	if p.Provider != pl.Provider || urlHost(p.URL) != urlHost(live.URL) || !p.OnReplayEdge {
		t.Fatal("stalled stream did not adopt the same account's replay edge")
	}
	waitNoInflight(t, h.media)
	waitNoInflight(t, live)
}

// A native link may redirect to its serving CDN. That host differs from
// the payload's host, so the new replay guard must preserve the existing
// first-interruption alternate-provider fallback.
func TestRedirectedBodyStallRetainsAlternateProviderFallback(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox", "realdebrid")
	live := newMediaServer(t, 4<<20)
	h.provs["realdebrid-1"].media = live
	h.cacheEverywhere(testHash)
	cfg := h.svc.Config()
	cfg.ReadAheadChunks = -1
	h.svc.UpdateConfig(cfg)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("redirected-stall"))
	if err != nil {
		t.Fatal(err)
	}
	pl, err := DecodePayload(raw)
	if err != nil {
		t.Fatal(err)
	}
	if pl.Provider != "torbox-0" {
		t.Fatalf("initial provider = %s", pl.Provider)
	}
	// Every request follows the same redirect and then stalls. An
	// accidental retry of the original route cannot heal the fixture.
	target := pl.URL
	h.media.stallEvery.Store(123)
	var redirects atomic.Int32
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirects.Add(1)
		http.Redirect(w, r, target, http.StatusFound)
	}))
	defer redirect.Close()
	pl.URL = redirect.URL + "/file"
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	e.stall = 80 * time.Millisecond
	src, err := e.Open(context.Background(), "redirected-stall", pl.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	const off = 1 << 20
	buf := make([]byte, 4096)
	n, err := src.(readAtContexter).ReadAtContext(ctx, buf, off)
	if err != nil || n != len(buf) || !bytes.Equal(buf, live.data[off:off+len(buf)]) {
		t.Fatalf("redirected stall recovery n=%d err=%v", n, err)
	}
	p, _ := src.(*httpSource).f.payload()
	if p.Provider != "realdebrid-1" || urlHost(p.URL) != urlHost(live.URL) {
		t.Fatalf("legacy provider fallback lost: provider=%s host=%s", p.Provider, urlHost(p.URL))
	}
	if redirects.Load() != 1 || h.media.requests.Load() != 1 {
		t.Fatalf("did not switch on first interruption: redirects=%d stalled requests=%d",
			redirects.Load(), h.media.requests.Load())
	}
	waitNoInflight(t, h.media)
	waitNoInflight(t, live)
}
