package livetv

import (
	"testing"
)

// tsGen writes a minimal program: PAT, PMT (H.264 video 0x100 carrying the
// PCR, AAC audio 0x101), one video PES per frame (keyframe every gop) and
// one audio PES per frame, each spanning two packets.
type tsGen struct {
	cc        map[int]byte
	dts       int64
	frame     int
	gop       int
	audioLead int64 // audio PTS relative to video DTS
	ac3       bool  // the PMT announces AC-3 audio (version 1)
}

func (g *tsGen) pkt(id int, pusi bool, adapt []byte, payload []byte) []byte {
	p := make([]byte, tsPacket)
	p[0] = 0x47
	p[1] = byte(id >> 8)
	if pusi {
		p[1] |= 0x40
	}
	p[2] = byte(id)
	cc := g.cc[id]
	g.cc[id] = (cc + 1) & 0x0f
	room := tsPacket - 4
	afc := byte(1)
	at := 4
	if adapt != nil || len(payload) < room {
		afc = 3
		alen := max(len(adapt), room-1-len(payload))
		p[4] = byte(alen)
		copy(p[5:], adapt)
		for i := 5 + len(adapt); i < 5+alen; i++ {
			p[i] = 0xff
		}
		if len(adapt) == 0 && alen > 0 {
			p[5] = 0
		}
		at = 5 + alen
	}
	p[3] = afc<<4 | cc
	copy(p[at:], payload)
	return p
}

func psiSection(table byte, body []byte) []byte {
	sec := append([]byte{table, 0xb0, byte(len(body) + 4)}, body...)
	sec = append(sec, 0, 0, 0, 0) // CRC is not checked by the splicer
	return append([]byte{0}, sec...)
}

func (g *tsGen) psi() []byte {
	pat := psiSection(0, []byte{0, 1, 0xc1, 0, 0, 0, 1, 0xf0, 0x00}) // program 1 -> PMT 0x1000
	version, audio := byte(0xc1), byte(0x0f)
	if g.ac3 {
		version, audio = 0xc3, 0x81
	}
	pmt := psiSection(2, []byte{0, 1, version, 0, 0, 0xe1, 0x00, 0xf0, 0x00,
		0x1b, 0xe1, 0x00, 0xf0, 0x00,
		audio, 0xe1, 0x01, 0xf0, 0x00})
	return append(g.pkt(0, true, nil, pat), g.pkt(0x1000, true, nil, pmt)...)
}

