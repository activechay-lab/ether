package livetv

// The reserve keeps a raw MPEG-TS input as packet-aligned bytes with a media
// clock beside them, so a reader can be held N seconds behind the live edge
// and a provider reconnect can be spliced in without the reader noticing.
//
// tsSplicer sits between the provider body and the tap's ring:
//
//   - It forwards whole 188-byte packets only, resyncing on garbage, so a
//     connection that dies mid-packet never leaves a torn packet in the ring.
//   - It indexes the stream: a continuous 90 kHz media clock from the PCR
//     (jumps and wraps contribute nothing), and every video random-access
//     point together with the PAT that precedes it, where a new reader can
//     start decoding at once.
//   - After a reconnect it discards the new connection up to its first video
//     keyframe, replays the PAT/PMT in front of it, and shifts every PCR, PTS
//     and DTS (and each PID's continuity counter) so the new connection
//     continues the old timeline exactly one frame after its last picture.
//     PES packets that would step backwards on their PID are dropped. To
//     every reader (Steady's decoder, a relay's ffmpeg) the reconnect is an
//     ordinary cut in one continuous stream: no EOF, no timestamp jump, no
//     continuity error.

const (
	tsClockHz    = 90000
	tsWrap       = int64(1) << 33
	tsNoPID      = -1
	tsMarkEvery  = tsClockHz / 10 // a time mark at most every 100 ms
	tsSeekLimit  = 24 << 20       // give up waiting for a keyframe after this much
	tsPCRMaxStep = 2 * tsClockHz  // a larger PCR step is a discontinuity, not playtime
)

// tsMark is one index entry. off is the absolute ring offset of a packet.
// clock is the continuous media time at that packet. A key mark is a video
// random-access point; entry is where a reader starting there should begin
// (the PAT before the keyframe, or the keyframe itself).
type tsMark struct {
	off, entry int64
	clock      int64
	key        bool
}

type tsSplicer struct {
	pend   []byte // carried partial packet
	synced bool

	// Current epoch's program description.
	pmtPID, pcrPID, videoPID int
	videoType                byte
	pesPIDs                  map[int]bool
	pat, pmt                 [][]byte // latest PAT/PMT section packets
	pmtOpen                  bool     // collecting a multi-packet PMT

	// Splice state.
	epoch     int
	seeking   bool // after a reconnect: waiting for PSI and a keyframe
	seekBytes int
	tsOff     int64         // added to PCR/PTS/DTS in this epoch (mod 2^33)
	ccOff     map[int]byte  // per-PID continuity adjustment in this epoch
	lastCC    map[int]byte  // last continuity counter written per PID
	guard     map[int]int64 // per-PID floor a PES must exceed after a splice (-1 done)
	dropPES   map[int]bool  // dropping the rest of a PES that failed the guard
	lastDTS   map[int]int64 // last written PES DTS (or PTS) per PID, output timeline
	frameDur  int64         // estimated video frame duration, 90 kHz

	// Index.
	clock     int64 // continuous media clock, 90 kHz
	lastPCR   int64 // last output PCR base, -1 none
	lastMark  int64 // clock of the last time mark
	marked    bool
	lastPAT   int64 // offset of the last PAT written, -1 none
	haveClock bool
}

func newTSSplicer() *tsSplicer {
	return &tsSplicer{pmtPID: tsNoPID, pcrPID: tsNoPID, videoPID: tsNoPID, pesPIDs: map[int]bool{},
		ccOff: map[int]byte{}, lastCC: map[int]byte{}, guard: map[int]int64{}, dropPES: map[int]bool{},
		lastDTS: map[int]int64{}, lastPCR: -1, lastPAT: -1, frameDur: 3600}
}

// looksTS reports whether b starts (after at most one packet of junk) with
// three packets' worth of sync bytes.
func looksTS(b []byte) bool {
	for i := 0; i < tsPacket && i+2*tsPacket < len(b); i++ {
		if b[i] == 0x47 && b[i+tsPacket] == 0x47 && b[i+2*tsPacket] == 0x47 {
			return true
		}
	}
	return false
}

// reconnect starts a new epoch: the next bytes come from a fresh provider
// connection and are spliced in at its first keyframe.
func (s *tsSplicer) reconnect() {
	s.pend = s.pend[:0]
	s.synced = false
	s.epoch++
	s.seeking = true
	s.seekBytes = 0
	s.pat, s.pmt, s.pmtOpen = nil, nil, false
	s.pmtPID, s.pcrPID, s.videoPID, s.videoType = tsNoPID, tsNoPID, tsNoPID, 0
	s.pesPIDs = map[int]bool{}
	s.ccOff = map[int]byte{}
	s.dropPES = map[int]bool{}
}

