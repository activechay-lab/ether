package playbackbuffer

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"sort"
)

// Facts describe container observations, not a decoder verdict or sample-end
// durations. Verified is deliberately false for unsupported formats, transport
// discontinuities, clock wrap, missing tracks, and structural parse failures.
// An initial per-PID discontinuity marker is a legal muxer reset, not an
// intra-segment discontinuity.
// MPEGTS timestamps are unsigned 33-bit ticks at exactly 90000 Hz. Wrap is
// rejected, never silently normalized against an unrelated generation.
type Facts struct {
	Verified bool
	Reason   string
	Tracks   []TrackFacts
}

// TrackFacts timestamps are the first and maximum observed PES PTS and first/last
// PES DTS, not every access-unit timestamp. In particular an AAC PES can contain
// several ADTS frames after LastPTS. No sample-end time is inferred.
type TrackFacts struct {
	PID                                  uint16
	StreamType                           byte
	FirstPTS, LastPTS, FirstDTS, LastDTS int64
	Independent                          bool
	Config                               [32]byte
}

func inspect(f Format, k Kind, b []byte) Facts {
	if f != MPEGTS || k != Segment {
		return Facts{Reason: "unsupported format or init"}
	}
	tracks, ok := parseTS(b)
	if !ok {
		return Facts{Reason: "unsupported, incomplete, discontinuous, or corrupt TS"}
	}
	return Facts{Verified: true, Tracks: tracks}
}
func follows(a, b Facts) bool {
	if !a.Verified || !b.Verified || len(a.Tracks) == 0 || len(a.Tracks) != len(b.Tracks) {
		return false
	}
	for i, x := range a.Tracks {
		y := b.Tracks[i]
		if x.PID != y.PID || x.StreamType != y.StreamType || x.Config != y.Config || !y.Independent || y.FirstDTS <= x.LastDTS || y.FirstPTS <= x.LastPTS {
			return false
		}
	}
	return true
}
func crc(b []byte) uint32 {
	c := uint32(0xffffffff)
	for _, v := range b {
		c ^= uint32(v) << 24
		for i := 0; i < 8; i++ {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
	}
	return c
}

// Conservative one-packet PSI support matches the fixed CPU acceptance stream.
// Multi-packet tables, multi-program streams and unknown elementary streams are
// retained but produce no verified facts.
func section(p []byte, id byte) ([]byte, bool) {
	if len(p) < 1 {
		return nil, false
	}
	n := int(p[0]) + 1
	if n+3 > len(p) {
		return nil, false
	}
	b := p[n:]
	size := 3 + int(binary.BigEndian.Uint16(b[1:3])&0xfff)
	if size < 12 || size > len(b) || b[0] != id || b[1]&0x80 == 0 {
		return nil, false
	}
	b = b[:size]
	if b[5]&1 == 0 || b[6] != 0 || b[7] != 0 || crc(b) != 0 {
		return nil, false
	}
	return b, true
}

type tsTrack struct {
	fact     TrackFacts
	pes      []byte
	count    int
	config   []byte
	sps, pps bool
	firstVCL bool
}

func parseTS(b []byte) ([]TrackFacts, bool) {
	if len(b) == 0 || len(b)%188 != 0 {
		return nil, false
	}
	pmt := -1
	tracks := map[uint16]*tsTrack{}
	cc := map[uint16]byte{}
	seen := map[uint16]bool{}
	var patBytes, pmtBytes []byte
	for off := 0; off < len(b); off += 188 {
		p := b[off : off+188]
		if p[0] != 0x47 || p[1]&0x80 != 0 || p[3]&0xc0 != 0 {
			return nil, false
		}
		pid := uint16(p[1]&31)<<8 | uint16(p[2])
		first := !seen[pid]
		seen[pid] = true
		afc := (p[3] >> 4) & 3
		if afc == 0 {
			return nil, false
		}
		o := 4
		if afc&2 != 0 {
			n := int(p[4])
			if n > 183 || 5+n > 188 {
				return nil, false
			}
			// A restarted child muxer marks its first packet on each PID.
			// Later resets still invalidate this complete segment.
			if n > 0 && p[5]&0x80 != 0 && !first {
				return nil, false
			}
			o += 1 + n
		}
		if afc&1 == 0 {
			continue
		}
		if o >= 188 {
			return nil, false
		}
		if pid == 8191 {
			continue
		}
		if prev, ok := cc[pid]; ok && p[3]&15 != (prev+1)&15 {
			return nil, false
		}
		cc[pid] = p[3] & 15
		payload := p[o:]
		start := p[1]&0x40 != 0
		if pid == 0 {
			if !start {
				return nil, false
			}
			v, ok := section(payload, 0)
			if !ok || len(v) != 16 {
				return nil, false
			}
			if binary.BigEndian.Uint16(v[8:10]) == 0 {
				return nil, false
			}
			if patBytes != nil && !bytes.Equal(patBytes, v) {
				return nil, false
			}
			patBytes = v
			pmt = int(binary.BigEndian.Uint16(v[10:12]) & 8191)
			continue
		}
		if int(pid) == pmt {
			if !start {
				return nil, false
			}
			v, ok := section(payload, 2)
			if !ok || len(v) < 16 {
				return nil, false
			}
			if pmtBytes != nil {
				if !bytes.Equal(pmtBytes, v) {
					return nil, false
				}
				continue
			}
			pmtBytes = v
			i := 12 + int(binary.BigEndian.Uint16(v[10:12])&4095)
			end := len(v) - 4
			for i < end {
				if i+5 > end {
					return nil, false
				}
				typ := v[i]
				id := binary.BigEndian.Uint16(v[i+1:i+3]) & 8191
				n := int(binary.BigEndian.Uint16(v[i+3:i+5]) & 4095)
				if (typ != 0x1b && typ != 0x0f) || tracks[id] != nil || i+5+n > end || id == 0 || int(id) == pmt {
					return nil, false
				}
				tracks[id] = &tsTrack{fact: TrackFacts{PID: id, StreamType: typ}}
				i += 5 + n
			}
			continue
		}
		t := tracks[pid]
		if t == nil {
			if pid == 17 {
				continue
			}
			return nil, false
		}
		if start {
			if len(t.pes) > 0 && !finishPES(t) {
				return nil, false
			}
			t.pes = t.pes[:0]
		} else if len(t.pes) == 0 {
			return nil, false
		}
		t.pes = append(t.pes, payload...)
	}
	if pmtBytes == nil || len(tracks) == 0 {
		return nil, false
	}
	out := make([]TrackFacts, 0, len(tracks))
	for _, t := range tracks {
		if !finishPES(t) || t.count == 0 {
			return nil, false
		}
		t.fact.Config = sha256.Sum256(t.config)
		out = append(out, t.fact)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out, true
}
func timestamp(b []byte, prefix byte) (int64, bool) {
	if len(b) < 5 || b[0]>>4 != prefix || b[0]&1 == 0 || b[2]&1 == 0 || b[4]&1 == 0 {
		return 0, false
	}
	return int64(b[0]>>1&7)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1), true
}
func finishPES(t *tsTrack) bool {
	b := t.pes
	if len(b) < 14 || b[0] != 0 || b[1] != 0 || b[2] != 1 || b[6]&0xc0 != 0x80 || b[6]&0x30 != 0 {
		return false
	}
	n := int(binary.BigEndian.Uint16(b[4:6]))
	// H.264 PES commonly omits packet length. The enclosing TS payload-unit
	// starts (or segment end) already delimit the assembled video packet.
	if (n == 0 && t.fact.StreamType != 0x1b) || (n != 0 && n+6 != len(b)) {
		return false
	}
	flags := b[7] >> 6
	h := int(b[8])
	if (flags != 2 && flags != 3) || 9+h >= len(b) || h < 5 {
		return false
	}
	pts, ok := timestamp(b[9:], flags)
	if !ok {
		return false
	}
	dts := pts
	if flags == 3 {
		if h < 10 {
			return false
		}
		dts, ok = timestamp(b[14:], 1)
		if !ok {
			return false
		}
	}
	if t.count == 0 {
		t.fact.FirstPTS = pts
		t.fact.FirstDTS = dts
	} else if dts <= t.fact.LastDTS || pts < t.fact.FirstPTS {
		return false
	}
	if pts > t.fact.LastPTS || t.count == 0 {
		t.fact.LastPTS = pts
	}
	t.fact.LastDTS = dts
	payload := b[9+h:]
	if t.fact.StreamType == 0x1b {
		if b[3] < 0xe0 || b[3] > 0xef || !h264(t, payload) {
			return false
		}
	} else {
		if b[3] < 0xc0 || b[3] > 0xdf || !adts(t, payload) {
			return false
		}
	}
	t.count++
	return true
}

// Annex B observations follow the owned transcode PES clock concepts, but do
// not reuse its first-timestamp-only helper as a continuity certificate.
func h264(t *tsTrack, b []byte) bool {
	nalStart := func(i int) int {
		if i+3 <= len(b) && b[i] == 0 && b[i+1] == 0 {
			if b[i+2] == 1 {
				return 3
			}
			if i+4 <= len(b) && b[i+2] == 0 && b[i+3] == 1 {
				return 4
			}
		}
		return 0
	}
	i := 0
	vcl := false
	for i < len(b) {
		prefix := nalStart(i)
		if prefix == 0 {
			return false
		}
		a := i + prefix
		j := a
		for j < len(b) && nalStart(j) == 0 {
			j++
		}
		end := j
		for end > a && b[end-1] == 0 {
			end--
		}
		if end <= a || b[a]&0x80 != 0 {
			return false
		}
		typ := b[a] & 31
		nal := b[a:end]
		switch typ {
		case 7, 8:
			if len(nal) < 4 {
				return false
			}
			if t.count == 0 && !t.firstVCL {
				t.config = append(t.config, byte(typ), byte(len(nal)>>8), byte(len(nal)))
				t.config = append(t.config, nal...)
				if typ == 7 {
					t.sps = true
				} else {
					t.pps = true
				}
			} else {
				return false
			}
		case 1, 5:
			if len(nal) < 2 {
				return false
			}
			if !t.firstVCL {
				t.fact.Independent = typ == 5 && t.sps && t.pps
				t.firstVCL = true
			}
			vcl = true
		case 6, 9, 10, 11, 12:
		default:
			return false
		}
		i = j
	}
	return vcl
}
func adts(t *tsTrack, b []byte) bool {
	frames := 0
	for len(b) > 0 {
		if len(b) < 7 || b[0] != 0xff || b[1]&0xf6 != 0xf0 || b[6]&3 != 0 {
			return false
		}
		freq := (b[2] >> 2) & 15
		channels := (b[2]&1)<<2 | b[3]>>6
		if freq >= 13 || channels == 0 {
			return false
		}
		header := 7
		if b[1]&1 == 0 {
			header = 9
		}
		n := int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5]>>5)
		if n <= header || n > len(b) {
			return false
		}
		config := []byte{b[1] & 8, b[2] >> 6, freq, channels}
		if t.config == nil {
			t.config = config
		} else if !bytes.Equal(t.config, config) {
			return false
		}
		frames++
		b = b[n:]
	}
	if frames == 0 {
		return false
	}
	t.fact.Independent = true
	return true
}
