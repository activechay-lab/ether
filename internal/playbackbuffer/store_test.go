package playbackbuffer

import (
	"bytes"
	"context"
	"io"
	"os"
	"sync"
	"testing"
)

func newStore(t *testing.T, n int64) *Store {
	t.Helper()
	s, e := New(Limits{Budget{n, 20, 2}, Budget{n, 10, 1}, 2})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.OpenSession("s", State{1, 4, -1}); e != nil {
		t.Fatal(e)
	}
	return s
}
func retain(t *testing.T, s *Store, k Key, b []byte) {
	t.Helper()
	if e := s.Retain(context.Background(), k, MPEGTS, int64(len(b)), bytes.NewReader(b)); e != nil {
		t.Fatal(e)
	}
}
func TestBoundsReservationsAndCleanup(t *testing.T) {
	s := newStore(t, 50)
	k := Key{"s", 1, 4, 0, Segment}
	r, w := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- s.Retain(context.Background(), k, MPEGTS, 10, r) }()
	// Synchronize on Read, which occurs after the entire reservation is charged.
	if _, e := w.Write([]byte{1}); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e := s.Retain(context.Background(), Key{"s", 1, 4, int64(i + 1), Segment}, MPEGTS, 1, bytes.NewReader([]byte{0}))
			if e == nil {
				t.Error("overbooked")
			}
		}(i)
	}
	wg.Wait()
	w.Close()
	if <-done == nil {
		t.Fatal("truncation accepted")
	}
	r.Close()
	v, _ := s.Snapshot("s")
	if v.Usage != (Budget{}) {
		t.Fatal(v.Usage)
	}
	for _, b := range [][]byte{{1}, {1, 2, 3}} {
		if s.Retain(context.Background(), k, MPEGTS, 2, bytes.NewReader(b)) == nil {
			t.Fatal("size mismatch accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if s.Retain(ctx, k, MPEGTS, 1, bytes.NewReader([]byte{1})) == nil {
		t.Fatal("canceled accepted")
	}
	v, _ = s.Snapshot("s")
	if v.Usage != (Budget{}) {
		t.Fatal(v.Usage)
	}
	// Init count is independent of segment count and failed reservations roll back.
	k.Kind = Init
	retain(t, s, k, []byte{1})
	k.Sequence++
	if s.Retain(context.Background(), k, MPEGTS, 1, bytes.NewReader([]byte{1})) == nil {
		t.Fatal("init limit")
	}
}
func TestPublicationLeasesAndCAS(t *testing.T) {
	s := newStore(t, 100)
	k := Key{"s", 1, 4, 0, Segment}
	b := []byte{1, 2, 3}
	retain(t, s, k, b)
	b[0] = 9
	if _, e := s.Acquire(k); e == nil {
		t.Fatal("staged visible")
	}
	state := State{1, 4, -1}
	if e := s.Publish("s", state, []Key{k}); e != nil {
		t.Fatal(e)
	}
	if s.Publish("s", state, []Key{k}) == nil {
		t.Fatal("stale publish")
	}
	if s.Retain(context.Background(), k, MPEGTS, 3, bytes.NewReader(b)) == nil {
		t.Fatal("key mutation")
	}
	l, e := s.Acquire(k)
	if e != nil {
		t.Fatal(e)
	}
	out := make([]byte, 3)
	l.ReadAt(out, 0)
	if out[0] != 1 {
		t.Fatal("aliased input")
	}
	out[0] = 8
	l.ReadAt(out, 0)
	if out[0] != 1 {
		t.Fatal("aliased output")
	}
	if s.CloseSession("s") == nil || s.Delete(k) == nil {
		t.Fatal("reader promise lost")
	}
	token, e := s.FreezeGeneration("s", State{1, 4, 0}, 2)
	if e != nil {
		t.Fatal(e)
	}
	next := Key{"s", 2, 4, 1, Segment}
	retain(t, s, next, []byte{1})
	if s.PublishTransition(token, []Key{next}) == nil {
		t.Fatal("raw bytes proved continuity")
	}
	if e = s.AbortTransition(token); e != nil {
		t.Fatal(e)
	}
	if s.PublishTransition(token, []Key{next}) == nil {
		t.Fatal("reused token")
	}
	l.Close()
	l.Close()
	if _, e = l.ReadAt(out, 0); e == nil {
		t.Fatal("closed lease readable")
	}
	if e = s.CloseSession("s"); e != nil {
		t.Fatal(e)
	}
}
func TestActualTSFactsAndTransition(t *testing.T) {
	b, e := os.ReadFile("testdata/cpu.ts")
	if e != nil {
		t.Fatal(e)
	}
	f := inspect(MPEGTS, Segment, b)
	if !f.Verified || len(f.Tracks) != 2 {
		t.Fatalf("facts: %+v", f)
	}
	for _, tr := range f.Tracks {
		if !tr.Independent || tr.FirstPTS <= 0 {
			t.Fatalf("track: %+v", tr)
		}
	}
	for _, bad := range [][]byte{b[:len(b)-1], append([]byte{0}, b[1:]...), bytes.Repeat([]byte{0}, 188)} {
		if inspect(MPEGTS, Segment, bad).Verified {
			t.Fatal("bad TS verified")
		}
	}
	if inspect(FMP4, Segment, b).Verified {
		t.Fatal("fMP4 verified")
	}
	s := newStore(t, int64(len(b))*20)
	old := Key{"s", 1, 4, 0, Segment}
	retain(t, s, old, b)
	if e = s.Publish("s", State{1, 4, -1}, []Key{old}); e != nil {
		t.Fatal(e)
	}
	v, _ := s.Snapshot("s")
	v.Entries[0].Facts.Tracks[0].FirstPTS = 0
	v2, _ := s.Snapshot("s")
	if v2.Entries[0].Facts.Tracks[0].FirstPTS == 0 {
		t.Fatal("snapshot aliases")
	}
	next := Key{"s", 2, 4, 1, Segment}
	shifted := append([]byte(nil), b...)
	shiftPTS(shifted, 90000)
	retain(t, s, next, shifted)
	token, e := s.FreezeGeneration("s", State{1, 4, 0}, 2)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.PublishTransition(token, []Key{next}); e != nil {
		t.Fatal(e)
	}
	l, e := s.Acquire(old)
	if e != nil {
		t.Fatal("old bytes disappeared", e)
	}
	l.Close()
	if s.PublishTransition(token, []Key{next}) == nil {
		t.Fatal("token replay")
	}
	// A backwards clock and 33-bit wrap cannot be verified across generations.
	a := f
	c := f
	c.Tracks = append([]TrackFacts(nil), f.Tracks...)
	c.Tracks[0].FirstDTS = 1
	if follows(a, c) {
		t.Fatal("clock regression")
	}
}
func shiftPTS(b []byte, delta int64) {
	for off := 0; off < len(b); off += 188 {
		p := b[off : off+188]
		if p[1]&0x40 == 0 {
			continue
		}
		o := 4
		if p[3]&0x20 != 0 {
			o += 1 + int(p[4])
		}
		if o+14 > 188 {
			continue
		}
		pes := p[o:]
		if !bytes.Equal(pes[:3], []byte{0, 0, 1}) {
			continue
		}
		for _, i := range []int{9, 14} {
			if i == 14 && pes[7]>>6 != 3 {
				continue
			}
			v, ok := timestamp(pes[i:], pes[i]>>4)
			if !ok {
				continue
			}
			v += delta
			prefix := pes[i] & 0xf0
			pes[i] = prefix | byte(v>>29)&14 | 1
			pes[i+1] = byte(v >> 22)
			pes[i+2] = byte(v>>14)&254 | 1
			pes[i+3] = byte(v >> 7)
			pes[i+4] = byte(v<<1) | 1
		}
	}
}
