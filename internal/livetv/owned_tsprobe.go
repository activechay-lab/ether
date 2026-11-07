package livetv

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"
	"time"
)

// The owned timeline republishes provider segments byte for byte. These
// helpers read just enough of an MPEG-TS segment (PAT, PMT, the first SPS and
// audio frame header per stream) to notice a format change the provider did
// not mark, and shift slate timestamps so consecutive slate segments play as
// one continuous clip. Nothing here decodes media.

// mediaFormat is what a player's decoder chain is configured from. Unknown
// fields (zero values) never count as a change.
type mediaFormat struct {
	video  string // "h264", "hevc", "mpeg2video", ...
	width  int
	height int
	audio  []audioFormat // ordered by PID
	known  bool          // a PMT was found
	// mislabeled: an audio payload is not the codec the PMT declares (a
	// copy remux keeps its first PMT across a source codec change).
	mislabeled bool
}

type audioFormat struct {
	pid      uint16
	codec    string
	rate     int // sample rate index or Hz; only compared for equality
	channels int // channel configuration
}

func (f mediaFormat) String() string {
	if !f.known {
		return "unknown"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "video=%s", f.video)
	if f.width > 0 {
		fmt.Fprintf(&b, " %dx%d", f.width, f.height)
	}
	for _, a := range f.audio {
		fmt.Fprintf(&b, " audio=%s", a.codec)
		if a.rate > 0 {
			fmt.Fprintf(&b, "/%d", a.rate)
		}
		if a.channels > 0 {
			fmt.Fprintf(&b, "/%dch", a.channels)
		}
	}
	return b.String()
}

// formatChanged reports a change a strict HLS player must see marked with
// EXT-X-DISCONTINUITY: the stream set, a codec, or audio parameters.
// Resolution changes inside one codec are compared too; they are cheap to
// mark and Apple's authoring rules ask for it.
func formatChanged(prev, cur mediaFormat) bool {
	if !prev.known || !cur.known {
		return false
	}
	if prev.video != cur.video {
		return true
	}
	if prev.width > 0 && cur.width > 0 && (prev.width != cur.width || prev.height != cur.height) {
		return true
	}
	if len(prev.audio) != len(cur.audio) {
		return true
	}
	for i := range prev.audio {
		a, b := prev.audio[i], cur.audio[i]
		if a.pid != b.pid || a.codec != b.codec {
			return true
		}
		if a.rate > 0 && b.rate > 0 && a.rate != b.rate {
			return true
		}
		if a.channels > 0 && b.channels > 0 && a.channels != b.channels {
			return true
		}
	}
	return false
}

// mergeFormat fills fields a segment did not reveal (no SPS, no audio frame
// start) from the previous format, so absence never reads as a change later.
func mergeFormat(prev, cur mediaFormat) mediaFormat {
	if !cur.known {
		return prev
	}
	if !prev.known || prev.video != cur.video {
		return cur
	}
	if cur.width == 0 {
		cur.width, cur.height = prev.width, prev.height
	}
	for i := range cur.audio {
		for _, p := range prev.audio {
			if p.pid == cur.audio[i].pid && p.codec == cur.audio[i].codec {
				if cur.audio[i].rate == 0 {
					cur.audio[i].rate = p.rate
				}
				if cur.audio[i].channels == 0 {
					cur.audio[i].channels = p.channels
				}
			}
		}
	}
	return cur
}

func isTS(b []byte) bool {
	return len(b) >= tsPacket && b[0] == 0x47 && (len(b) < 2*tsPacket || b[tsPacket] == 0x47)
}

type tsStream struct {
	pid   uint16
	kind  string // "video" | "audio" | ""
	codec string
	pes   []byte // first PES payload (bounded)
	open  bool
	done  bool
}

