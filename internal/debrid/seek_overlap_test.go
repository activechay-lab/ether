package debrid

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// ffmpeg may seek its MKV header/tail while the preceding HTTP range producer
// is still unwinding. Seeking may retire speculative read-ahead, but it must
// not cancel another still-live range request on the same source handle.
func TestOverlappingActiveSeeksKeepTheirRequestLifetime(t *testing.T) {
	r := newEngineRig(t, 64*testBlock, -1)
	cfg := r.svc.Config()
	cfg.ReadAheadChunks = -1
	r.svc.UpdateConfig(cfg)
	r.media.latency = 200 * time.Millisecond
	src, err := r.eng.Open(t.Context(), r.rel.ID, r.pl)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	rc := src.(readAtContexter)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	before := r.media.requests.Load()
	first := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		n, e := rc.ReadAtContext(ctx, buf, 16*testBlock)
		if e == nil && (n != len(buf) || !bytes.Equal(buf, r.media.data[16*testBlock:16*testBlock+4096])) {
			e = context.Canceled
		}
		first <- e
	}()
	for r.media.requests.Load() == before {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	buf := make([]byte, 4096)
	n, err := rc.ReadAtContext(ctx, buf, 48*testBlock)
	if err != nil || n != len(buf) || !bytes.Equal(buf, r.media.data[48*testBlock:48*testBlock+4096]) {
		t.Fatalf("second seek n=%d err=%v", n, err)
	}
	if err = <-first; err != nil {
		t.Fatalf("active first range cancelled by unrelated seek: %v", err)
	}
}
