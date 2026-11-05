package livetv

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/transcode"
)

type broadcastRingEntry struct {
	ready chan struct{}
	ring  *frameRing
	err   error
	used  time.Time
	bytes int64
}
type broadcastRingCache struct {
	mu      sync.Mutex
	entries map[string]*broadcastRingEntry
	budget  int64
}

func (c *broadcastRingCache) load(ctx context.Context, runner transcode.ContinuousRunner, path string, f transcode.RawFormat) (*frameRing, error) {
	key := fmt.Sprintf("%s/%+v", path, f)
	c.mu.Lock()
	if c.entries == nil {
		c.entries = map[string]*broadcastRingEntry{}
	}
	if c.budget == 0 {
		c.budget = 2 << 30
	}
	if entry := c.entries[key]; entry != nil {
		entry.used = time.Now()
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-entry.ready:
			if entry.err != nil {
				return nil, entry.err
			}
			copy := *entry.ring
			copy.audioAt, copy.videoAt = 0, 0
			return &copy, nil
		}
	}
	entry := &broadcastRingEntry{ready: make(chan struct{}), used: time.Now()}
	c.entries[key] = entry
	budget := c.budget
	c.mu.Unlock()
	decoder, err := runner.StartDecoder(ctx, transcode.DecoderRequest{SessionID: "ring", InputURL: path, Format: f})
	var ring *frameRing
	if err == nil {
		ring, err = readFrameRing(ctx, decoder, f, budget)
	}
	c.mu.Lock()
	entry.ring, entry.err = ring, err
	if ring != nil {
		entry.bytes = int64(len(ring.pcm))
		for _, frame := range ring.frames {
			entry.bytes += int64(len(frame.Video))
		}
	}
	close(entry.ready)
	if err != nil {
		delete(c.entries, key)
	} else {
		for {
			var bytes int64
			var oldest string
			var at time.Time
			for k, e := range c.entries {
				bytes += e.bytes
				if k != key && e.ring != nil && (oldest == "" || e.used.Before(at)) {
					oldest, at = k, e.used
				}
			}
			if bytes <= c.budget || oldest == "" {
				break
			}
			delete(c.entries, oldest)
		}
	}
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	copy := *ring
	copy.audioAt, copy.videoAt = 0, 0
	return &copy, nil
}

type ringDecoder struct {
	ring   *frameRing
	format transcode.RawFormat
	tick   int64
	closed chan struct{}
	once   sync.Once
}

func (d *ringDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	select {
	case <-ctx.Done():
		return transcode.RawFrame{}, ctx.Err()
	case <-d.closed:
		return transcode.RawFrame{}, context.Canceled
	default:
	}
	frame := d.ring.next(tickSamples(d.format, d.tick), d.format.Channels)
	d.tick++
	return frame, nil
}
func (d *ringDecoder) Close() error { d.once.Do(func() { close(d.closed) }); return nil }
