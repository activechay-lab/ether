// Package hlsplay measures client-observed playback through Plex HTTP handlers.
package hlsplay

import "math"

type Stall struct {
	StartSeconds    float64
	DurationSeconds float64
}
type Sample struct{ ElapsedSeconds, BufferedSeconds, PlayedSeconds float64 }

// Buffer is driven by a monotonic clock, including time spent blocked in HTTP.
// Initial buffering is startup latency. After playback begins, every empty
// interval is a stall, including a terminal interval with no subsequent fetch.
type Buffer struct {
	Level, Played, last float64
	Started             bool
	Stalls              []Stall
	active              bool
}

func (b *Buffer) Advance(now float64) {
	delta := math.Max(0, now-b.last)
	if b.Started {
		used := math.Min(delta, b.Level)
		b.Level -= used
		b.Played += used
		if delta-used > 1e-9 {
			if !b.active {
				b.Stalls = append(b.Stalls, Stall{StartSeconds: b.last + used})
				b.active = true
			}
			b.Stalls[len(b.Stalls)-1].DurationSeconds += delta - used
		}
	}
	b.last = now
}
func (b *Buffer) Add(now, duration float64) { b.Advance(now); b.Level += duration; b.active = false }