// tsVideoSpan measures a segment's media length from its video PTS: last
// minus first presentation time plus one frame (the smallest gap between
// pictures). ok is false without enough video timestamps.
func tsVideoSpan(b []byte) (time.Duration, bool) {
	if !isTS(b) {
		return 0, false
	}
	pmtPID, videoPID := -1, -1
	var pts []uint64
	for off := 0; off+tsPacket <= len(b); off += tsPacket {
		p := b[off : off+tsPacket]
		if p[0] != 0x47 || p[1]&0x40 == 0 {
			continue
		}
		pid := int(p[1]&0x1f)<<8 | int(p[2])
		payload := tsPayload(p)
		if payload == nil {
			continue
		}
		switch {
		case pid == 0 && pmtPID < 0:
			pmtPID = ownedParsePAT(payload)
		case pid == pmtPID && videoPID < 0:
			for spid, st := range ownedParsePMT(payload) {
				if st.kind == "video" {
					videoPID = int(spid)
				}
			}
		case pid == videoPID && len(payload) >= 14 && payload[0] == 0 && payload[1] == 0 && payload[2] == 1 && payload[7]&0x80 != 0:
			t := payload[9:14]
			pts = append(pts, uint64(t[0]>>1&7)<<30|uint64(t[1])<<22|uint64(t[2]>>1)<<15|uint64(t[3])<<7|uint64(t[4]>>1))
		}
	}
	if len(pts) < 2 {
		return 0, false
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i] < pts[j] })
	frame := uint64(0)
	for i := 1; i < len(pts); i++ {
		if d := pts[i] - pts[i-1]; d > 0 && (frame == 0 || d < frame) {
			frame = d
		}
	}
	span := pts[len(pts)-1] - pts[0] + frame
	if span > 90000*600 { // a 33-bit wrap or garbage, not a segment
		return 0, false
	}
	return time.Duration(span) * time.Second / 90000, true
}

// inspectTS reads the stream set and per-stream parameters of one segment.
func inspectTS(b []byte) mediaFormat {
	var f mediaFormat
	if !isTS(b) {
		return f
	}
	pmtPID := -1
	var streams map[uint16]*tsStream
	const pesLimit = 256 << 10
	for off := 0; off+tsPacket <= len(b); off += tsPacket {
		p := b[off : off+tsPacket]
		if p[0] != 0x47 {
			// Resynchronise on the next sync byte rather than giving up.
			next := off + 1
			for next+tsPacket <= len(b) && b[next] != 0x47 {
				next++
			}
			off = next - tsPacket
			continue
		}
		pid := uint16(p[1]&0x1f)<<8 | uint16(p[2])
		pusi := p[1]&0x40 != 0
		payload := tsPayload(p)
		if payload == nil {
			continue
		}
		switch {
		case pid == 0 && pusi && pmtPID < 0:
			pmtPID = ownedParsePAT(payload)
		case pmtPID >= 0 && int(pid) == pmtPID && pusi && streams == nil:
			streams = ownedParsePMT(payload)
			if streams != nil {
				f.known = true
			}
		default:
			st := streams[pid]
			if st == nil || st.done {
				continue
			}
			if pusi {
				if st.open && len(st.pes) > 0 {
					st.done = true
					continue
				}
				st.open = true
				if h := pesHeaderLen(payload); h > 0 && h <= len(payload) {
					payload = payload[h:]
				} else {
					st.done = true
					continue
				}
			}
			if st.open && len(st.pes) < pesLimit {
				st.pes = append(st.pes, payload...)
			}
		}
	}
	if !f.known {
		return f
	}
	pids := make([]int, 0, len(streams))
	for pid := range streams {
		pids = append(pids, int(pid))
	}
	sort.Ints(pids)
	for _, pid := range pids {
		st := streams[uint16(pid)]
		switch st.kind {
		case "video":
			if f.video != "" {
				continue
			}
			f.video = st.codec
			switch st.codec {
			case "h264":
				f.width, f.height = h264Size(st.pes)
			case "hevc":
				f.width, f.height = hevcSize(st.pes)
			}
		case "audio":
			a := audioFormat{pid: st.pid, codec: st.codec}
			if sniffed := sniffAudioCodec(st.pes); sniffed != "" && sniffed != a.codec {
				a.codec = sniffed
				f.mislabeled = true
			}
			switch a.codec {
			case "aac":
				a.rate, a.channels = adtsParams(st.pes)
			case "ac3", "eac3":
				a.rate, a.channels = ac3Params(st.pes)
			}
			f.audio = append(f.audio, a)
		}
	}
	return f
}