func pesHeader(sid byte, pts, dts int64) []byte {
	h := []byte{0, 0, 1, sid, 0, 0, 0x80, 0xc0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	h[9], h[14] = 0x31, 0x11
	writeTS(h[9:], pts)
	writeTS(h[14:], dts)
	return h
}

func pcrField(v int64) []byte {
	b := []byte{0x10, 0, 0, 0, 0, 0x7e, 0}
	writePCR(b[1:], v)
	return b
}

// frame writes one video frame (PAT/PMT first on keyframes) and one audio
// frame.
func (g *tsGen) next() []byte {
	key := g.frame%g.gop == 0
	var out []byte
	if key {
		out = append(out, g.psi()...)
	}
	adapt := pcrField(g.dts - 63000)
	if key {
		adapt[0] |= 0x40 // random access
	}
	nal := []byte{0, 0, 0, 1, 0x09, 0xf0, 0, 0, 0, 1, 0x41}
	if key {
		nal[10] = 0x65
	}
	out = append(out, g.pkt(0x100, true, adapt, append(pesHeader(0xe0, g.dts+3600, g.dts), nal...))...)
	out = append(out, g.pkt(0x100, false, nil, make([]byte, 184))...)
	out = append(out, g.pkt(0x101, true, nil, append(pesHeader(0xc0, g.dts+g.audioLead, g.dts+g.audioLead), 0xff, 0xf1))...)
	out = append(out, g.pkt(0x101, false, nil, make([]byte, 184))...)
	g.frame++
	g.dts += 3600
	return out
}

type tsSeen struct {
	pid          int
	pusi, key    bool
	cc           byte
	payload      bool
	pts, dts     int64
	pcr          int64
	hasTS, hasPC bool
}

func scanOut(t *testing.T, b []byte) []tsSeen {
	t.Helper()
	if len(b)%tsPacket != 0 {
		t.Fatalf("output is not packet aligned: %d bytes", len(b))
	}
	var out []tsSeen
	for i := 0; i < len(b); i += tsPacket {
		p := b[i : i+tsPacket]
		if p[0] != 0x47 {
			t.Fatalf("packet %d lost sync", i/tsPacket)
		}
		s := tsSeen{pid: pid(p), pusi: p[1]&0x40 != 0, cc: p[3] & 0x0f, payload: (p[3]>>4)&1 != 0}
		s.key = (p[3]>>4)&2 != 0 && p[4] > 0 && p[5]&0x40 != 0
		if at := pcrAt(p); at >= 0 {
			s.pcr, s.hasPC = readPCR(p[at:]), true
		}
		if s.pusi {
			if pts, dts := pesTimes(p, payloadAt(p)); pts >= 0 {
				s.pts, s.hasTS = readTS(p[pts:]), true
				s.dts = s.pts
				if dts >= 0 {
					s.dts = readTS(p[dts:])
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// A reconnect is spliced into one continuous stream: whole packets only,
// continuity counters unbroken on every PID, the new connection entering at
// a keyframe with PAT/PMT in front, video DTS exactly one frame after the
// last picture, audio never stepping back, and the PCR clock continuous.
func TestTSSpliceKeepsOneContinuousStream(t *testing.T) {
	sp := newTSSplicer()
	var out []byte
	var marks []tsMark
	feed := func(b []byte, chunk int) {
		for len(b) > 0 {
			n := min(chunk, len(b))
			var m []tsMark
			out, m = sp.feed(b[:n], out, int64(len(out)))
			marks = append(marks, m...)
			b = b[n:]
		}
	}
	a := &tsGen{cc: map[int]byte{}, dts: 900000, gop: 12, audioLead: -1800}
	var first []byte
	for range 30 {
		first = append(first, a.next()...)
	}
	// The first connection dies mid-packet.
	feed(append([]byte{0x00, 0x12}, first[:len(first)-100]...), 1000)
	before := len(out)
	lastVideo := int64(-1)
	for _, s := range scanOut(t, out) {
		if s.pid == 0x100 && s.hasTS {
			lastVideo = s.dts
		}
	}

	sp.reconnect(true)
	// The new connection is at a different clock, starts mid-GOP and its
	// counters restart.
	b := &tsGen{cc: map[int]byte{0x100: 7, 0x101: 3}, dts: 5_000_000, gop: 12, frame: 5, audioLead: -5400}
	var second []byte
	for range 40 {
		second = append(second, b.next()...)
	}
	feed(append([]byte{0x47, 0x01}, second...), 777)

	seen := scanOut(t, out)
	lastCC := map[int]byte{}
	lastDTS := map[int]int64{}
	var lastPCR int64 = -1
	spliced := false
	for i, s := range seen {
		if prev, ok := lastCC[s.pid]; ok && s.payload && s.cc != (prev+1)&0x0f {
			t.Fatalf("packet %d pid %#x: continuity %d after %d", i, s.pid, s.cc, prev)
		}
		lastCC[s.pid] = s.cc
		if s.hasPC {
			if lastPCR >= 0 && (s.pcr <= lastPCR || s.pcr-lastPCR > 2*3600) {
				t.Fatalf("packet %d: PCR %d after %d", i, s.pcr, lastPCR)
			}
			lastPCR = s.pcr
		}
		if s.hasTS {
			if prev, ok := lastDTS[s.pid]; ok && s.dts <= prev {
				t.Fatalf("packet %d pid %#x: timestamp %d after %d", i, s.pid, s.dts, prev)
			}
			lastDTS[s.pid] = s.dts
		}
		if i*tsPacket == before {
			if s.pid != 0 || seen[i+1].pid != 0x1000 || seen[i+2].pid != 0x100 || !seen[i+2].key {
				t.Fatalf("splice does not enter with PAT, PMT, keyframe: %+v %+v %+v", s, seen[i+1], seen[i+2])
			}
			if seen[i+2].dts != lastVideo+3600 {
				t.Fatalf("spliced keyframe DTS %d, want %d", seen[i+2].dts, lastVideo+3600)
			}
			spliced = true
		}
	}
	if !spliced {
		t.Fatal("no splice point found")
	}
	// The index has a keyframe entry at the splice and a clock that only
	// moved forward by playtime.
	var keyAtSplice bool
	for i, m := range marks {
		if m.key && m.entry == int64(before) {
			keyAtSplice = true
		}
		if i > 0 && m.clock < marks[i-1].clock {
			t.Fatalf("index clock went backwards at mark %d", i)
		}
	}
	if !keyAtSplice {
		t.Fatal("no keyframe entry at the splice")
	}
	// 30 + (40-7 skipped to the next keyframe) frames, less the torn one.
	if got := sp.clock; got < 50*3600 || got > 70*3600 {
		t.Fatalf("media clock %d does not match the frames played", got)
	}
}

// A provider that re-sends its recent buffer on reconnect (an initial
// burst) is the same stream: the splice continues right after the last
// whole frame, with no repeated content, no gap and no timestamp rewrite.
func TestTSSpliceContinuesAResentStream(t *testing.T) {
	gen := func() []byte {
		g := &tsGen{cc: map[int]byte{}, dts: 900000, gop: 12, audioLead: -1800}
		var b []byte
		for range 60 {
			b = append(b, g.next()...)
		}
		return b
	}
	stream := gen()
	perFrame := 4 * tsPacket
	sp := newTSSplicer()
	var out []byte
	// The first connection dies inside frame 30 (after its PSI-free frames
	// 24..29 and part of 30); the second re-sends from frame 20.
	cut := 24*perFrame + 2*2*tsPacket + 6*perFrame + perFrame/2
	out, _ = sp.feed(stream[:cut], out, 0)
	sp.reconnect(true)
	resend := 20*perFrame + 2*2*tsPacket
	out, _ = sp.feed(stream[resend:], out, int64(len(out)))
	var dts []int64
	psi := 0
	for _, s := range scanOut(t, out) {
		if s.pid == 0x100 && s.hasTS {
			dts = append(dts, s.dts)
		}
		if s.pid == 0 {
			psi++
		}
	}
	if len(dts) != 59 {
		t.Fatalf("delivered %d frames, want 59 (every frame once, the generator's last still uncommitted)", len(dts))
	}
	for i := 1; i < len(dts); i++ {
		if dts[i]-dts[i-1] != 3600 {
			t.Fatalf("frame %d: DTS step %d", i, dts[i]-dts[i-1])
		}
	}
	if psi != 5 {
		t.Fatalf("PAT count %d: a follow-on splice must not inject PSI", psi)
	}
}

// A program change (PMT) starts a new format generation, so a decoder
// reopened for a codec change can join at the new format's first keyframe.
func TestTSFormatGenerationFollowsPMT(t *testing.T) {
	g := &tsGen{cc: map[int]byte{}, dts: 900000, gop: 12}
	var b []byte
	for range 30 {
		b = append(b, g.next()...)
	}
	// The audio becomes AC-3 with a PMT version bump; the next PMT goes out
	// with the keyframe at frame 36.
	g.ac3 = true
	for range 30 {
		b = append(b, g.next()...)
	}
	sp := newTSSplicer()
	_, marks := sp.feed(b, nil, 0)
	var gens []int
	for _, m := range marks {
		if m.key {
			gens = append(gens, m.format)
		}
	}
	// Keyframes at 0, 12, 24 (old program), 36, 48 (new); the frame-60 one
	// is still uncommitted.
	if want := []int{0, 0, 0, 1, 1}; len(gens) != len(want) || gens[2] != 0 || gens[3] != 1 || gens[4] != 1 {
		t.Fatalf("format generation per keyframe %v, want %v", gens, want)
	}
}
