package transcode

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"io"
	"testing"
)

// One transport-to-AU invariant: independently packaged bytes must be recovered
// through original file spans despite split ADTS headers and split raw AUs,
// multiple AUs per PES, PES headers split across packets, and unrelated video.
// Rejection controls perturb that same transport, rather than testing helpers.
func TestRecoveryTSAACByteObservation(t *testing.T) {
	stamp := func(v int64) []byte {
		return []byte{0x21 | byte(v>>29)&14, byte(v >> 22), byte(v>>14)&0xfe | 1, byte(v >> 7), byte(v<<1) | 1}
	}
	section := func(b []byte) []byte {
		c := recoveryTSAACCRC(b)
		b = binary.BigEndian.AppendUint32(b, c)
		return append([]byte{0}, b...)
	}
	pat := section([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0})
	pmt := section([]byte{2, 0xb0, 23, 0, 1, 0xc1, 0, 0, 0xe1, 0, 0xf0, 0, 0x1b, 0xe1, 0, 0xf0, 0, 0x0f, 0xe1, 1, 0xf0, 0})
	var frames, raws [][]byte
	for i, n := range []int{401, 273, 611, 97, 333} {
		raw := make([]byte, n)
		for j := range raw {
			raw[j] = byte(j*17 + i*31)
		}
		size := n + 7
		h := []byte{255, 0xf1, 0x4c, 0x80 | byte(size>>11), byte(size >> 3), byte(size<<5) | 31, 0xfc}
		raws = append(raws, raw)
		frames = append(frames, append(h, raw...))
	}
	pes := func(pts int64, frames [][]byte) []byte {
		b := []byte{0, 0, 1, 0xc0, 0, 0, 0x80, 0x80, 5}
		b = append(b, stamp(pts)...)
		for _, f := range frames {
			b = append(b, f...)
		}
		binary.BigEndian.PutUint16(b[4:6], uint16(len(b)-6))
		return b
	}
	var fixture []byte
	cc := map[uint16]byte{}
	// Adaptation stuffing ensures these exact chunk lengths, with no PES stuffing.
	packet := func(pid uint16, start bool, payload []byte) {
		p := bytes.Repeat([]byte{255}, 188)
		p[0] = 0x47
		p[1] = byte(pid >> 8)
		if start {
			p[1] |= 64
		}
		p[2] = byte(pid)
		p[3] = 0x30 | cc[pid]
		cc[pid] = (cc[pid] + 1) & 15
		if len(payload) == 184 {
			p[3] = 0x10 | p[3]&15
			copy(p[4:], payload)
			fixture = append(fixture, p...)
			return
		}
		a := 183 - len(payload)
		p[4] = byte(a)
		if a > 0 {
			p[5] = 0
		}
		copy(p[5+a:], payload)
		fixture = append(fixture, p...)
	}
	packet(0, true, pat)
	packet(4096, true, pmt)
	// These video bytes deliberately carry no complete PES: video completeness is
	// outside this observation and cannot be conferred by AAC success.
	packet(256, true, []byte{0, 0, 1, 0xe0, 0, 0, 0x80, 0xc0, 10, 0x31, 0, 1, 0, 1, 0x11, 0, 1, 0, 1})
	first := len(fixture)
	for pi, b := range [][]byte{pes(90000, frames[:3]), pes(95760, frames[3:])} {
		chunks := []int{8, 7, 2, 184, 184, 40, 184, 184, 184, 184, 184, 184, 184}
		start := true
		for _, n := range chunks {
			if len(b) == 0 {
				break
			}
			n = min(n, len(b))
			packet(257, start, b[:n])
			b = b[n:]
			start = false
		}
		if len(b) != 0 {
			t.Fatalf("fixture PES %d unconsumed", pi)
		}
	}
	observe := func(b []byte) (*recoveryTSAACObservation, error) {
		return observeRecoveryTSAAC(bytes.NewReader(b), int64(len(b)))
	}
	original := append([]byte(nil), fixture...)
	o, e := observe(fixture)
	if e != nil {
		t.Fatal(e)
	}
	if len(o.units) != 5 || len(o.pes) != 2 || o.pid != 257 || o.program != 1 || o.declaredASC != [2]byte{0x11, 0x90} || o.sha256 != sha256.Sum256(original) {
		t.Fatal("identity/config/order")
	}
	// Ownership survives caller mutation and returned observation is never a receipt.
	clear(fixture)
	collect := func(ss []recoveryTSAACSpan) []byte {
		var b []byte
		last := -1
		for _, s := range ss {
			if s.offset < last || s.length <= 0 || s.offset < 0 || s.offset > len(o.owned)-s.length {
				t.Fatal("invalid original span")
			}
			b = append(b, o.owned[s.offset:s.offset+s.length]...)
			last = s.offset + s.length
		}
		return b
	}
	for i, u := range o.units {
		h, r := collect(u.header), collect(u.raw)
		f := append(h, r...)
		if !bytes.Equal(f, frames[i]) || !bytes.Equal(r, raws[i]) || sha256.Sum256(f) != u.frameSHA256 || sha256.Sum256(r) != u.rawSHA256 || u.declaredProfilePTS90k != 90000+int64(i)*1920 {
			t.Fatalf("unit %d exact bytes/hash/declared clock", i)
		}
		wantPES, wantOrder := 0, i
		if i >= 3 {
			wantPES, wantOrder = 1, i-3
		}
		if u.pes != wantPES || u.orderInPES != wantOrder || o.pes[u.pes].hasDTS {
			t.Fatal("PES identity/observed clock")
		}
	}
	if len(o.units[0].header) < 2 || len(o.units[0].raw) < 2 || o.pes[0].pts90k != 90000 || o.pes[1].pts90k != 95760 || o.pes[0].firstPacketOffset != first {
		t.Fatal("fixture must exercise splits and anchors")
	}
	// Mutations of unencoded raw audio are observable, not decoder-rejected: the
	// structural layer cannot assert semantic completeness from an ADTS header.
	changed := append([]byte(nil), original...)
	changed[o.units[0].raw[0].offset] ^= 1
	m, e := observe(changed)
	if e != nil || m.units[0].rawSHA256 == o.units[0].rawSHA256 {
		t.Fatal("raw mutation must change identity")
	}
	// Locate actual original bytes through observed spans, never nominal offsets.
	cases := []struct {
		name   string
		mutate func([]byte) []byte
	}{
		{"crc", func(b []byte) []byte { b[187] ^= 1; return b }},
		{"continuity", func(b []byte) []byte { b[first+188+3] ^= 1; return b }},
		{"discontinuity", func(b []byte) []byte { b[first+5] |= 0x80; return b }},
		{"config", func(b []byte) []byte { b[o.units[1].header[0].offset+2] ^= 4; return b }},
		{"crc ADTS", func(b []byte) []byte { b[o.units[1].header[0].offset+1] &= 0xfe; return b }},
		{"multiblock", func(b []byte) []byte { b[o.units[1].header[0].offset+6] |= 1; return b }},
		{"partial AU", func(b []byte) []byte { b[o.units[4].header[0].offset+4] = 255; return b }},
		{"partial PES", func(b []byte) []byte { return b[:len(b)-188] }},
		{"unaligned", func(b []byte) []byte { return b[:len(b)-1] }},
		{"anchor", func(b []byte) []byte {
			// PTS header spans two packets in the second PES. Change its final bit,
			// preserving marker bits and all byte lengths.
			off := o.pes[1].firstPacketOffset + 188 + 186
			b[off] ^= 2
			return b
		}},
		{"program change", func(b []byte) []byte {
			p := append([]byte(nil), b[:188]...)
			p[3] = p[3]&0xf0 | 1 // valid next PAT continuity
			p[181] = 2           // change program number, with a valid section CRC
			binary.BigEndian.PutUint32(p[184:], recoveryTSAACCRC(p[172:184]))
			return append(b, p...)
		}},
	}
	for _, tc := range cases {
		b := tc.mutate(append([]byte(nil), original...))
		if got, e := observe(b); e == nil || got != nil {
			t.Fatalf("accepted %s", tc.name)
		}
	}
	// Declared size/EOF and clock overflow controls belong to the same invariant.
	for _, size := range []int64{0, -188, recoveryTSAACByteLimit + 188, int64(len(original) - 188), int64(len(original) + 188)} {
		if got, e := observeRecoveryTSAAC(bytes.NewReader(original), size); e == nil || got != nil {
			t.Fatalf("accepted size %d", size)
		}
	}
	// Anchor near the 33-bit boundary cannot legitimize a wrapped later AU.
	overflow := append([]byte(nil), original...)
	// First PES's five PTS bytes are bytes 1..5 of its second tiny payload.
	copy(overflow[first+188+182:first+188+187], stamp(recoveryTSAACClockLimit-100))
	if _, e := observe(overflow); e == nil {
		t.Fatal("wrapped declared clock")
	}
	if got, e := observeRecoveryTSAAC(io.MultiReader(bytes.NewReader(original), bytes.NewReader([]byte{0})), int64(len(original))); e == nil || got != nil {
		t.Fatal("missing immediate EOF")
	}
}