func tsPayload(p []byte) []byte {
	afc := (p[3] >> 4) & 3
	off := 4
	if afc&2 != 0 {
		off = 5 + int(p[4])
	}
	if afc&1 == 0 || off >= tsPacket {
		return nil
	}
	return p[off:]
}

func ownedParsePAT(payload []byte) int {
	sec := ownedPSISection(payload, 0)
	if sec == nil {
		return -1
	}
	for i := 8; i+4 <= len(sec)-4; i += 4 {
		program := binary.BigEndian.Uint16(sec[i:])
		if program != 0 {
			return int(binary.BigEndian.Uint16(sec[i+2:]) & 0x1fff)
		}
	}
	return -1
}

// ownedPSISection returns a complete PSI section of the given table that starts in
// this packet, without its CRC, or nil.
func ownedPSISection(payload []byte, table byte) []byte {
	if len(payload) < 1 {
		return nil
	}
	ptr := int(payload[0])
	if 1+ptr+3 > len(payload) {
		return nil
	}
	sec := payload[1+ptr:]
	if sec[0] != table {
		return nil
	}
	n := int(binary.BigEndian.Uint16(sec[1:]) & 0x0fff)
	if n < 9 || 3+n > len(sec) {
		return nil
	}
	return sec[:3+n]
}

func ownedParsePMT(payload []byte) map[uint16]*tsStream {
	sec := ownedPSISection(payload, 2)
	if sec == nil || len(sec) < 12 {
		return nil
	}
	infoLen := int(binary.BigEndian.Uint16(sec[10:]) & 0x0fff)
	i := 12 + infoLen
	end := len(sec) - 4
	out := map[uint16]*tsStream{}
	for i+5 <= end {
		typ := sec[i]
		pid := binary.BigEndian.Uint16(sec[i+1:]) & 0x1fff
		esLen := int(binary.BigEndian.Uint16(sec[i+3:]) & 0x0fff)
		if i+5+esLen > end {
			break
		}
		desc := sec[i+5 : i+5+esLen]
		kind, codec := tsStreamCodec(typ, desc)
		if kind != "" {
			out[pid] = &tsStream{pid: pid, kind: kind, codec: codec}
		}
		i += 5 + esLen
	}
	return out
}

func tsStreamCodec(typ byte, desc []byte) (string, string) {
	switch typ {
	case 0x01, 0x02:
		return "video", "mpeg2video"
	case 0x10:
		return "video", "mpeg4"
	case 0x1b:
		return "video", "h264"
	case 0x24:
		return "video", "hevc"
	case 0x03, 0x04:
		return "audio", "mp2"
	case 0x0f:
		return "audio", "aac"
	case 0x11:
		return "audio", "aac_latm"
	case 0x81:
		return "audio", "ac3"
	case 0x87:
		return "audio", "eac3"
	case 0x06:
		for i := 0; i+2 <= len(desc); {
			tag, n := desc[i], int(desc[i+1])
			if i+2+n > len(desc) {
				break
			}
			body := desc[i+2 : i+2+n]
			switch tag {
			case 0x6a:
				return "audio", "ac3"
			case 0x7a:
				return "audio", "eac3"
			case 0x7b:
				return "audio", "dts"
			case 0x05:
				switch string(body) {
				case "AC-3":
					return "audio", "ac3"
				case "EAC3":
					return "audio", "eac3"
				case "Opus":
					return "audio", "opus"
				case "HEVC":
					return "video", "hevc"
				}
			}
			i += 2 + n
		}
	}
	return "", ""
}

func pesHeaderLen(payload []byte) int {
	if len(payload) < 9 || payload[0] != 0 || payload[1] != 0 || payload[2] != 1 {
		return 0
	}
	return 9 + int(payload[8])
}

// nalUnits splits an Annex B byte stream.
func nalUnits(b []byte, fn func(nal []byte) bool) {
	start := -1
	for i := 0; i+3 <= len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && b[end-1] == 0 {
					end--
				}
				if !fn(b[start:end]) {
					return
				}
			}
			start = i + 3
			i += 2
		}
	}
	if start >= 0 && start < len(b) {
		fn(b[start:])
	}
}

