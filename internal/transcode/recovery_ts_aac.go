package transcode

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
)

const (
	recoveryTSAACByteLimit  = 32 << 20
	recoveryTSAACPESLimit   = 4096
	recoveryTSAACUnitLimit  = 32768
	recoveryTSAACSpanLimit  = 262144
	recoveryTSAACClockLimit = int64(1<<33) - 1
)

var errRecoveryTSAACUnsupported = errors.New("transcode: unsupported AAC byte observation")

type recoveryTSAACSpan struct{ offset, length int }
type recoveryTSAACUnit struct {
	// Header and raw spans address ONLY owned original file bytes, in order.
	header, raw            []recoveryTSAACSpan
	frameSHA256, rawSHA256 [32]byte
	pes, orderInPES        int
	// This is a declared-profile-derived position, NOT a decoded sample clock or
	// an observed per-AU timestamp. Every actual PES anchor must agree exactly.
	declaredProfilePTS90k int64
}
type recoveryTSAACPES struct {
	pid               uint16
	firstPacketOffset int
	streamID          byte
	pts90k, dts90k    int64
	hasDTS            bool
	sha256            [32]byte
}

// recoveryTSAACObservation has no receipt conversion, JSON promotion, exported
// API, or integration caller. Only observeRecoveryTSAAC constructs observations
// from an exact owned input copy. Facts establish audio bytes, not complete TS,
// video, decoder independence/sample counts, implicit SBR/PS absence, source AU
// indices, sample ends, fencing, retained publication, CAS or stitch authority.
// Declared MPEG-4 LC/48k/mono-stereo ADTS does not prove decoder semantics.
// Finite subset: 188-byte TS, stable single-packet/single-section PAT/PMT;
// descriptor-free selected AAC and optional H264; optional single-packet SDT
// (CRC checked, contents ignored) and null packets. H264 PES/clocks/pictures are
// deliberately uninterpreted. Unknown PIDs, transport discontinuities, duplicate
// CC, scrambled input, partial PES/AUs and cross-PES ADTS are unsupported.
// Input, PES, AU and aggregate span counts are capped above; each PES <=65541
// bytes and ADTS <=8191 bytes. All index sums are dominated by these limits.
// No wrapping clocks are inferred and no transport bytes are rewritten.
type recoveryTSAACObservation struct {
	owned        []byte
	sha256       [32]byte
	program, pid uint16
	// Canonical ASC from declared ADTS fields only; no extension assertion.
	declaredASC [2]byte
	pes         []recoveryTSAACPES
	units       []recoveryTSAACUnit
}

// Reader must bound its own blocking reads and must not retain/mutate Read
// buffers after returning. This function starts no goroutine. A byte reader over
// caller storage is copied; later caller mutation cannot alter the observation.
func observeRecoveryTSAAC(reader io.Reader, size int64) (*recoveryTSAACObservation, error) {
	if reader == nil || size <= 0 || size > recoveryTSAACByteLimit || size%188 != 0 {
		return nil, errRecoveryTSAACUnsupported
	}
	b := make([]byte, int(size))
	for off := 0; off < len(b); {
		n, e := reader.Read(b[off:])
		if n < 0 || n > len(b)-off {
			return nil, errRecoveryTSAACUnsupported
		}
		off += n
		if e != nil {
			if e == io.EOF && off == len(b) {
				break
			}
			return nil, errRecoveryTSAACUnsupported
		}
		if n == 0 {
			return nil, errRecoveryTSAACUnsupported
		}
	}
	var extra [1]byte
	if n, e := reader.Read(extra[:]); n != 0 || e != io.EOF {
		return nil, errRecoveryTSAACUnsupported
	}
	o := &recoveryTSAACObservation{owned: b, sha256: sha256.Sum256(b)}
	if e := o.parse(); e != nil {
		return nil, e
	}
	return o, nil
}

type recoveryTSAACPacket struct {
	pid     uint16
	cc      byte
	start   bool
	payload []byte
	offset  int
}

