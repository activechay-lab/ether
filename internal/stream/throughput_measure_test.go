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

// TestMeasureLatencyBoundStreamThroughput (#385) plays 80 Mbit/s titles from
// a fake provider whose connections are latency- and bandwidth-bound (150 ms
// per reply, 0.5 MB/s per connection), the shape of a distant Usenet
// endpoint. A reader pulls each title as fast as the source delivers, like a
// remux to HLS, and the delivered rate is reported against real time.
//
// At 24 connections the raw ceiling is 12 MB/s before latency/encoding,
// below the two readers' aggregate 20 MB/s target. A target miss is not a
// regression. This short transfer does not establish sustained playback.
// It is a measurement, not a gate: it moves 40 MiB through loopback sockets
// and runs only with ETHER_MEASURE_USENET=1.
func TestMeasureLatencyBoundStreamThroughput(t *testing.T) {
	if os.Getenv("ETHER_MEASURE_USENET") != "1" {
		t.Skip("set ETHER_MEASURE_USENET=1 to measure")
	}
	const (
		mediaBps = 10_000_000 // 80 Mbit/s
		size     = 8 << 20
		article  = 64 << 10
		warmup   = 1 << 20
	)
	run := func(t *testing.T, conns, streams int) []float64 {
		srv := nntptest.Start(t, nntptest.Options{Latency: 150 * time.Millisecond, BytesPerSecond: 500_000})
		docs := make([][]byte, streams)
		for i := range docs {
			post, err := srv.Post(map[string][]byte{fmt.Sprintf("title%d.mkv", i): mkvBytes(uint64(40+i), size)}, article, nntptest.PostOptions{})
			if err != nil {
				t.Fatal(err)
			}
			docs[i] = post.NZB
		}
		fetcher := usenet.New([]config.UsenetProvider{srv.Provider("p", conns)}, usenet.Options{})
		t.Cleanup(func() { _ = fetcher.Close() })
		cache, err := segcache.Open(t.TempDir(), 4<<30, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cache.Close() })
		e := newTestEngine(t, fetcher, cache, Options{PlaybackReadAhead: 2 * time.Minute})
		rates := make([]float64, streams)
		var wg sync.WaitGroup
		for i := range streams {
			wg.Go(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				src, err := e.Open(ctx, fmt.Sprintf("r%d", i), docs[i])
				if err != nil {
					t.Error(err)
					return
				}
				defer src.Close()
				if d, ok := src.(core.PlaybackDurationSetter); ok {
					d.SetPlaybackDuration(time.Duration(float64(src.Size()) / mediaBps * float64(time.Second)))
				}
				r, ok := src.(core.ContextReaderAt)
				if !ok {
					t.Error("source cannot read with a context")
					return
				}
				buf := make([]byte, 1<<20)
				start := time.Now()
				var off int64
				var warmAt time.Time
				var warmOff int64
				var warmDelivery core.SourceDelivery
				reporter := src.(core.SourceDeliveryReporter)
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
				// The post-warmup rate excludes the initial bytes: connection
				// set-up and the first per-fetch measurement.
				rates[i] = float64(off-warmOff) / time.Since(warmAt).Seconds()
				diag := ""
				if dr, ok := src.(interface {
					SourceDelivery() core.SourceDelivery
				}); ok {
					d := dr.SourceDelivery()
					t.Logf("fresh decoded fetch completion delivery after warmup: %.2f MB/s over wall time; %d completions, busy=%v, outstanding=%d, pipeline ceiling=%d (not unique wire traffic)", float64(d.Bytes-warmDelivery.Bytes)/time.Since(warmAt).Seconds()/1e6, d.Fetches-warmDelivery.Fetches, d.Busy-warmDelivery.Busy, d.InFlight, d.PipelineDepth)
					diag = fmt.Sprintf(" parallel=%d perFetch=%.2fMB/s", d.Parallel, d.PerFetchBps/1e6)
				}
				t.Logf("stream %d: %d MiB in %v (%.2fx real time overall); post-warmup reader %.2f MB/s = %.2fx real time%s", i, off>>20, took.Round(time.Millisecond), float64(off)/took.Seconds()/mediaBps, rates[i]/1e6, rates[i]/mediaBps, diag)
			})
		}
		wg.Wait()
		st := srv.Stats()
		t.Logf("server: max %d concurrent connections, %d articles", st.MaxActive, st.ArticlesSent)
		return rates
	}
	t.Run("one stream, 100 connections", func(t *testing.T) { run(t, 100, 1) })
	t.Run("two streams, 100 connections", func(t *testing.T) { run(t, 100, 2) })
	t.Run("two streams, 24 connections", func(t *testing.T) { run(t, 24, 2) })
}
