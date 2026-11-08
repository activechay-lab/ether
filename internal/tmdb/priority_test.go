package tmdb

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Background traffic saturating the budget never delays a viewer-facing
// request by more than a token's worth, and still gets the budget the
// viewer leaves.
func TestBackgroundYieldsToViewerRequests(t *testing.T) {
	r := newRateLimiter(40, 40)
	ctx, stop := context.WithCancel(context.Background())
	bg := Background(ctx)
	var done atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r.wait(bg) == nil {
				done.Add(1)
			}
		}()
	}
	time.Sleep(1200 * time.Millisecond) // the burst is spent; background runs at the rate
	for i := range 5 {
		// A search page: 8 at once.
		t0 := time.Now()
		for range 8 {
			if err := r.wait(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if d := time.Since(t0); d > 30*time.Millisecond {
			t.Errorf("page %d waited %v behind background traffic", i, d)
		}
		time.Sleep(400 * time.Millisecond)
	}
	before := done.Load()
	time.Sleep(500 * time.Millisecond)
	stop()
	wg.Wait()
	if n := done.Load() - before; n < 10 {
		t.Errorf("background made %d requests in 500 ms with no viewer traffic, want about 20", n)
	}
}