// Local finite envelope parser: playbackts's unexported primitives deliberately
// carry stricter complete-transport authority and are not exported for reuse.
func recoveryTSAACPacketAt(b []byte, off int) (recoveryTSAACPacket, error) {
	var v recoveryTSAACPacket
	if len(b) != 188 || b[0] != 0x47 || b[1]&0x80 != 0 || b[3]&0xc0 != 0 {
		return v, errRecoveryTSAACUnsupported
	}
	v.pid = uint16(b[1]&31)<<8 | uint16(b[2])
	v.cc = b[3] & 15
	v.start = b[1]&64 != 0
	afc := b[3] >> 4 & 3
	at := 4
	if afc == 0 {
		return v, errRecoveryTSAACUnsupported
	}
	if afc&2 != 0 {
		n := int(b[4])
		at = 5 + n
		if at > 188 || (afc == 2 && at != 188) || (afc == 3 && at >= 188) {
			return v, errRecoveryTSAACUnsupported
		}
		if n > 0 {
			flags := b[5]
			// Only random_access, elementary_stream_priority and PCR are supported.
			if flags & ^byte(0x70) != 0 {
				return v, errRecoveryTSAACUnsupported
			}
			used := 6
			if flags&0x10 != 0 {
				if n < 7 || b[10]&0x7e != 0x7e || (uint16(b[10]&1)<<8|uint16(b[11])) >= 300 {
					return v, errRecoveryTSAACUnsupported
				}
				used = 12
			}
			for _, x := range b[used:at] {
				if x != 255 {
					return v, errRecoveryTSAACUnsupported
				}
			}
		}
	}
	if afc&1 != 0 {
		v.payload = b[at:]
		v.offset = off + at
	} else if v.start {
		return v, errRecoveryTSAACUnsupported
	}
	return v, nil
}
func recoveryTSAACCRC(b []byte) uint32 {
	c := uint32(0xffffffff)
	for _, x := range b {
		c ^= uint32(x) << 24
		for j := 0; j < 8; j++ {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
	}
	return c
}
func recoveryTSAACSection(p recoveryTSAACPacket, table byte) ([]byte, error) {
	b := p.payload
	if !p.start || len(b) < 4 || b[0] != 0 {
		return nil, errRecoveryTSAACUnsupported
	}
	b = b[1:]
	sectionFlags := byte(0xb0)
	if table == 0x42 {
		sectionFlags = 0xf0
	} // SDT private_indicator is set.
	n := 3 + int(binary.BigEndian.Uint16(b[1:3])&0xfff)
	if n < 12 || n > len(b) || b[0] != table || b[1]&0xf0 != sectionFlags || b[5]&0xc1 != 0xc1 || b[6] != 0 || b[7] != 0 || recoveryTSAACCRC(b[:n]) != 0 {
		return nil, errRecoveryTSAACUnsupported
	}
	for _, x := range b[n:] {
		if x != 255 {
			return nil, errRecoveryTSAACUnsupported
		}
	}
	return b[:n], nil
}

func (o *recoveryTSAACObservation) parse() error {
	fail := errRecoveryTSAACUnsupported
	var pat, pmt []byte
	var pmtPID, videoPID uint16
	cc := map[uint16]byte{}
	var pending []byte
	var spans []recoveryTSAACSpan
	firstPacket := -1
	spanCount := 0
	finish := func() error {
		if len(pending) == 0 {
			return nil
		}
		if err := o.finishPES(pending, spans, firstPacket, &spanCount); err != nil {
			return err
		}
		pending = nil
		spans = nil
		return nil
	}
	for off := 0; off < len(o.owned); off += 188 {
		p, e := recoveryTSAACPacketAt(o.owned[off:off+188], off)
		if e != nil {
			return e
		}
		// Null payload carries no audio or table evidence; its CC is unspecified.
		if p.pid == 8191 {
			continue
		}
		if p.pid != 0 && p.pid != 17 && (pat == nil || p.pid != pmtPID) && (pmt == nil || (p.pid != o.pid && p.pid != videoPID)) {
			return fail
		}
		if old, seen := cc[p.pid]; seen {
			want := old
			if len(p.payload) > 0 {
				want = (want + 1) & 15
			}
			if want != p.cc {
				return fail
			}
		}
		cc[p.pid] = p.cc
		if len(p.payload) == 0 {
			continue
		}
		if p.pid == 17 {
			if _, e := recoveryTSAACSection(p, 0x42); e != nil {
				return e
			}
			continue
		}
		if p.pid == 0 {
			s, e := recoveryTSAACSection(p, 0)
			if e != nil {
				return e
			}
			if len(s) != 16 || s[10]&0xe0 != 0xe0 {
				return fail
			}
			program := binary.BigEndian.Uint16(s[8:10])
			pid := binary.BigEndian.Uint16(s[10:12]) & 8191
			if program == 0 || pid < 32 || pid == 8191 || (pat != nil && !bytes.Equal(pat, s)) {
				return fail
			}
			pat = s
			o.program = program
			pmtPID = pid
			continue
		}
		if p.pid == pmtPID {
			s, e := recoveryTSAACSection(p, 2)
			if e != nil {
				return e
			}
			if len(s) < 21 || binary.BigEndian.Uint16(s[3:5]) != o.program || s[8]&0xe0 != 0xe0 || s[10] != 0xf0 || s[11] != 0 {
				return fail
			}
			if pmt != nil {
				if !bytes.Equal(pmt, s) {
					return fail
				}
				continue
			}
			for at := 12; at < len(s)-4; at += 5 {
				if len(s)-4-at < 5 || s[at+1]&0xe0 != 0xe0 || s[at+3] != 0xf0 || s[at+4] != 0 {
					return fail
				}
				pid := binary.BigEndian.Uint16(s[at+1:at+3]) & 8191
				if pid < 32 || pid == 8191 || pid == pmtPID || pid == o.pid || pid == videoPID {
					return fail
				}
				switch s[at] {
				case 0x0f:
					if o.pid != 0 {
						return fail
					}
					o.pid = pid
				case 0x1b:
					if videoPID != 0 {
						return fail
					}
					videoPID = pid
				default:
					return fail
				}
			}
			pcrPID := binary.BigEndian.Uint16(s[8:10]) & 8191
			if o.pid == 0 || (pcrPID != o.pid && (videoPID == 0 || pcrPID != videoPID)) {
				return fail
			}
			pmt = s
			continue
		}
		if p.pid == videoPID {
			continue
		} // No video PES parsing, PTS ordering or completeness claim.
		if p.start {
			if e := finish(); e != nil {
				return e
			}
			firstPacket = off
		} else if len(pending) == 0 {
			return fail
		}
		if len(p.payload) > 65541-len(pending) || len(spans) >= recoveryTSAACSpanLimit {
			return fail
		}
		pending = append(pending, p.payload...)
		spans = append(spans, recoveryTSAACSpan{p.offset, len(p.payload)})
	}
	if pmt == nil {
		return fail
	}
	if e := finish(); e != nil {
		return e
	}
	if len(o.units) == 0 {
		return fail
	}
	return nil
}

func recoveryTSAACStamp(b []byte, prefix byte) (int64, bool) {
	if len(b) < 5 || b[0]>>4 != prefix || b[0]&1 == 0 || b[2]&1 == 0 || b[4]&1 == 0 {
		return 0, false
	}
	return int64(b[0]>>1&7)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1), true
}