func unescapeRBSP(nal []byte) []byte {
	out := make([]byte, 0, len(nal))
	zeros := 0
	for _, c := range nal {
		if zeros >= 2 && c == 3 {
			zeros = 0
			continue
		}
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
		out = append(out, c)
	}
	return out
}

type bitReader struct {
	b   []byte
	pos int
	bad bool
}

func (r *bitReader) bit() uint {
	if r.pos >= len(r.b)*8 {
		r.bad = true
		return 0
	}
	v := (r.b[r.pos/8] >> (7 - uint(r.pos%8))) & 1
	r.pos++
	return uint(v)
}

func (r *bitReader) bits(n int) uint {
	var v uint
	for i := 0; i < n; i++ {
		v = v<<1 | r.bit()
	}
	return v
}

func (r *bitReader) ue() uint {
	zeros := 0
	for r.bit() == 0 {
		zeros++
		if zeros > 31 || r.bad {
			r.bad = true
			return 0
		}
	}
	return (1<<zeros - 1) + r.bits(zeros)
}

func (r *bitReader) se() int {
	v := r.ue()
	if v&1 == 1 {
		return int(v+1) / 2
	}
	return -int(v / 2)
}

func h264Size(es []byte) (int, int) {
	w, h := 0, 0
	nalUnits(es, func(nal []byte) bool {
		if len(nal) < 4 || nal[0]&0x1f != 7 {
			return true
		}
		w, h = parseH264SPS(unescapeRBSP(nal[1:]))
		return false
	})
	return w, h
}

func parseH264SPS(b []byte) (int, int) {
	r := &bitReader{b: b}
	profile := r.bits(8)
	r.bits(16) // constraint flags, level
	r.ue()     // seq_parameter_set_id
	chroma := uint(1)
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chroma = r.ue()
		if chroma == 3 {
			r.bit()
		}
		r.ue() // bit_depth_luma
		r.ue() // bit_depth_chroma
		r.bit()
		if r.bit() == 1 { // seq_scaling_matrix_present
			n := 8
			if chroma == 3 {
				n = 12
			}
			for i := 0; i < n; i++ {
				if r.bit() == 1 {
					size := 16
					if i >= 6 {
						size = 64
					}
					last, next := 8, 8
					for j := 0; j < size; j++ {
						if next != 0 {
							next = (last + r.se() + 256) % 256
						}
						if next != 0 {
							last = next
						}
					}
				}
			}
		}
	}
	r.ue() // log2_max_frame_num
	switch r.ue() {
	case 0:
		r.ue()
	case 1:
		r.bit()
		r.se()
		r.se()
		n := r.ue()
		for i := uint(0); i < n && !r.bad; i++ {
			r.se()
		}
	}
	r.ue()  // max_num_ref_frames
	r.bit() // gaps
	wMbs := r.ue() + 1
	hMapUnits := r.ue() + 1
	frameMbsOnly := r.bit()
	if frameMbsOnly == 0 {
		r.bit()
	}
	r.bit() // direct_8x8
	w := int(wMbs) * 16
	h := int(2-frameMbsOnly) * int(hMapUnits) * 16
	if r.bit() == 1 { // frame_cropping
		l, rt, t, bt := r.ue(), r.ue(), r.ue(), r.ue()
		cx, cy := 2, 2*int(2-frameMbsOnly)
		if chroma == 0 || chroma == 3 {
			cx, cy = 1, int(2-frameMbsOnly)
		}
		w -= cx * int(l+rt)
		h -= cy * int(t+bt)
	}
	if r.bad || w <= 0 || h <= 0 || w > 16384 || h > 16384 {
		return 0, 0
	}
	return w, h
}

func hevcSize(es []byte) (int, int) {
	w, h := 0, 0
	nalUnits(es, func(nal []byte) bool {
		if len(nal) < 4 || (nal[0]>>1)&0x3f != 33 {
			return true
		}
		w, h = parseHEVCSPS(unescapeRBSP(nal[2:]))
		return false
	})
	return w, h
}

