package debrid

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// A transport failure must move the same torrent file to another provider,
// rather than accepting another signature on the unreachable endpoint.
func TestRefusedHostMovesToSameFileOnAnotherProvider(t *testing.T) {
	h := newHarness(t, 2<<20, "torbox", "realdebrid")
	other := newMediaServer(t, 2<<20)
	h.provs["realdebrid-1"].media = other
	h.cacheEverywhere(testHash)
	cfg := h.svc.Config()
	cfg.ReadAheadChunks = -1
	h.svc.UpdateConfig(cfg)
	payload, err := h.svc.Acquire(context.Background(), torrentRelease("seek"))
	if err != nil {
		t.Fatal(err)
	}
	var links atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/torrents/requestdl" {
			t.Errorf("unexpected API route %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		links.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": h.media.URL + "/same-host"})
	}))
	defer api.Close()
	h.svc.entryByID("torbox-0").p = newTorBox(testKey, fastOpts(api.URL))
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	source, err := e.Open(context.Background(), "seek", payload)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	rc := source.(readAtContexter)
	buf := make([]byte, 4096)
	if _, err = rc.ReadAtContext(context.Background(), buf, 0); err != nil {
		t.Fatal(err)
	}
	h.media.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	off := int64(1 << 20)
	n, err := rc.ReadAtContext(ctx, buf, off)
	if err != nil || n != len(buf) || !bytes.Equal(buf, other.data[off:off+int64(len(buf))]) {
		t.Fatalf("alternate read n=%d err=%v", n, err)
	}
	if links.Load() != rerouteLinks {
		t.Fatalf("TorBox regenerated %d links on the refused host, want one wave of %d", links.Load(), rerouteLinks)
	}
	p, _ := source.(*httpSource).f.payload()
	if p.Provider != "realdebrid-1" {
		t.Fatalf("provider did not move: %s", p.Provider)
	}
}

func TestRefusedHostAcceptsRegeneratedDifferentNode(t *testing.T) {
	h := newHarness(t, 2<<20, "torbox")
	other := newMediaServer(t, 2<<20)
	h.cacheEverywhere(testHash)
	cfg := h.svc.Config()
	cfg.ReadAheadChunks = -1
	h.svc.UpdateConfig(cfg)
	payload, err := h.svc.Acquire(context.Background(), torrentRelease("node"))
	if err != nil {
		t.Fatal(err)
	}
	var links atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		links.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": other.issue("replacement")})
	}))
	defer api.Close()
	h.svc.entryByID("torbox-0").p = newTorBox(testKey, fastOpts(api.URL))
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	source, err := e.Open(context.Background(), "node", payload)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	rc := source.(readAtContexter)
	buf := make([]byte, 4096)
	if _, err = rc.ReadAtContext(context.Background(), buf, 0); err != nil {
		t.Fatal(err)
	}
	h.media.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	off := int64(1 << 20)
	n, err := rc.ReadAtContext(ctx, buf, off)
	if err != nil || n != len(buf) || !bytes.Equal(buf, other.data[off:off+int64(len(buf))]) {
		t.Fatalf("node read n=%d err=%v", n, err)
	}
	if links.Load() != 1 {
		t.Fatalf("regenerated links=%d", links.Load())
	}
}