// feed consumes upstream bytes and appends what the ring should receive to
// out. at is the absolute ring offset where out's first new byte will land.
// It returns out and the index marks for the appended packets.
func (s *tsSplicer) feed(p []byte, out []byte, at int64) ([]byte, []tsMark) {
	var marks []tsMark
	base := len(out)
	emit := func(pkt []byte, key bool) {
		off := at + int64(len(out)-base)
		if pid(pkt) == 0 && pkt[1]&0x40 != 0 {
			s.lastPAT = off
		}
		out = append(out, pkt...)
		if s.haveClock && (key || !s.marked || s.clock-s.lastMark >= tsMarkEvery) {
			m := tsMark{off: off, entry: off, clock: s.clock, key: key}
			if key && s.lastPAT >= 0 && s.lastPAT <= off {
				m.entry = s.lastPAT
			}
			if !key {
				s.lastMark, s.marked = s.clock, true
			}
			marks = append(marks, m)
		}
	}
	data := p
	if len(s.pend) > 0 {
		need := tsPacket - len(s.pend)
		if len(p) < need {
			s.pend = append(s.pend, p...)
			return out, marks
		}
		s.pend = append(s.pend, p[:need]...)
		pkt := s.pend
		data = p[need:]
		if pkt[0] == 0x47 {
			s.packet(pkt, emit)
		} else {
			s.synced = false
		}
		s.pend = s.pend[:0]
	}
	for len(data) > 0 {
		if !s.synced || data[0] != 0x47 {
			i := syncAt(data)
			if i < 0 {
				// Keep a tail that may hold the start of the next sync.
				keep := min(len(data), 2*tsPacket)
				s.pend = append(s.pend[:0], data[len(data)-keep:]...)
				if keep >= 2*tsPacket {
					s.pend = s.pend[:0]
				}
				s.synced = false
				return out, marks
			}
			data = data[i:]
			s.synced = true
		}
		if len(data) < tsPacket {
			s.pend = append(s.pend[:0], data...)
			break
		}
		s.packet(data[:tsPacket], emit)
		data = data[tsPacket:]
	}
	return out, marks
}

// syncAt finds a sync byte confirmed by the next packet's, or by the end of
// the buffer when the next packet is not here yet.
func syncAt(b []byte) int {
	for i := range b {
		if b[i] != 0x47 {
			continue
		}
		if i+tsPacket < len(b) {
			if b[i+tsPacket] == 0x47 {
				return i
			}
			continue
		}
		return i
	}
	return -1
}

func pid(pkt []byte) int { return int(pkt[1]&0x1f)<<8 | int(pkt[2]) }

// payloadAt returns the payload start of a packet, or -1 when it has none.
func payloadAt(pkt []byte) int {
	afc := (pkt[3] >> 4) & 3
	at := 4
	if afc&2 != 0 {
		at = 5 + int(pkt[4])
	}
	if afc&1 == 0 || at >= tsPacket {
		return -1
	}
	return at
}

// packet handles one aligned packet. The packet is copied before rewriting.
func (s *tsSplicer) packet(in []byte, emit func([]byte, bool)) {
	pkt := make([]byte, tsPacket)
	copy(pkt, in)
	id := pid(pkt)
	pusi := pkt[1]&0x40 != 0
	pl := payloadAt(pkt)
	switch {
	case id == 0:
		if pusi && pl >= 0 {
			s.parsePAT(pkt[pl:])
		}
		if pusi {
			s.pat = [][]byte{pkt}
		}
	case id == s.pmtPID:
		if pusi {
			s.pmt = [][]byte{pkt}
			s.pmtOpen = true
		} else if s.pmtOpen && len(s.pmt) < 8 {
			s.pmt = append(s.pmt, pkt)
		}
		if pusi && pl >= 0 {
			s.parsePMT(pkt[pl:])
		}
	}
	key := id == s.videoPID && pusi && s.isKey(pkt, pl)
	if s.seeking {
		s.seekBytes += tsPacket
		ready := s.pat != nil && s.pmt != nil && s.videoPID != tsNoPID
		giveUp := s.seekBytes > tsSeekLimit && ready && pusi && s.pesPIDs[id]
		if !(ready && key) && !giveUp {
			return
		}
		if !s.splice(pkt, pl) {
			return
		}
		s.seeking = false
		for _, psi := range append(append([][]byte{}, s.pat...), s.pmt...) {
			cp := append([]byte(nil), psi...)
			s.continuity(cp)
			emit(cp, false)
		}
	}
	if s.epoch > 0 && s.tsOff != 0 {
		s.shift(pkt, pl)
	}
	if s.pesPIDs[id] && !s.monotonic(pkt, pl, id, pusi) {
		return
	}
	s.continuity(pkt)
	s.observe(pkt, pl, id, pusi)
	emit(pkt, key)
}