func parseHEVCSPS(b []byte) (int, int) {
	r := &bitReader{b: b}
	r.bits(4)
	maxSub := int(r.bits(3))
	r.bit()
	// profile_tier_level(1, maxSub)
	r.bits(8)  // profile space, tier, profile idc
	r.bits(32) // compatibility flags
	r.bits(48) // constraint flags
	r.bits(8)  // level
	prof := make([]bool, maxSub)
	lvl := make([]bool, maxSub)
	for i := 0; i < maxSub; i++ {
		prof[i] = r.bit() == 1
		lvl[i] = r.bit() == 1
	}
	if maxSub > 0 {
		for i := maxSub; i < 8; i++ {
			r.bits(2)
		}
	}
	for i := 0; i < maxSub; i++ {
		if prof[i] {
			r.bits(88)
		}
		if lvl[i] {
			r.bits(8)
		}
	}
	r.ue() // sps id
	chroma := r.ue()
	if chroma == 3 {
		r.bit()
	}
	w, h := int(r.ue()), int(r.ue())
	if r.bit() == 1 {
		l, rt, t, bt := r.ue(), r.ue(), r.ue(), r.ue()
		cx, cy := 1, 1
		if chroma == 1 {
			cx, cy = 2, 2
		} else if chroma == 2 {
			cx = 2
		}
		w -= cx * int(l+rt)
		h -= cy * int(t+bt)
	}
	if r.bad || w <= 0 || h <= 0 || w > 16384 || h > 16384 {
		return 0, 0
	}
	return w, h
}

// sniffAudioCodec identifies an audio payload from the frame header at its
// start, independent of what the PMT declares; "" when unrecognised.
func sniffAudioCodec(es []byte) string {
	if len(es) < 6 {
		return ""
	}
	switch {
	case es[0] == 0x0b && es[1] == 0x77:
		if es[5]>>3 > 10 {
			return "eac3"
		}
		return "ac3"
	case es[0] == 0xff && es[1]&0xf6 == 0xf0:
		return "aac"
	case es[0] == 0x56 && es[1]&0xe0 == 0xe0:
		return "aac_latm"
	case es[0] == 0xff && es[1]&0xe0 == 0xe0 && (es[1]>>1)&3 != 0:
		return "mp2"
	}
	return ""
}

var adtsRates = []int{96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350}

func adtsParams(es []byte) (int, int) {
	for i := 0; i+7 <= len(es); i++ {
		if es[i] != 0xff || es[i+1]&0xf6 != 0xf0 {
			continue
		}
		idx := int(es[i+2]>>2) & 0xf
		ch := int(es[i+2]&1)<<2 | int(es[i+3]>>6)
		if idx >= len(adtsRates) {
			return 0, 0
		}
		return adtsRates[idx], ch
	}
	return 0, 0
}

var ac3Rates = []int{48000, 44100, 32000, 0}

// ac3Channels is the full-bandwidth channel count of each AC-3 acmod.
var ac3Channels = []int{2, 1, 2, 3, 3, 4, 4, 5}

// ac3Params returns the AC-3/E-AC-3 sample rate and full-bandwidth channel
// count (LFE is not counted).
func ac3Params(es []byte) (int, int) {
	for i := 0; i+8 <= len(es); i++ {
		if es[i] != 0x0b || es[i+1] != 0x77 {
			continue
		}
		if es[i+5]>>3 > 10 { // E-AC-3: fscod and acmod in byte 4
			return ac3Rates[es[i+4]>>6], ac3Channels[es[i+4]>>1&7]
		}
		return ac3Rates[es[i+4]>>6], ac3Channels[es[i+6]>>5]
	}
	return 0, 0
}

