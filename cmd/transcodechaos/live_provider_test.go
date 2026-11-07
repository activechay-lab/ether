package main

import (
	"context"
	"encoding/binary"
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

// Timestamp and PSI rewrites are bit-packed; a wrong mask silently corrupts
// every fixture, so pin them without needing ffmpeg.
func TestTSRewrites(t *testing.T) {
	b := []byte{0x21, 0, 1, 0, 1} // PTS '0010' prefix with marker bits
	for _, v := range []int64{0, 1, 126000, pts33 - 1, pts33 + 5, -90000} {
		writeTS33(b, v)
		want := ((v % pts33) + pts33) % pts33
		if got := readTS33(b); got != want || b[0]&0xf1 != 0x21 || b[2]&1 != 1 || b[4]&1 != 1 {
			t.Fatalf("PTS %d: read %d, bytes %x", v, got, b)
		}
	}
	sec := []byte{2, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0xe1, 0, 0xf0, 0}
	sec = binary.BigEndian.AppendUint32(sec, mpegCRC(sec))
	p := make([]byte, tsPacket)
	for i := range p {
		p[i] = 0xff
	}
	copy(p, []byte{0x47, 0x50, 0x00, 0x10, 0x00})
	copy(p[5:], sec)
	if n := setPMTVersion(p, 0x1000, 31); n != 1 {
		t.Fatalf("rewrote %d PMTs", n)
	}
	got, err := psiSection(p)
	if err != nil || got[5] != 0xff || mpegCRC(got) != 0 {
		t.Fatalf("PMT version/CRC: %x %v", got, err)
	}
}
