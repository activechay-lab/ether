//go:build tunnelstress

package adapters_test

import (
	"runtime"
	"sync"
	"testing"
)

// TestRemoteTunnelUnderContention is an opt-in load harness. Run with
// GOMAXPROCS=1 go test -race -tags=tunnelstress ./internal/plugins/adapters
// -run ^TestRemoteTunnelUnderContention$ -count=100.
func TestRemoteTunnelUnderContention(t *testing.T) {
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for j := 0; j < 100000; j++ {
				}
				runtime.Gosched()
			}
		}()
	}
	defer func() { close(stop); wg.Wait() }()
	TestRemoteFileRangePlaybackAndGrantRevocation(t)
}
