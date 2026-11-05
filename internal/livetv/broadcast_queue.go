package livetv

import (
	"context"
	"sync"

	"github.com/braedonsaunders/ether/internal/transcode"
)

// Encoder scheduling must not stop the output clock or discard PCM. Reserve
// six seconds of ticks, but retain at most one second of distinct pictures.
// Beyond that watermark, repeat a picture while preserving every audio tick.
// Additional backlog costs only PCM, rather than full uncompressed pictures.
type broadcastFrameQueue struct {
	mu        sync.Mutex
	frames    []transcode.RawFrame
	head      int
	fps       int
	repeating bool
	held      []byte
	ready     chan struct{}
}

func newBroadcastFrameQueue(fps int) *broadcastFrameQueue {
	return &broadcastFrameQueue{fps: fps, frames: make([]transcode.RawFrame, 0, fps*6), ready: make(chan struct{}, 1)}
}

func (q *broadcastFrameQueue) push(frame transcode.RawFrame) (changed, repeating bool, pending int) {
	q.mu.Lock()
	pending = len(q.frames) - q.head
	was := q.repeating
	if pending >= q.fps {
		q.repeating = true
	} else if pending <= q.fps/4 {
		q.repeating = false
	}
	if q.repeating && q.held != nil {
		frame.Video = q.held
		frame.RepeatVideo = true
	} else {
		q.held = frame.Video
	}
	q.frames = append(q.frames, frame)
	changed, repeating = was != q.repeating, q.repeating
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
	return
}

func (q *broadcastFrameQueue) pop(ctx context.Context) (transcode.RawFrame, bool) {
	for {
		if ctx.Err() != nil {
			return transcode.RawFrame{}, false
		}
		q.mu.Lock()
		if q.head < len(q.frames) {
			frame := q.frames[q.head]
			q.frames[q.head] = transcode.RawFrame{}
			q.head++
			if q.head == len(q.frames) {
				q.frames = q.frames[:0]
				q.head = 0
			} else if q.head >= q.fps*6 {
				n := copy(q.frames, q.frames[q.head:])
				clear(q.frames[n:])
				q.frames = q.frames[:n]
				q.head = 0
			}
			q.mu.Unlock()
			return frame, true
		}
		q.mu.Unlock()
		select {
		case <-ctx.Done():
			return transcode.RawFrame{}, false
		case <-q.ready:
		}
	}
}
