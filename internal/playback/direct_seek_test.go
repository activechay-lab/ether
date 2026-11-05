package playback

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/debrid"
)

// Real range serving and debrid flights over a sparse, delayed CDN. In
// particular, assert complete bodies: 206 alone hides producer cancellation.
func TestDirectSeekCuesReplay(t *testing.T) {
	const size int64 = 1421207277
	var mu sync.Mutex
	var starts []int64
	entered := make(chan int64, 256)
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var a, b int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &a, &b); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		starts = append(starts, a)
		mu.Unlock()
		entered <- a
		select {
		case <-time.After(120 * time.Millisecond):
		case <-r.Context().Done():
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", a, b, size))
		w.Header().Set("Content-Length", fmt.Sprint(b-a+1))
		w.WriteHeader(206)
		buf := make([]byte, 64<<10)
		for off := a; off <= b; {
			n := min(int64(len(buf)), b-off+1)
			for i := int64(0); i < n; i++ {
				buf[i] = byte((off + i) % 251)
			}
			if _, err := w.Write(buf[:n]); err != nil {
				return
			}
			off += n
		}
	}))
	defer cdn.Close()
	svc := debrid.NewService(config.Debrid{ReadAheadChunks: -1}, debrid.Options{})
	eng := debrid.NewEngine(svc, debrid.EngineOptions{})
	defer eng.Close()
	payload := debrid.Payload{ReleaseID: "A", Provider: "torbox-0", Kind: "torbox", InfoHash: "0123456789012345678901234567890123456789", Path: "movie.mkv", Size: size, URL: cdn.URL, Expires: time.Now().Add(time.Hour)}
	src, err := eng.Open(t.Context(), "A", payload.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	h := newHarness(t, []*fakeRelease{mkv("A", size, 80*time.Minute)}, []string{"A"})
	ps, err := h.m.Prepare(t.Context(), friend, movieKey, defaultCaps("android"))
	if err != nil {
		t.Fatal(err)
	}
	g := h.m.lookup(ps.ID).current()
	<-g.indexReady
	g.src = src
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { h.m.ServeRange(w, r, ps.ID) }))
	defer srv.Close()
	client := &http.Client{Timeout: 10 * time.Second}
	var validator string
	read := func(ctx context.Context, a, b int64, open bool) error {
		req, _ := http.NewRequestWithContext(ctx, "GET", srv.URL, nil)
		rg := fmt.Sprintf("bytes=%d-%d", a, b)
		if open {
			rg = fmt.Sprintf("bytes=%d-", a)
			b = size - 1
		}
		req.Header.Set("Range", rg)
		resp, e := client.Do(req)
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 206 || resp.ContentLength != b-a+1 || resp.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", a, b, size) || resp.Header.Get("Accept-Ranges") != "bytes" {
			return fmt.Errorf("bad range response: %d %v", resp.StatusCode, resp.Header)
		}
		mu.Lock()
		v := resp.Header.Get("ETag") + resp.Header.Get("Last-Modified")
		if validator == "" {
			validator = v
		}
		same := v == validator
		mu.Unlock()
		if !same {
			return fmt.Errorf("validators changed")
		}
		buf := make([]byte, 64<<10)
		off := a
		for {
			n, e := resp.Body.Read(buf)
			for i := 0; i < n; i++ {
				if buf[i] != byte((off+int64(i))%251) {
					return fmt.Errorf("changed byte at %d", off+int64(i))
				}
			}
			off += int64(n)
			if e == io.EOF {
				break
			}
			if e != nil {
				return e
			}
		}
		if off != b+1 {
			return fmt.Errorf("short range: got %d want %d", off-a, b-a+1)
		}
		return nil
	}
	// A live index read must survive an unrelated mid-file seek.
	done := make(chan error, 2)
	go func() { done <- read(t.Context(), 1401032694, 0, true) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("CDN not entered")
	}
	go func() { done <- read(t.Context(), 1040000000, 1040004095, false) }()
	for i := 0; i < 2; i++ {
		if e := <-done; e != nil {
			t.Fatal(e)
		}
	}
	if e := read(t.Context(), 0, 4095, false); e != nil {
		t.Fatal(e)
	}
	// Churn more than the streaming LRU, then replay the reporter's Cues
	// ranges. Completed edge blocks must survive all the intervening seeks.
	for i := int64(0); i < 28; i++ {
		if e := read(t.Context(), 100000000+i*(512<<10), 100004095+i*(512<<10), false); e != nil {
			t.Fatal(e)
		}
	}
	if e := read(t.Context(), 1400846286, 0, true); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	before := len(starts)
	mu.Unlock()
	began := time.Now()
	for i := 0; i < 3; i++ {
		if e := read(t.Context(), 1401032694, 0, true); e != nil {
			t.Fatal(e)
		}
	}
	if e := read(t.Context(), 0, 4095, false); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	after := len(starts)
	mu.Unlock()
	if after != before {
		t.Fatalf("cached edges fetched again: %d -> %d", before, after)
	}
	t.Logf("three cached 20 MB Cues reads + head: %s, upstream requests: 0", time.Since(began))
	// Cancel one flight and immediately retry the same block under a live context.
	ctx, cancel := context.WithCancel(t.Context())
	go func() { done <- read(ctx, 900000000, 900004095, false) }()
	time.AfterFunc(20*time.Millisecond, cancel)
	<-done
	if e := read(t.Context(), 900000000, 900004095, false); e != nil {
		t.Fatal(e)
	}
	if h.m.lookup(ps.ID).current() != g {
		t.Fatal("seek changed release")
	}
}
