package transcode

import (
	"sort"
	"time"
)

// FrameAt returns the latest frame at or before at, if any.
func (b *BIF) FrameAt(at time.Duration) ([]byte, bool) {
	i := sort.Search(len(b.times), func(i int) bool { return b.times[i] > at }) - 1
	if i < 0 {
		return nil, false
	}
	return b.Frame(i), true
}
