package stream

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/braedonsaunders/ether/internal/iso/isotest"
)

// bdavPacket builds one 192-byte source packet: a 4-byte TP_extra_header, then
// a TS packet whose adaptation field carries pcr and whose PES header carries
// pts and dts (both at the same 90 kHz base).
func bdavPacket(pts, pcr int64, fill byte) []byte {
	p := make([]byte, bdavPacketLen)
	ts := p[4:]
	ts[0], ts[1], ts[2], ts[3] = tsSync, 0x41, 0x00, 0x30 // PUSI, adaptation + payload
	ts[4] = 7                                             // adaptation field length
	ts[5] = 0x10                                          // PCR flag
	b := ts[6:12]
	b[0], b[1], b[2], b[3] = byte(pcr>>25), byte(pcr>>17), byte(pcr>>9), byte(pcr>>1)
	b[4] = byte((pcr&1)<<7) | 0x7E
	pes := ts[12:]
	pes[0], pes[1], pes[2], pes[3] = 0x00, 0x00, 0x01, 0xE0
	pes[6], pes[7], pes[8] = 0x80, 0xC0, 10 // PTS and DTS
	writeTS(pes[9:14], pts)
	pes[9] |= 0x31
	writeTS(pes[14:19], pts)
	pes[14] |= 0x11
	for i := 19; i < len(pes); i++ {
		pes[i] = fill
	}
	return p
}

func clipBytes(base int64, packets int, fill byte) []byte {
	var out []byte
	for i := 0; i < packets; i++ {
		out = append(out, bdavPacket(base+int64(i)*3000, base+int64(i)*3000-900, fill)...)
	}
	return out
}

func firstPTS(b []byte) int64 { return readTS(b[4+12+9 : 4+12+14]) }

func TestRemuxContinuousTimeline(t *testing.T) {
	// Clip 1: authored 10 s from 0. Clip 2: 4 s from 2 s into its own timeline.
	c1, c2 := clipBytes(0, 50, 0xA1), clipBytes(180000, 30, 0xB2)
	spans := clipSpans([]clipBoundary{
		{byteLen: int64(len(c1)), delta90k: 0},
		{byteLen: int64(len(c2)), delta90k: 900000 - 180000},
	})
	raw := append(append([]byte{}, c1...), c2...)
	read := func(dst []byte, from int64) (int, error) { return copy(dst, raw[from:]), nil }

	// The whole file, sequentially.
	full := append([]byte{}, raw...)
	if err := remux(spans, full, 0, read); err != nil {
		t.Fatal(err)
	}
	if len(full) != len(raw) {
		t.Fatal("length changed")
	}
	if got := firstPTS(full[len(c1):]); got != 900000 {
		t.Fatalf("clip 2 starts at PTS %d, want 900000 (right after clip 1's 10 s)", got)
	}
	if got := firstPTS(full); got != 0 {
		t.Fatalf("clip 1 PTS moved to %d", got)
	}
	if bytes.Equal(full[len(c1):], c2) {
		t.Fatal("clip 2 was not rewritten")
	}
	// Payload bytes (everything but the timestamps) are untouched.
	if full[len(c1)+4+12+30] != 0xB2 {
		t.Fatal("payload changed")
	}

	// Any window, aligned or not, equals the same bytes of the full rewrite.
	r := rand.New(rand.NewPCG(3, 4))
	for i := 0; i < 400; i++ {
		off := r.Int64N(int64(len(raw)))
		n := 1 + r.IntN(2500)
		end := min(int(off)+n, len(raw))
		got := append([]byte{}, raw[off:end]...)
		if err := remux(spans, got, off, read); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, full[off:end]) {
			t.Fatalf("window [%d,%d) differs from the full rewrite", off, end)
		}
	}
}