func (s *tsSplicer) parsePAT(b []byte) {
	if len(b) < 1 {
		return
	}
	ptr := int(b[0])
	b = b[1+min(ptr, len(b)-1):]
	if len(b) < 8 || b[0] != 0 {
		return
	}
	n := int(b[1]&0x0f)<<8 | int(b[2])
	end := min(3+n-4, len(b))
	for i := 8; i+4 <= end; i += 4 {
		prog := int(b[i])<<8 | int(b[i+1])
		if prog != 0 {
			s.pmtPID = int(b[i+2]&0x1f)<<8 | int(b[i+3])
			return
		}
	}
}

func (s *tsSplicer) parsePMT(b []byte) {
	if len(b) < 1 {
		return
	}
	ptr := int(b[0])
	b = b[1+min(ptr, len(b)-1):]
	if len(b) < 12 || b[0] != 2 {
		return
	}
	n := int(b[1]&0x0f)<<8 | int(b[2])
	end := min(3+n-4, len(b))
	s.pcrPID = int(b[8]&0x1f)<<8 | int(b[9])
	info := int(b[10]&0x0f)<<8 | int(b[11])
	video, vtype := tsNoPID, byte(0)
	pes := map[int]bool{}
	for i := 12 + info; i+5 <= end; {
		st := b[i]
		ep := int(b[i+1]&0x1f)<<8 | int(b[i+2])
		es := int(b[i+3]&0x0f)<<8 | int(b[i+4])
		pes[ep] = true
		if video == tsNoPID && tsVideoType(st) {
			video, vtype = ep, st
		}
		i += 5 + es
	}
	s.pesPIDs = pes
	s.videoPID, s.videoType = video, vtype
}

func tsVideoType(t byte) bool {
	switch t {
	case 0x01, 0x02, 0x10, 0x1b, 0x24, 0x42, 0xd1, 0xea:
		return true
	}
	return false
}

// isKey reports a video random-access point: the adaptation field's flag,
// or an IDR/IRAP/sequence header at the start of the PES payload.
func (s *tsSplicer) isKey(pkt []byte, pl int) bool {
	if (pkt[3]>>4)&2 != 0 && pkt[4] > 0 && pkt[5]&0x40 != 0 {
		return true
	}
	if pl < 0 || pl+9 > tsPacket || pkt[pl] != 0 || pkt[pl+1] != 0 || pkt[pl+2] != 1 {
		return false
	}
	es := pl + 9 + int(pkt[pl+8])
	for i := es; i+3 < tsPacket; i++ {
		if pkt[i] != 0 || pkt[i+1] != 0 || pkt[i+2] != 1 {
			continue
		}
		n := pkt[i+3]
		switch s.videoType {
		case 0x1b:
			if t := n & 0x1f; t == 5 || t == 7 {
				return true
			}
		case 0x24:
			if t := (n >> 1) & 0x3f; t >= 16 && t <= 21 || t >= 32 && t <= 34 {
				return true
			}
		case 0x01, 0x02:
			if n == 0xb3 {
				return true
			}
		}
	}
	return false
}

// pesTimes returns the PES header's PTS and DTS positions in pkt (-1 absent).
func pesTimes(pkt []byte, pl int) (pts, dts int) {
	pts, dts = -1, -1
	if pl < 0 || pl+9 > tsPacket || pkt[pl] != 0 || pkt[pl+1] != 0 || pkt[pl+2] != 1 {
		return
	}
	switch sid := pkt[pl+3]; {
	case sid == 0xbc, sid == 0xbe, sid == 0xbf, sid >= 0xf0 && sid <= 0xf2, sid == 0xf8, sid == 0xff:
		return
	}
	flags := pkt[pl+7] >> 6
	if flags&2 != 0 && pl+14 <= tsPacket {
		pts = pl + 9
	}
	if flags == 3 && pl+19 <= tsPacket {
		dts = pl + 14
	}
	return
}

func readTS(b []byte) int64 {
	return int64(b[0]>>1&7)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}

func writeTS(b []byte, v int64) {
	v &= tsWrap - 1
	b[0] = b[0]&0xf1 | byte(v>>29)&0x0e
	b[1] = byte(v >> 22)
	b[2] = b[2]&0x01 | byte(v>>14)&0xfe
	b[3] = byte(v >> 7)
	b[4] = b[4]&0x01 | byte(v<<1)&0xfe
}

// pcrAt returns the position of a packet's PCR, or -1.
func pcrAt(pkt []byte) int {
	if (pkt[3]>>4)&2 == 0 || pkt[4] < 7 || pkt[5]&0x10 == 0 {
		return -1
	}
	return 6
}

func readPCR(b []byte) int64 {
	return int64(b[0])<<25 | int64(b[1])<<17 | int64(b[2])<<9 | int64(b[3])<<1 | int64(b[4]>>7)
}

