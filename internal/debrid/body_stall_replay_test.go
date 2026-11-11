package debrid

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
