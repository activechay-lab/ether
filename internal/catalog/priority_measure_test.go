package catalog

// Viewer-facing TMDB latency while a sync runs, with and without the
// background lane (tmdb.Background). Not part of CI (about 30 s):
//
//	ETHER_PRIORITY_MEASURE=1 go test ./internal/catalog -run TestPriorityMeasure -v

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestPriorityMeasure(t *testing.T) {
	if os.Getenv("ETHER_PRIORITY_MEASURE") == "" {
		t.Skip("ETHER_PRIORITY_MEASURE not set")
	}
	const latency = 50 * time.Millisecond // recorded lean-fetch p50 from bb-worker-server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(latency)
		id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/movie/"), "/tv/")
		fmt.Fprintf(w, `{"id":%s,"title":"T%s","release_date":"2001-01-01","poster_path":"/p%s.jpg","popularity":3,"vote_count":20}`, id, id, id)
	}))
	defer srv.Close()
	pct := func(d []time.Duration, p int) time.Duration {
		sort.Slice(d, func(a, b int) bool { return d[a] < d[b] })
		return d[min(len(d)-1, len(d)*p/100)].Round(time.Millisecond)
	}
	for _, lane := range []bool{false, true} {
		db, err := store.OpenMemory(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		client := tmdb.New("k", slog.Default(), tmdb.WithBaseURL(srv.URL), tmdb.WithCacheTTL(0))
		c, err := New(db, client, config.Catalog{Language: "en-US", Region: "US"}, slog.Default())
		if err != nil {
			t.Fatal(err)
		}
		// The sync: 8 lean-fetch workers, as fillLoop runs them.
		ctx, stop := context.WithCancel(context.Background())
		load := ctx
		if lane {
			load = tmdb.Background(ctx)
		}
		var syncDone atomic.Int64
		var wg sync.WaitGroup
		workers := 8
		if os.Getenv("ETHER_PRIORITY_IDLE") != "" {
			workers = 0
		}
		for w := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for id := int64(1_000_000 + w*100_000); load.Err() == nil; id++ {
					if _, err := c.fetchSummary(load, false, id); err == nil {
						syncDone.Add(1)
					}
				}
			}()
		}
		time.Sleep(2 * time.Second) // the budget is saturated
		start, before := time.Now(), syncDone.Load()
		var opens, pages []time.Duration
		for i := range 20 {
			// A busy household: an open and a search page every 500 ms
			// (about 18 viewer-facing requests a second).
			time.Sleep(500*time.Millisecond - min(500*time.Millisecond, time.Since(start)-time.Duration(i)*500*time.Millisecond))
			// Open a title nobody stored: Get waits for the detail fetch
			// that promotes it.
			t0 := time.Now()
			if _, err := c.Get(context.Background(), core.MovieKey(int64(10+i))); err != nil {
				t.Fatal(err)
			}
			opens = append(opens, time.Since(t0))
			// A search page of 8 unknown posters, waited for in full.
			page := make([]core.Title, 8)
			for n := range page {
				page[n] = core.Title{Key: core.MovieKey(int64(5000 + i*8 + n)), Type: core.TypeMovie, Title: "x"}
			}
			t0 = time.Now()
			c.FillPosters(context.Background(), page, func(*core.Title) bool { return true }, 5*time.Second)
			pages = append(pages, time.Since(t0))
			for _, p := range page {
				if p.PosterPath == "" {
					t.Errorf("page not filled")
				}
			}
		}
		rate := float64(syncDone.Load()-before) / time.Since(start).Seconds()
		stop()
		wg.Wait()
		client.Close()
		db.Close()
		t.Logf("background lane %v: promotion on open p50 %v p95 %v; poster page of 8 p50 %v p95 %v; sync kept %.1f req/s meanwhile",
			lane, pct(opens, 50), pct(opens, 95), pct(pages, 50), pct(pages, 95), rate)
	}
}
