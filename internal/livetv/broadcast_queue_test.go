package livetv

import (
	"context"
	"encoding/binary"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/transcode"
)

func TestBroadcastFrameQueuePreservesPCMDuringDip(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		q := newBroadcastFrameQueue(30)
		origin := time.Now()
		done := make(chan struct{})
		repeated := 0
		go func() {
			defer close(done)
			var previous byte
			for tick := 0; tick < 300; tick++ {
				if tick == 60 {
					time.Sleep(4 * time.Second)
				}
				frame, ok := q.pop(context.Background())
				if !ok || int(binary.LittleEndian.Uint32(frame.PCM)) != tick {
					t.Errorf("audio lost or reordered at tick %d", tick)
					return
				}
				if tick > 0 && frame.Video[0] == previous {
					repeated++
				}
				previous = frame.Video[0]
			}
		}()
		for tick := 0; tick < 300; tick++ {
			pcm := make([]byte, 4)
			binary.LittleEndian.PutUint32(pcm, uint32(tick))
			q.push(transcode.RawFrame{Video: []byte{byte(tick)}, PCM: pcm})
			time.Sleep(time.Until(origin.Add(time.Duration(tick+1) * time.Second / 30)))
		}
		<-done
		if repeated == 0 {
			t.Fatal("slow encoder did not select repeated video")
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		if len(q.frames)-q.head != 0 || q.repeating {
			t.Fatal("encoder did not catch up after the dip")
		}
	})
}
