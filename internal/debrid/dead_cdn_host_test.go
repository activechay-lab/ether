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

// Issue #396: a TorBox CDN node died mid-stream and requestdl first handed
// back a link on the same dead node. The file must resume on a fresh link on
// a live node, same bytes, without a second provider.
func TestDeadCDNHostReroutesToFreshLinkOnLiveNode(t *testing.T) {
	h := newHarness(t, 4<<20, "torbox")
	live := newMediaServer(t, 4<<20)
	h.cacheEverywhere(testHash)
	cfg := h.svc.Config()
	cfg.ReadAheadChunks = -1
	h.svc.UpdateConfig(cfg)
	payload, err := h.svc.Acquire(context.Background(), torrentRelease("dead-node"))
	if err != nil {
		t.Fatal(err)
	}
	dead := h.media.URL + "/m/" + "stale"
	var links atomic.Int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := dead // TorBox keeps the dead node in rotation for a while
		if links.Add(1) > 1 {
			u = live.issue("fresh")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "data": u})
	}))
	defer api.Close()
	h.svc.entryByID("torbox-0").p = newTorBox(testKey, fastOpts(api.URL))
	e := NewEngine(h.svc, EngineOptions{BlockSize: testBlock, HedgeDelay: -1, Logger: h.svc.log})
	defer e.Close()
	source, err := e.Open(context.Background(), "dead-node", payload)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	buf := make([]byte, 4096)
	if _, err := source.(readAtContexter).ReadAtContext(context.Background(), buf, 0); err != nil {
		t.Fatal(err)
	}
	h.media.Close() // the node now refuses connections

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
			t.Fatalf("%s read after the node died: n=%d err=%v", read.name, n, err)
		}
	}
	if got := links.Load(); got != 2 {
		t.Fatalf("requestdl calls=%d, want 2 (dead node, then a live one)", got)
	}
}
