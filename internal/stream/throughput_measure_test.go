package stream

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/segcache"
	"github.com/braedonsaunders/ether/internal/usenet"
	"github.com/braedonsaunders/ether/internal/usenet/nntptest"
)

// TestMeasureLatencyBoundStreamThroughput (#385) plays ~9.8 MB/s (78.4 Mbit/s)
// 4K remux titles from a fake NNTP provider whose connections are latency- and
// bandwidth-bound (~0.55 MB/s per connection with 512 KiB articles), matching
// the real-world distant Usenet endpoint in issue #385.
//
// It compares the fixed-8 baseline (before adaptive scaling, where 8 sockets *
// ~0.55 MB/s capped fetchBps around ~4.4 MB/s) against the adaptive read-ahead
// engine on the exact same fake NNTP fixture, and measures multi-stream sharing.
// It is a measurement, not a wall-clock gate, and runs only with ETHER_MEASURE_USENET=1.
func TestMeasureLatencyBoundStreamThroughput(t *testing.T) {
	if os.Getenv("ETHER_MEASURE_USENET") != "1" {
		t.Skip("set ETHER_MEASURE_USENET=1 to measure")
	}
	const (
		remuxBps = 9_800_000 // ~9.8 MB/s 4K remux from #385
		size     = 48 << 20
		article  = 512 << 10
		warmup   = 2 << 20
	)
	type streamSpec struct {
		mediaBps int64
		size     int64
	}
	type streamResult struct {
		readerBps   float64
		deliveryBps float64
		parallel    int
		perFetchBps float64
	}
	run := func(t *testing.T, conns int, fixedCap int, specs []streamSpec) ([]streamResult, nntptest.Stats) {
		srv := nntptest.Start(t, nntptest.Options{Latency: 150 * time.Millisecond, BytesPerSecond: 650_000})
		docs := make([][]byte, len(specs))
		for i, sp := range specs {
			post, err := srv.Post(map[string][]byte{fmt.Sprintf("title%d.mkv", i): mkvBytes(uint64(40+i), int(sp.size))}, article, nntptest.PostOptions{})
			if err != nil {
				t.Fatal(err)
			}
			docs[i] = post.NZB
		}
		prov := srv.Provider("p", conns)
		if fixedCap > 0 {
			// Emulate the pre-fix 8-connection per-stream ceiling by capping
			// the pool at fixedCap+1 (1 reserved live slot + fixedCap readahead).
			prov = srv.Provider("p", fixedCap+1)
		}
		fetcher := usenet.New([]config.UsenetProvider{prov}, usenet.Options{})
		t.Cleanup(func() { _ = fetcher.Close() })
		cache, err := segcache.Open(t.TempDir(), 4<<30, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cache.Close() })
		e := newTestEngine(t, fetcher, cache, Options{PlaybackReadAhead: 2 * time.Minute})
		results := make([]streamResult, len(specs))
		var wg sync.WaitGroup
		for i, sp := range specs {
			wg.Go(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				src, err := e.Open(ctx, fmt.Sprintf("r%d", i), docs[i])
				if err != nil {
					t.Error(err)
					return
				}
				defer src.Close()
				if d, ok := src.(core.PlaybackDurationSetter); ok {
					d.SetPlaybackDuration(time.Duration(float64(src.Size()) / float64(sp.mediaBps) * float64(time.Second)))
				}
				r := src.(core.ContextReaderAt)
				reporter := src.(core.SourceDeliveryReporter)
				buf := make([]byte, 1<<20)
				start := time.Now()
				var off int64
				var warmAt time.Time
				var warmOff int64
				var warmDelivery core.SourceDelivery
				// First byte read matches playback's 512 KiB firstReadAlign.
				firstN, err := r.ReadAtContext(ctx, buf[:512<<10], 0)
				off += int64(firstN)
				if err != nil && off < src.Size() {
					t.Error(err)
					return
				}
				for off < src.Size() {
					n, err := r.ReadAtContext(ctx, buf, off)
					off += int64(n)
					if err != nil && off < src.Size() {
						t.Error(err)
						return
					}
					if warmAt.IsZero() && off >= warmup {
						warmAt = time.Now()
						warmOff = off
						warmDelivery = reporter.SourceDelivery()
					}
				}
				took := time.Since(start)
				postSpan := time.Since(warmAt).Seconds()
				d := reporter.SourceDelivery()
				res := streamResult{
					readerBps:   float64(off-warmOff) / postSpan,
					deliveryBps: float64(d.Bytes-warmDelivery.Bytes) / postSpan,
					parallel:    d.Parallel,
					perFetchBps: d.PerFetchBps,
				}
				results[i] = res
				t.Logf("stream %d (target %.2f MB/s): %d MiB in %v; post-warmup reader=%.2f MB/s (%.2fx), network fetchBps=%.2f MB/s, parallel=%d, perFetch=%.2f MB/s",
					i, float64(sp.mediaBps)/1e6, off>>20, took.Round(time.Millisecond),
					res.readerBps/1e6, res.readerBps/float64(sp.mediaBps),
					res.deliveryBps/1e6, res.parallel, res.perFetchBps/1e6)
			})
		}
		wg.Wait()
		st := srv.Stats()
		t.Logf("server: max %d concurrent connections, %d articles", st.MaxActive, st.ArticlesSent)
		return results, st
	}

	t.Run("before fixed-8 cap on 4K remux", func(t *testing.T) {
		res, _ := run(t, 50, 8, []streamSpec{{mediaBps: remuxBps, size: 24 << 20}})
		t.Logf("BEFORE (fixed 8 readahead): post-warmup reader=%.2f MB/s, delivery=%.2f MB/s vs 9.80 MB/s media",
			res[0].readerBps/1e6, res[0].deliveryBps/1e6)
	})

	t.Run("after adaptive 50 connections on 4K remux", func(t *testing.T) {
		res, st := run(t, 50, 0, []streamSpec{{mediaBps: remuxBps, size: size}})
		t.Logf("AFTER (adaptive 50-conn pool): post-warmup reader=%.2f MB/s, delivery=%.2f MB/s vs 9.80 MB/s media (maxActive=%d)",
			res[0].readerBps/1e6, res[0].deliveryBps/1e6, st.MaxActive)
		if res[0].parallel <= 8 || st.MaxActive <= 9 {
			t.Fatalf("adaptive 4K remux did not scale above fixed-8 baseline: parallel=%d maxActive=%d", res[0].parallel, st.MaxActive)
		}
	})

	t.Run("4K remux plus 1080p stream on 50 connections", func(t *testing.T) {
		res, _ := run(t, 50, 0, []streamSpec{
			{mediaBps: remuxBps, size: size},
			{mediaBps: 1_800_000, size: 16 << 20},
		})
		if res[0].parallel <= res[1].parallel {
			t.Fatalf("water-filling did not give 4K stream (%d) more concurrency than 1080p stream (%d)", res[0].parallel, res[1].parallel)
		}
	})

	t.Run("two 4K remux streams on 50 connections", func(t *testing.T) {
		run(t, 50, 0, []streamSpec{
			{mediaBps: remuxBps, size: 32 << 20},
			{mediaBps: remuxBps, size: 32 << 20},
		})
	})
}
