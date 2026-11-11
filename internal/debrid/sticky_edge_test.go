package debrid

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRerouteReplayBypassesAPICooldown(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	h.cacheEverywhere(testHash)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("independent-replay"))
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := DecodePayload(raw)
	live := newMediaServer(t, 4<<20)
	live.issue(pl.URL[strings.LastIndex(pl.URL, "/")+1:])
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		// A healthy replay must never wait for this API.
		<-r.Context().Done()
	}))
	defer api.Close()
	h.svc.entryByID(pl.Provider).p = fixtureTorBox(api.URL)
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	src, err := e.Open(context.Background(), "independent-replay", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	f := src.(*httpSource).f
	f.mu.Lock()
	f.rerouteAt = e.now().Add(time.Hour)
	f.mu.Unlock()
	e.recent.record(pl.Provider, urlHost(live.URL), e.now())
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if !f.reroute(ctx, pl.URL, 0, 1<<20) {
		t.Fatal("API cooldown suppressed replay")
	}
	if calls.Load() != 0 {
		t.Fatal("replay called API")
	}
	body, _, _, err := f.rangeGet(ctx, 1<<20, (1<<20)+4095, true)
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	buf := make([]byte, 4096)
	if _, err := io.ReadFull(body, buf); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, live.data[1<<20:(1<<20)+4096]) {
		t.Fatal("wrong resumed bytes")
	}
}

func TestRerouteOtherProvidersRetainRefreshWave(t *testing.T) {
	h := newHarness(t, 4<<20, "realdebrid")
	h.cacheEverywhere(testHash)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("other-provider-wave"))
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := DecodePayload(raw)
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	src, err := e.Open(context.Background(), "other-provider-wave", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	f := src.(*httpSource).f
	_, gen := f.payload()
	before := h.provs[pl.Provider].callCount("link")
	if f.reroute(context.Background(), pl.URL, gen, 1<<20) {
		t.Fatal("same destination claimed as recovery")
	}
	if got := h.provs[pl.Provider].callCount("link") - before; got != rerouteLinks {
		t.Fatalf("non-TorBox API calls=%d, want %d", got, rerouteLinks)
	}
}

func TestReroutePinnedDestinationDoesNotSuppressReplay(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	h.cacheEverywhere(testHash)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("sticky-wave"))
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := DecodePayload(raw)
	var calls atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": pl.URL})
	}))
	defer api.Close()
	h.svc.entryByID(pl.Provider).p = fixtureTorBox(api.URL)
	var tick atomic.Int64
	tick.Store(time.Now().UnixNano())
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log, Now: func() time.Time { return time.Unix(0, tick.Load()) }})
	defer e.Close()
	src, err := e.Open(context.Background(), "sticky-wave", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	f := src.(*httpSource).f
	_, gen := f.payload()
	if f.reroute(context.Background(), pl.URL, gen, 1<<20) {
		t.Fatal("same host claimed as recovery")
	}
	if got := calls.Load(); got != rerouteLinks {
		t.Fatalf("API calls=%d, want %d", got, rerouteLinks)
	}
	// Cooling down the API must not prevent a newly learned edge winning.
	live := newMediaServer(t, 4<<20)
	live.issue(pl.URL[strings.LastIndex(pl.URL, "/")+1:])
	e.recent.record(pl.Provider, urlHost(live.URL), e.now())
	// Advance only replay pacing, leaving the API cooldown in force.
	tick.Add(int64(rerouteSpacing + time.Millisecond))
	_, gen = f.payload()
	if !f.reroute(context.Background(), pl.URL, gen, 1<<20) {
		t.Fatal("no replay during API cooldown")
	}
	if got := calls.Load(); got != rerouteLinks {
		t.Fatalf("cooldown made %d API calls", got)
	}
}

func TestReplayFastFailuresDoNotStarveLaterCandidate(t *testing.T) {
	for _, name := range []string{"same signature", "new signature"} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, 4<<20, "torbox")
			h.cacheEverywhere(testHash)
			raw, err := h.svc.Acquire(context.Background(), torrentRelease("probe-rotation"))
			if err != nil {
				t.Fatal(err)
			}
			pl, _ := DecodePayload(raw)
			live := newMediaServer(t, 4<<20)
			live.issue(pl.URL[strings.LastIndex(pl.URL, "/")+1:])
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "API must remain in cooldown", http.StatusServiceUnavailable)
			}))
			defer api.Close()
			h.svc.entryByID(pl.Provider).p = fixtureTorBox(api.URL)
			var tick atomic.Int64
			tick.Store(time.Now().UnixNano())
			e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log, Now: func() time.Time { return time.Unix(0, tick.Load()) }})
			defer e.Close()
			src, err := e.Open(context.Background(), "probe-rotation", raw)
			if err != nil {
				t.Fatal(err)
			}
			defer src.Close()
			f := src.(*httpSource).f
			f.mu.Lock()
			f.rerouteAt = e.now().Add(time.Hour)
			f.mu.Unlock()
			// Allocate all listeners before closing them, ensuring three distinct
			// refused destinations precede the live fourth candidate.
			dead := make([]*httptest.Server, recentEdgeTries)
			for i := range dead {
				dead[i] = httptest.NewServer(http.NotFoundHandler())
			}
			for i, server := range dead {
				e.recent.record(pl.Provider, urlHost(server.URL), e.now().Add(-time.Duration(i)*time.Second))
				server.Close()
			}
			e.recent.record(pl.Provider, urlHost(live.URL), e.now().Add(-time.Minute))
			ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
			defer cancel()
			_, gen := f.payload()
			if f.reroute(ctx, pl.URL, gen, 1<<20) {
				t.Fatal("first wave exceeded its three-candidate allowance")
			}
			if live.requests.Load() != 0 {
				t.Fatal("first wave reached the fourth candidate")
			}
			if name == "new signature" {
				// The provider can refresh the signature while pinning the same host.
				// That is not a reason to forget which candidates already failed.
				fresh := live.issue("refreshed-signature")
				f.mu.Lock()
				f.pl.URL = withHost(fresh, urlHost(pl.URL))
				f.gen++
				gen = f.gen
				f.mu.Unlock()
			}
			tick.Add(int64(rerouteSpacing + time.Millisecond))
			if !f.reroute(ctx, pl.URL, gen, 1<<20) {
				t.Fatal("fast failures starved the live fourth candidate")
			}
			if calls.Load() != 0 {
				t.Fatal("candidate rotation bypassed API cooldown")
			}
			body, _, _, err := f.rangeGet(ctx, 1<<20, (1<<20)+4095, true)
			if err != nil {
				t.Fatal(err)
			}
			defer body.Close()
			buf := make([]byte, 4096)
			if _, err := io.ReadFull(body, buf); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf, live.data[1<<20:(1<<20)+4096]) {
				t.Fatal("wrong bytes after rotating to the live candidate")
			}
		})
	}
}

func TestReplayCandidateOrderStableAtEqualTimestamps(t *testing.T) {
	var edges recentEdges
	now := time.Now()
	for _, host := range []string{"edge-d", "edge-b", "edge-c", "edge-a"} {
		edges.record("account", host, now)
	}
	edges.record("account", "newest", now.Add(time.Second))
	// The cursor resumes after a host, so equal-time entries must have a
	// stable order rather than a fresh map-iteration order on each wave.
	for i := 0; i < 100; i++ {
		got := strings.Join(edges.freshest("account", "", nil, now), ",")
		if got != "newest,edge-a,edge-b,edge-c,edge-d" {
			t.Fatalf("candidate order changed: %s", got)
		}
	}
}
