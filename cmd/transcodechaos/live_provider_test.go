package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// The looping provider must be continuous across loop points: every PID's
// continuity counter steps by one and each stream's timestamps keep their
// cadence, or the "healthy" control itself would be a fault.
func TestLiveProviderLoopContinuity(t *testing.T) {
	ff := os.Getenv("CHAOS_FFMPEG")
	if ff == "" {
		t.Skip("set CHAOS_FFMPEG to generate live fixtures")
	}
	ctx := context.Background()
	fixtures := map[string]*liveFixture{}
	for _, name := range []string{"base", "ac3"} {
		f, err := loadLiveFixture(ctx, ff, t.TempDir(), liveVariants[name])
		if err != nil {
			t.Fatal(err)
		}
		fixtures[name] = f
	}
	p, err := newLiveProvider(providerConfig{Name: "p", Faults: []liveFault{{Kind: faultCodecAC3, At: 120 * time.Second}}}, fixtures, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	loop := fixtures["base"].LoopSegs
	var stream []byte
	for n := int64(0); n < int64(loop*2+2); n++ {
		stream = append(stream, p.segment(n)...)
	}
	cc := map[uint16]int{}
	last := map[uint16]int64{}
	step := map[uint16]int64{}
	for i := 0; i < len(stream); i += tsPacket {
		pk := stream[i : i+tsPacket]
		pid := tsPID(pk)
		if tsHasPayload(pk) {
			want, seen := cc[pid]
			got := int(pk[3] & 15)
			if seen && got != (want+1)&15 {
				t.Fatalf("PID %d continuity %d -> %d at packet %d", pid, want, got, i/tsPacket)
			}
			cc[pid] = got
		}
		pts, ok := packetPTS(pk)
		if !ok {
			continue
		}
		if prev, seen := last[pid]; seen {
			d := pts - prev
			if pid == fixtures["base"].VideoPID {
				// Decode order differs from presentation order with B-frames;
				// check only that nothing jumps by more than a GOP.
				if d < -90000 || d > 90000 {
					t.Fatalf("video PTS jumped %d ticks at packet %d", d, i/tsPacket)
				}
			} else {
				if s, ok := step[pid]; ok && d != s {
					t.Fatalf("PID %d PTS step %d, want %d at packet %d", pid, d, s, i/tsPacket)
				}
				step[pid] = d
			}
		}
		last[pid] = pts
	}
	if len(step) == 0 {
		t.Fatal("no audio timestamps checked")
	}
}
