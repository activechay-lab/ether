package perfharness

import (
	"context"
	"fmt"
	"sync"
)

// SyncBenchOperations counts completed detail API calls, independently of any
// later audit of whether successfully touched titles survived an upgrade.
// A rejected call is not a successful touch, even if other calls succeeded.
type SyncBenchOperations struct {
	Attempted int `json:"attempted"`
	Succeeded int `json:"succeeded"`
	Rejected  int `json:"rejected"`
}

func runSyncBenchOperations(ctx context.Context, count int, fn func(context.Context, int) error) (*SyncBenchOperations, error) {
	work := make(chan int)
	failures := make([]error, count)
	var wg sync.WaitGroup
	for range min(8, count) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range work {
				// Every index has one worker; read results only after Wait.
				failures[i] = fn(ctx, i)
			}
		}()
	}
	for i := range count {
		work <- i
	}
	close(work)
	wg.Wait()
	counts := &SyncBenchOperations{Attempted: count, Succeeded: count}
	var first error
	for i, err := range failures {
		if err != nil {
			counts.Rejected++
			counts.Succeeded--
			if first == nil {
				first = fmt.Errorf("operation %d: %w", i, err)
			}
		}
	}
	if counts.Rejected != 0 {
		return counts, fmt.Errorf("%d of %d operations rejected: %w", counts.Rejected, count, first)
	}
	return counts, nil
}