func writePCR(b []byte, v int64) {
	v &= tsWrap - 1
	b[0], b[1], b[2], b[3] = byte(v>>25), byte(v>>17), byte(v>>9), byte(v>>1)
	b[4] = b[4]&0x7f | byte(v<<7)&0x80
}

// splice sets the new epoch's offsets from its first keyframe packet so the
// keyframe lands one frame after the last picture of the previous epoch.
// It reports false when the packet carries no timestamp to anchor on.
func (s *tsSplicer) splice(pkt []byte, pl int) bool {
	pts, dts := pesTimes(pkt, pl)
	at := dts
	if at < 0 {
		at = pts
	}
	if at < 0 {
		return false
	}
	prev, ok := s.lastDTS[s.videoPID]
	if !ok {
		// Anchor on the old video PID even if the new connection renumbered it.
		for p, v := range s.lastDTS {
			if !ok || p == s.videoPID {
				prev, ok = v, true
			}
		}
	}
	if !ok {
		s.tsOff = 0
		return true
	}
	want := prev + s.frameDur
	s.tsOff = ((want-readTS(pkt[at:]))%tsWrap + tsWrap) % tsWrap
	s.guard = map[int]int64{}
	for p, v := range s.lastDTS {
		s.guard[p] = v
	}
	return true
}

// shift moves the packet's PCR, PTS and DTS by the epoch offset.
func (s *tsSplicer) shift(pkt []byte, pl int) {
	if at := pcrAt(pkt); at >= 0 {
		writePCR(pkt[at:], readPCR(pkt[at:])+s.tsOff)
	}
	if pkt[1]&0x40 == 0 || !s.pesPIDs[pid(pkt)] {
		return
	}
	pts, dts := pesTimes(pkt, pl)
	if pts >= 0 {
		writeTS(pkt[pts:], readTS(pkt[pts:])+s.tsOff)
	}
	if dts >= 0 {
		writeTS(pkt[dts:], readTS(pkt[dts:])+s.tsOff)
	}
}

// monotonic drops PES that would step backwards on their PID right after a
// splice (audio muxed ahead of the keyframe). It reports whether to keep pkt.
func (s *tsSplicer) monotonic(pkt []byte, pl, id int, pusi bool) bool {
	floor, guarded := s.guard[id]
	if !guarded {
		return true
	}
	if !pusi {
		return !s.dropPES[id]
	}
	pts, dts := pesTimes(pkt, pl)
	at := dts
	if at < 0 {
		at = pts
	}
	if at < 0 {
		return !s.dropPES[id]
	}
	v := readTS(pkt[at:])
	if d := (v - floor + tsWrap) % tsWrap; d == 0 || d > tsWrap/2 {
		s.dropPES[id] = true
		return false
	}
	delete(s.guard, id)
	delete(s.dropPES, id)
	return true
}

// continuity renumbers the packet's continuity counter so each PID stays
// continuous across epochs.
func (s *tsSplicer) continuity(pkt []byte) {
	id := pid(pkt)
	if id == 0x1fff {
		return
	}
	cc := pkt[3] & 0x0f
	hasPayload := (pkt[3]>>4)&1 != 0
	off, ok := s.ccOff[id]
	if !ok {
		last, seen := s.lastCC[id]
		if seen && s.epoch > 0 {
			next := last
			if hasPayload {
				next = (last + 1) & 0x0f
			}
			off = (next - cc) & 0x0f
		}
		s.ccOff[id] = off
	}
	cc = (cc + off) & 0x0f
	pkt[3] = pkt[3]&0xf0 | cc
	s.lastCC[id] = cc
}

// observe updates the clock and timestamp history from a written packet.
func (s *tsSplicer) observe(pkt []byte, pl, id int, pusi bool) {
	if id == s.pcrPID || s.pcrPID == tsNoPID {
		if at := pcrAt(pkt); at >= 0 {
			v := readPCR(pkt[at:])
			if s.lastPCR >= 0 {
				if d := (v - s.lastPCR + tsWrap) % tsWrap; d <= tsPCRMaxStep {
					s.clock += d
				}
			}
			s.lastPCR = v
			s.haveClock = true
		}
	}
	if !pusi || !s.pesPIDs[id] {
		return
	}
	pts, dts := pesTimes(pkt, pl)
	at := dts
	if at < 0 {
		at = pts
	}
	if at < 0 {
		return
	}
	v := readTS(pkt[at:])
	if prev, ok := s.lastDTS[id]; ok && id == s.videoPID {
		if d := (v - prev + tsWrap) % tsWrap; d >= 900 && d <= 9000 {
			s.frameDur = d
		}
	}
	s.lastDTS[id] = v
}