// shiftTS rewrites PCR, PTS and DTS by delta (90 kHz) and renumbers
// continuity counters from cc, so repeated slate bytes form one continuous
// transport stream. It returns a new slice.
func shiftTS(src []byte, delta int64, cc map[uint16]byte) []byte {
	b := append([]byte(nil), src...)
	for off := 0; off+tsPacket <= len(b); off += tsPacket {
		p := b[off : off+tsPacket]
		if p[0] != 0x47 {
			continue
		}
		pid := uint16(p[1]&0x1f)<<8 | uint16(p[2])
		afc := (p[3] >> 4) & 3
		if afc&1 != 0 && pid != 0x1fff {
			next, seen := cc[pid]
			if seen {
				next = (next + 1) & 0xf
			} else {
				next = p[3] & 0xf
			}
			cc[pid] = next
			p[3] = p[3]&0xf0 | next
		}
		off4 := 4
		if afc&2 != 0 {
			n := int(p[4])
			if n > 0 && 5+n <= tsPacket && p[5]&0x10 != 0 && n >= 7 {
				base := uint64(p[6])<<25 | uint64(p[7])<<17 | uint64(p[8])<<9 | uint64(p[9])<<1 | uint64(p[10])>>7
				base = (base + uint64(delta)) & (1<<33 - 1)
				p[6], p[7], p[8], p[9] = byte(base>>25), byte(base>>17), byte(base>>9), byte(base>>1)
				p[10] = p[10]&0x7f | byte(base&1)<<7
			}
			off4 = 5 + n
		}
		if p[1]&0x40 == 0 || afc&1 == 0 || off4+14 > tsPacket {
			continue
		}
		pes := p[off4:]
		if pes[0] != 0 || pes[1] != 0 || pes[2] != 1 {
			continue
		}
		flags := pes[7] >> 6
		if flags&2 != 0 {
			shiftPESTime(pes[9:14], delta)
		}
		if flags == 3 && len(pes) >= 19 {
			shiftPESTime(pes[14:19], delta)
		}
	}
	return b
}

func shiftPESTime(t []byte, delta int64) {
	v := uint64(t[0]>>1&7)<<30 | uint64(t[1])<<22 | uint64(t[2]>>1)<<15 | uint64(t[3])<<7 | uint64(t[4]>>1)
	v = (v + uint64(delta)) & (1<<33 - 1)
	t[0] = t[0]&0xf1 | byte(v>>29)&0x0e
	t[1] = byte(v >> 22)
	t[2] = byte(v>>14) | 1
	t[3] = byte(v >> 7)
	t[4] = byte(v<<1) | 1
}

// mp4Boxes visits the boxes of one level.
func mp4Boxes(b []byte, fn func(typ string, body []byte, whole []byte)) {
	for i := 0; i+8 <= len(b); {
		size := int(binary.BigEndian.Uint32(b[i:]))
		hdr := 8
		if size == 1 && i+16 <= len(b) {
			size = int(binary.BigEndian.Uint64(b[i+8:]))
			hdr = 16
		} else if size == 0 {
			size = len(b) - i
		}
		if size < hdr || i+size > len(b) {
			return
		}
		fn(string(b[i+4:i+8]), b[i+hdr:i+size], b[i:i+size])
		i += size
	}
}

// mp4Timescales maps track IDs to media timescales from an init segment.
func mp4Timescales(init []byte) map[uint32]uint32 {
	out := map[uint32]uint32{}
	mp4Boxes(init, func(typ string, body, _ []byte) {
		if typ != "moov" {
			return
		}
		mp4Boxes(body, func(typ string, trak, _ []byte) {
			if typ != "trak" {
				return
			}
			var id, scale uint32
			mp4Boxes(trak, func(typ string, body, _ []byte) {
				switch typ {
				case "tkhd":
					if len(body) >= 24 {
						if body[0] == 1 {
							id = binary.BigEndian.Uint32(body[20:])
						} else {
							id = binary.BigEndian.Uint32(body[12:])
						}
					}
				case "mdia":
					mp4Boxes(body, func(typ string, body, _ []byte) {
						if typ == "mdhd" && len(body) >= 24 {
							if body[0] == 1 {
								scale = binary.BigEndian.Uint32(body[20:])
							} else {
								scale = binary.BigEndian.Uint32(body[12:])
							}
						}
					})
				}
			})
			if id != 0 && scale != 0 {
				out[id] = scale
			}
		})
	})
	return out
}

