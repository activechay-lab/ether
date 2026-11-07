package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// liveFault times are relative to the provider's start, not the first request.
type liveFault struct {
	kind         string
	at, duration time.Duration
	segment      int64
}
type liveProvider struct {
	server      *httptest.Server
	started     time.Time
	segments    [][]byte
	fault       liveFault
	mu          sync.Mutex
	connections int
	accepted    int
}

// newLiveProvider generates one 24-second fixture once and loops its six
// independently decodable segments. All media and the listener are ephemeral.
func newLiveProvider(ctx context.Context, ff, dir string, fault liveFault) (*liveProvider, error) {
	pattern := filepath.Join(dir, "live-%02d.ts")
	if err := command(ctx, ff, "-hide_banner", "-loglevel", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=320x180:rate=24", "-f", "lavfi", "-i", "sine=sample_rate=48000", "-t", "24", "-c:v", "libx264", "-preset", "ultrafast", "-threads", "2", "-g", "96", "-sc_threshold", "0", "-c:a", "aac", "-f", "segment", "-segment_time", "4", "-segment_format", "mpegts", "-reset_timestamps", "0", pattern); err != nil {
		return nil, err
	}
	p := &liveProvider{started: time.Now(), fault: fault}
	for i := 0; i < 6; i++ {
		b, err := os.ReadFile(fmt.Sprintf(pattern, i))
		if err != nil {
			return nil, err
		}
		p.segments = append(p.segments, b)
	}
	p.server = httptest.NewServer(http.HandlerFunc(p.serve))
	return p, nil
}
func (p *liveProvider) Close() { p.server.Close() }
func (p *liveProvider) serve(w http.ResponseWriter, r *http.Request) {
	elapsed := time.Since(p.started)
	switch {
	case r.URL.Path == "/live.m3u8":
		seq := int64(elapsed / (4 * time.Second))
		if p.fault.kind == "stall" && elapsed >= p.fault.at && elapsed < p.fault.at+p.fault.duration {
			seq = int64(p.fault.at / (4 * time.Second))
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprintf(w, "#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:%d\n", seq)
		for i := seq; i < seq+6; i++ {
			fmt.Fprintf(w, "#EXTINF:4.000,\n/segment/%d.ts\n", i)
		}
	case strings.HasPrefix(r.URL.Path, "/segment/"):
		n, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/segment/"), ".ts"), 10, 64)
		if err != nil || n < 0 {
			http.NotFound(w, r)
			return
		}
		if p.fault.kind == "404" && n == p.fault.segment && elapsed >= p.fault.at {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		_, _ = w.Write(p.segments[n%int64(len(p.segments))])
	case r.URL.Path == "/live.ts":
		p.mu.Lock()
		if p.connections > 0 || (p.fault.kind == "403" && p.accepted > 0 && elapsed >= p.fault.at) {
			p.mu.Unlock()
			http.Error(w, "connection refused", http.StatusForbidden)
			return
		}
		p.connections++
		p.accepted++
		p.mu.Unlock()
		defer func() { p.mu.Lock(); p.connections--; p.mu.Unlock() }()
		w.Header().Set("Content-Type", "video/mp2t")
		for i := 0; ; i++ {
			b := p.segments[i%len(p.segments)]
			// Pace in small chunks so clients see real-time delivery rather than bursts.
			const chunks = 40
			for c := 0; c < chunks; c++ {
				if p.fault.kind == "eof" && time.Since(p.started) >= p.fault.at {
					return
				}
				if _, err := w.Write(b[len(b)*c/chunks : len(b)*(c+1)/chunks]); err != nil {
					return
				}
				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				}
				select {
				case <-r.Context().Done():
					return
				case <-time.After(100 * time.Millisecond):
				}
			}
		}
	default:
		http.NotFound(w, r)
	}
}
