package prefetch

import (
	"context"

	"time"

	"github.com/braedonsaunders/ether/internal/playback"
)

// waitIdle blocks until no job is queued or running, or ctx ends.
func (p *Prefetcher) waitIdle(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() {
		p.mu.Lock()
		p.idle.Broadcast()
		p.mu.Unlock()
	})
	defer stop()
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		if p.pending == 0 || p.closed {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		p.idle.Wait()
	}
}

// WarmWindow is the byte window kept warm for a resume point: behind bytes
// before the offset mapped from viewOffset (constant-bitrate mapping) and
// ahead bytes after it, clamped to the file.
func WarmWindow(viewOffset, duration time.Duration, size, behind, ahead int64) (start, length int64) {
	return WarmWindowFor(viewOffset, playback.TimeMap{Size: size, Duration: duration}, behind, ahead)
}