// Selects a range of reassembled PES bytes while retaining original file spans.
// It never derives file locations from decoder position, time or AU ordinal.
func recoveryTSAACSubspans(spans []recoveryTSAACSpan, start, n int) ([]recoveryTSAACSpan, error) {
	if start < 0 || n <= 0 || start > 65541 || n > 65541-start {
		return nil, errRecoveryTSAACUnsupported
	}
	var out []recoveryTSAACSpan
	for _, s := range spans {
		if start >= s.length {
			start -= s.length
			continue
		}
		take := min(n, s.length-start)
		out = append(out, recoveryTSAACSpan{s.offset + start, take})
		n -= take
		start = 0
		if n == 0 {
			return out, nil
		}
	}
	return nil, errRecoveryTSAACUnsupported
}

func (o *recoveryTSAACObservation) finishPES(b []byte, spans []recoveryTSAACSpan, first int, spanCount *int) error {
	fail := errRecoveryTSAACUnsupported
	if len(o.pes) >= recoveryTSAACPESLimit || len(b) < 14 || !bytes.Equal(b[:3], []byte{0, 0, 1}) || b[3] < 0xc0 || b[3] > 0xdf || int(binary.BigEndian.Uint16(b[4:6])) != len(b)-6 || b[6] != 0x80 {
		return fail
	}
	// Supported optional header is PTS, or PTS+DTS, plus ff stuffing only.
	if b[7] != 0x80 && b[7] != 0xc0 {
		return fail
	}
	need := 5
	if b[7] == 0xc0 {
		need = 10
	}
	hdr := int(b[8])
	if hdr < need || hdr > len(b)-9 {
		return fail
	}
	prefix := byte(2)
	if need == 10 {
		prefix = 3
	}
	pts, ok := recoveryTSAACStamp(b[9:14], prefix)
	if !ok {
		return fail
	}
	pes := recoveryTSAACPES{pid: o.pid, firstPacketOffset: first, streamID: b[3], pts90k: pts, sha256: sha256.Sum256(b), hasDTS: need == 10}
	if need == 10 {
		dts, ok := recoveryTSAACStamp(b[14:19], 1)
		if !ok || dts != pts {
			return fail
		}
		pes.dts90k = dts
	}
	for _, x := range b[9+need : 9+hdr] {
		if x != 255 {
			return fail
		}
	}
	if len(o.pes) > 0 && pes.streamID != o.pes[0].streamID {
		return fail
	}
	// Checked declared-profile clock: 1024 samples / 48000 Hz = 1920 ticks.
	base := pts
	if len(o.pes) > 0 {
		base = o.pes[0].pts90k
	}
	if int64(len(o.units)) > (recoveryTSAACClockLimit-base)/1920 || pts != base+int64(len(o.units))*1920 {
		return fail
	}
	order := 0
	for at := 9 + hdr; at < len(b); {
		if len(o.units) >= recoveryTSAACUnitLimit || len(b)-at < 7 {
			return fail
		}
		h := b[at : at+7]
		// MPEG-4, layer 0, protection_absent, LC, sampling_frequency_index=3.
		if h[0] != 255 || h[1] != 0xf1 || h[2]>>6 != 1 || h[2]>>2&15 != 3 || h[6]&3 != 0 {
			return fail
		}
		channels := (h[2]&1)<<2 | h[3]>>6
		if channels != 1 && channels != 2 {
			return fail
		}
		asc := [2]byte{0x11, 0x80 | channels<<3}
		if len(o.units) > 0 && o.declaredASC != asc {
			return fail
		}
		o.declaredASC = asc
		n := int(h[3]&3)<<11 | int(h[4])<<3 | int(h[5]>>5)
		if n <= 7 || n > len(b)-at {
			return fail
		}
		header, e := recoveryTSAACSubspans(spans, at, 7)
		if e != nil {
			return e
		}
		raw, e := recoveryTSAACSubspans(spans, at+7, n-7)
		if e != nil {
			return e
		}
		count := len(header) + len(raw)
		if count > recoveryTSAACSpanLimit-*spanCount {
			return fail
		}
		*spanCount += count
		if int64(len(o.units)) > (recoveryTSAACClockLimit-base)/1920 {
			return fail
		}
		o.units = append(o.units, recoveryTSAACUnit{header: header, raw: raw, frameSHA256: sha256.Sum256(b[at : at+n]), rawSHA256: sha256.Sum256(b[at+7 : at+n]), pes: len(o.pes), orderInPES: order, declaredProfilePTS90k: base + int64(len(o.units))*1920})
		at += n
		order++
	}
	if order == 0 {
		return fail
	}
	o.pes = append(o.pes, pes)
	return nil
}
