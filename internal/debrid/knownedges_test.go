package debrid

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestKnownEdgesSurviveRestartWithoutSuccessfulPlayback(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	h.cacheEverywhere(testHash)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("known-edge"))
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := DecodePayload(raw)
	live := newMediaServer(t, 4<<20)
	token := pl.URL[strings.LastIndex(pl.URL, "/")+1:]
	live.issue(token)
	// The provider issued this authority for another file, but no stream
	// ever played there. Discovery cannot require successful playback.
	witness := pl
	witness.Key = "candidate-only"
	witness.URL = live.issue("unplayed-witness")
	h.svc.savePayload(context.Background(), witness)
	restarted := NewService(h.svc.Config(), h.svc.opts)
	restarted.entryByID(pl.Provider).p = fixtureTorBox("http://api.invalid")
	e := NewEngine(restarted, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	src, err := e.Open(context.Background(), "known-edge", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	h.media.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	buf := make([]byte, 4096)
	n, err := src.(readAtContexter).ReadAtContext(ctx, buf, 1<<20)
	if err != nil || n != len(buf) || !bytes.Equal(buf, live.data[1<<20:(1<<20)+4096]) {
		t.Fatalf("restart recovery n=%d err=%v", n, err)
	}
	if got := e.recent.freshest(pl.Provider, "", nil, e.now()); len(got) == 0 {
		t.Fatal("read did not complete")
	}
	var host string
	if err := h.svc.db.QueryRow("SELECT host FROM debrid_cdn_edges WHERE provider_id=? AND host=?", pl.Provider, urlHost(live.URL)).Scan(&host); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(host, "/") || strings.Contains(host, token) {
		t.Fatal("persisted signed URL instead of host")
	}
}

func TestKnownEdgesBoundedExpiredAndAccountScoped(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	ctx := context.Background()
	for i := 0; i < knownEdgeLimit+8; i++ {
		h.svc.savePayload(ctx, Payload{Provider: "torbox-0", Kind: "torbox", URL: fmt.Sprintf("https://nexus-%03d.example.test/dld/a?token=secret", i)})
	}
	got := h.svc.knownCDNHosts(ctx, "torbox-0")
	if len(got) != knownEdgeLimit {
		t.Fatalf("candidates=%d", len(got))
	}
	if got := h.svc.knownCDNHosts(ctx, "another-account"); len(got) != 0 {
		t.Fatal("account leakage")
	}
	var n int
	if err := h.svc.db.QueryRow("SELECT COUNT(*) FROM debrid_cdn_edges WHERE provider_id=?", "torbox-0").Scan(&n); err != nil || n != knownEdgeLimit {
		t.Fatalf("stored=%d err=%v", n, err)
	}
	now := h.svc.now()
	h.svc.now = func() time.Time { return now.Add(knownEdgeTTL + time.Hour) }
	if got := h.svc.knownCDNHosts(ctx, "torbox-0"); len(got) != 0 {
		t.Fatal("expired candidates admitted")
	}
}

func TestReplayStalledCandidateDoesNotStarveNext(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	h.cacheEverywhere(testHash)
	raw, err := h.svc.Acquire(context.Background(), torrentRelease("probe-fairness"))
	if err != nil {
		t.Fatal(err)
	}
	pl, _ := DecodePayload(raw)
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer stalled.Close()
	live := newMediaServer(t, 4<<20)
	live.issue(pl.URL[strings.LastIndex(pl.URL, "/")+1:])
	h.svc.entryByID(pl.Provider).p = fixtureTorBox("http://api.invalid")
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	e.recent.record(pl.Provider, urlHost(stalled.URL), e.now())
	e.recent.record(pl.Provider, urlHost(live.URL), e.now().Add(-time.Minute))
	src, err := e.Open(context.Background(), "probe-fairness", raw)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	f := src.(*httpSource).f
	_, gen := f.payload()
	ctx, cancel := context.WithTimeout(context.Background(), edgeProbeBudget+2*time.Second)
	defer cancel()
	if !f.reroute(ctx, pl.URL, gen, 1<<20) {
		t.Fatal("stalled first candidate starved live second")
	}
	if p, _ := f.payload(); urlHost(p.URL) != urlHost(live.URL) {
		t.Fatal("wrong edge adopted")
	}
}