// shiftFMP4 moves every track fragment's decode time forward by seconds and
// sets the fragment sequence number. It returns a new slice.
func shiftFMP4(src []byte, scales map[uint32]uint32, seconds float64, sequence uint32) []byte {
	b := append([]byte(nil), src...)
	mp4Boxes(b, func(typ string, body, _ []byte) {
		if typ != "moof" {
			return
		}
		mp4Boxes(body, func(typ string, body, _ []byte) {
			switch typ {
			case "mfhd":
				if len(body) >= 8 {
					binary.BigEndian.PutUint32(body[4:], sequence)
				}
			case "traf":
				var track uint32
				mp4Boxes(body, func(typ string, body, _ []byte) {
					switch typ {
					case "tfhd":
						if len(body) >= 8 {
							track = binary.BigEndian.Uint32(body[4:])
						}
					case "tfdt":
						delta := uint64(seconds * float64(scales[track]))
						if len(body) >= 12 && body[0] == 1 {
							binary.BigEndian.PutUint64(body[4:], binary.BigEndian.Uint64(body[4:])+delta)
						} else if len(body) >= 8 {
							binary.BigEndian.PutUint32(body[4:], binary.BigEndian.Uint32(body[4:])+uint32(delta))
						}
					}
				})
			}
		})
	})
	return b
}

// splitTS cuts a transport stream segment at video keyframes so no part runs
// past maxDur by more than the rounding RFC 8216 allows (EXTINF rounded to
// the nearest second must not exceed the target). A relay's copy cut can land
// a GOP late at a reconnect splice, and the target is fixed for the
// playlist's life. Every part after the first starts with the segment's PAT
// and PMT, so each is independently decodable. A segment without a usable
// keyframe stays whole.
func splitTS(b []byte, maxDur time.Duration) [][]byte {
	if !isTS(b) || maxDur <= 0 {
		return [][]byte{b}
	}
	limit := uint64(maxDur.Seconds()*90000) + 90000*45/100
	var pat, pmt []byte
	pmtPID, videoPID := -1, -1
	type key struct {
		off int
		pts uint64
	}
	var keys []key
	first, last, haveFirst := uint64(0), uint64(0), false
	for off := 0; off+tsPacket <= len(b); off += tsPacket {
		p := b[off : off+tsPacket]
		if p[0] != 0x47 {
			return [][]byte{b}
		}
		pid := int(p[1]&0x1f)<<8 | int(p[2])
		pusi := p[1]&0x40 != 0
		payload := tsPayload(p)
		switch {
		case pid == 0 && pusi && pat == nil && payload != nil:
			pat = p
			pmtPID = ownedParsePAT(payload)
		case pid == pmtPID && pusi && pmt == nil && payload != nil:
			pmt = p
			for spid, st := range ownedParsePMT(payload) {
				if st.kind == "video" {
					videoPID = int(spid)
				}
			}
		case pid == videoPID && pusi && len(payload) >= 14 && payload[0] == 0 && payload[1] == 0 && payload[2] == 1 && payload[7]&0x80 != 0:
			t := payload[9:14]
			pts := uint64(t[0]>>1&7)<<30 | uint64(t[1])<<22 | uint64(t[2]>>1)<<15 | uint64(t[3])<<7 | uint64(t[4]>>1)
			if !haveFirst {
				first, last, haveFirst = pts, pts, true
				continue
			}
			last = max(last, pts)
			if (p[3]>>4)&2 != 0 && p[4] > 0 && p[5]&0x40 != 0 { // random access indicator
				keys = append(keys, key{off, pts})
			}
		}
	}
	if pat == nil || pmt == nil || !haveFirst || last < first {
		return [][]byte{b}
	}
	// While what remains from the current start runs past the limit, cut at
	// the furthest keyframe within it (leaving at least half a second).
	var cuts []int
	start, next := first, 0
	for last-start > limit {
		best := -1
		for i := next; i < len(keys) && keys[i].pts-start <= limit; i++ {
			if keys[i].pts-start >= 45000 {
				best = i
			}
		}
		if best < 0 {
			break
		}
		cuts = append(cuts, keys[best].off)
		start, next = keys[best].pts, best+1
	}
	if len(cuts) == 0 {
		return [][]byte{b}
	}
	parts := make([][]byte, 0, len(cuts)+1)
	prev := 0
	for _, c := range cuts {
		part := b[prev:c]
		if prev > 0 {
			part = append(append(append([]byte(nil), pat...), pmt...), part...)
		}
		parts = append(parts, part)
		prev = c
	}
	return append(parts, append(append(append([]byte(nil), pat...), pmt...), b[prev:]...))
}