// A multi-clip Blu-ray main feature reads with a continuous timeline through
// the whole engine, not only through the transform.
func TestISOMainFeatureHasContinuousTimeline(t *testing.T) {
	c1, c2 := clipBytes(0, 400, 0xA1), clipBytes(180000, 300, 0xB2)
	image := isotest.Build("DISC", []isotest.File{
		{Path: "BDMV/PLAYLIST/00000.mpls", Data: isotest.MPLS([]isotest.PlayItem{{Clip: "00001", In: 0, Out: 450000}, {Clip: "00002", In: 90000, Out: 270000}})},
		{Path: "BDMV/STREAM/00001.m2ts", Data: c1},
		{Path: "BDMV/STREAM/00002.m2ts", Data: c2},
	})
	f := newFakeFetcher()
	doc := post(t, f, 60_000, postFile{name: "Movie.iso", data: image})
	e := newTestEngine(t, f, nil, Options{})
	src := openSource(t, e, "iso", doc)
	got := readAll(t, src, 64<<10)
	if len(got) != len(c1)+len(c2) {
		t.Fatalf("size %d", len(got))
	}
	if p := firstPTS(got[len(c1):]); p != 900000 {
		t.Fatalf("clip 2 starts at PTS %d, want 900000", p)
	}
	// Random reads agree with the sequential one.
	checkRandomReads(t, src, got, 5, 80)
}

func TestRemuxLeavesNonTransportStreamAlone(t *testing.T) {
	raw := randBytes(77, 10*bdavPacketLen)
	raw[4] = tsSync // one packet that happens to look like TS
	spans := clipSpans([]clipBoundary{{byteLen: int64(len(raw)), delta90k: 5000}})
	got := append([]byte{}, raw...)
	if err := remux(spans, got, 0, func(dst []byte, from int64) (int, error) { return copy(dst, raw[from:]), nil }); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("non-TS data was rewritten")
	}
}

// A read not aligned to the clip's packet grid re-reads only the two packets
// cut by its ends, not the whole window.
func TestRemuxUnalignedReadRereadsOnlyEdgePackets(t *testing.T) {
	raw := clipBytes(0, 40, 0xA1)
	spans := clipSpans([]clipBoundary{{byteLen: int64(len(raw)), delta90k: 90000}})
	var reread int
	read := func(dst []byte, from int64) (int, error) {
		reread += len(dst)
		return copy(dst, raw[from:]), nil
	}
	want := append([]byte{}, raw...)
	if err := remux(spans, want, 0, func(dst []byte, from int64) (int, error) { return copy(dst, raw[from:]), nil }); err != nil {
		t.Fatal(err)
	}

	off, n := int64(100), 20*bdavPacketLen+37
	got := append([]byte{}, raw[off:off+int64(n)]...)
	if err := remux(spans, got, off, read); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want[off:off+int64(n)]) {
		t.Fatal("unaligned read differs from the same bytes of a whole-file remux")
	}
	if reread > 2*bdavPacketLen {
		t.Fatalf("re-read %d bytes, want at most %d", reread, 2*bdavPacketLen)
	}
}

// One packet without a sync byte (a zero-filled damaged packet) must not stop
// the rest of the window from being rewritten.
func TestRemuxSkipsOnlyDamagedPackets(t *testing.T) {
	raw := clipBytes(0, 10, 0xA1)
	spans := clipSpans([]clipBoundary{{byteLen: int64(len(raw)), delta90k: 90000}})
	clear(raw[3*bdavPacketLen : 4*bdavPacketLen])
	buf := append([]byte{}, raw...)
	if err := remux(spans, buf, 0, func(dst []byte, from int64) (int, error) { return copy(dst, raw[from:]), nil }); err != nil {
		t.Fatal(err)
	}
	for i := range 10 {
		pkt := buf[i*bdavPacketLen : (i+1)*bdavPacketLen]
		if i == 3 {
			if !bytes.Equal(pkt, raw[i*bdavPacketLen:(i+1)*bdavPacketLen]) {
				t.Fatal("damaged packet was modified")
			}
			continue
		}
		if got, want := firstPTS(pkt), int64(i)*3000+90000; got != want {
			t.Fatalf("packet %d PTS %d, want %d", i, got, want)
		}
	}
}

func TestRemuxEdgeReadErrorIsReturned(t *testing.T) {
	raw := clipBytes(0, 10, 0xA1)
	spans := clipSpans([]clipBoundary{{byteLen: int64(len(raw)), delta90k: 90000}})
	buf := append([]byte{}, raw[100:1000]...)
	err := remux(spans, buf, 100, func([]byte, int64) (int, error) { return 0, errTestRead })
	if err != errTestRead {
		t.Fatalf("err = %v, want errTestRead", err)
	}
}

var errTestRead = errors.New("test read failure")
