package main

import (
	"encoding/binary"
	"fmt"
)

// MPEG-TS helpers for the live fault simulator. They edit packets in place
// and never change packet count unless a fault drops packets on purpose.

const tsPacket = 188

const pts33 = int64(1) << 33

func tsPID(p []byte) uint16 { return uint16(p[1]&0x1f)<<8 | uint16(p[2]) }
func tsPUSI(p []byte) bool  { return p[1]&0x40 != 0 }
func tsHasPayload(p []byte) bool {
	return p[3]&0x10 != 0
}

// tsPayloadStart returns the payload offset, or -1 when the packet has none.
func tsPayloadStart(p []byte) int {
	if !tsHasPayload(p) {
		return -1
	}
	pos := 4
	if p[3]&0x20 != 0 {
		pos += 1 + int(p[4])
	}
	if pos >= tsPacket {
		return -1
	}
	return pos
}

// tsRandomAccess reports the adaptation-field random_access_indicator that
// ffmpeg's muxer sets on keyframe packets.
func tsRandomAccess(p []byte) bool {
	return p[3]&0x20 != 0 && p[4] > 0 && p[5]&0x40 != 0
}

func readTS33(b []byte) int64 {
	return int64(b[0]>>1&0x07)<<30 | int64(b[1])<<22 | int64(b[2]>>1)<<15 | int64(b[3])<<7 | int64(b[4]>>1)
}
func writeTS33(b []byte, v int64) {
	v = ((v % pts33) + pts33) % pts33
	b[0] = b[0]&0xf1 | byte(v>>29)&0x0e
	b[1] = byte(v >> 22)
	b[2] = b[2]&0x01 | byte(v>>14)&0xfe
	b[3] = byte(v >> 7)
	b[4] = b[4]&0x01 | byte(v<<1)&0xfe
}

// pesTimes locates PTS and DTS fields of a PES header starting a payload.
// Offsets are -1 when absent.
func pesTimes(p []byte) (pts, dts int) {
	pts, dts = -1, -1
	if !tsPUSI(p) {
		return
	}
	at := tsPayloadStart(p)
	if at < 0 || at+14 > tsPacket || p[at] != 0 || p[at+1] != 0 || p[at+2] != 1 {
		return
	}
	id := p[at+3]
	// Streams without the optional PES header (padding, private_2, PSM...).
	if id == 0xbc || id == 0xbe || id == 0xbf || id == 0xf0 || id == 0xf1 || id == 0xff || id == 0xf2 || id == 0xf8 {
		return
	}
	flags := p[at+7] >> 6
	if flags&2 != 0 {
		pts = at + 9
	}
	if flags == 3 {
		dts = at + 14
	}
	return
}

// packetPTS returns the PTS a PES-starting packet carries.
func packetPTS(p []byte) (int64, bool) {
	at, _ := pesTimes(p)
	if at < 0 {
		return 0, false
	}
	return readTS33(p[at:]), true
}

// shiftPacketTime adds off (90 kHz) to PTS, DTS and PCR, modulo 2^33.
func shiftPacketTime(p []byte, off int64) {
	if off == 0 {
		return
	}
	if p[3]&0x20 != 0 && p[4] >= 7 && p[5]&0x10 != 0 {
		// PCR base: 33 bits starting at byte 6, followed by 6 reserved bits and a 9-bit extension.
		base := int64(p[6])<<25 | int64(p[7])<<17 | int64(p[8])<<9 | int64(p[9])<<1 | int64(p[10]>>7)
		base = (((base + off) % pts33) + pts33) % pts33
		p[6], p[7], p[8], p[9] = byte(base>>25), byte(base>>17), byte(base>>9), byte(base>>1)
		p[10] = p[10]&0x7f | byte(base<<7)&0x80
	}
	pts, dts := pesTimes(p)
	if pts >= 0 {
		writeTS33(p[pts:], readTS33(p[pts:])+off)
	}
	if dts >= 0 {
		writeTS33(p[dts:], readTS33(p[dts:])+off)
	}
}

func mpegCRC(b []byte) uint32 {
	c := uint32(0xffffffff)
	for _, v := range b {
		c ^= uint32(v) << 24
		for range 8 {
			if c&0x80000000 != 0 {
				c = c<<1 ^ 0x04c11db7
			} else {
				c <<= 1
			}
		}
	}
	return c
}

// psiSection returns the single-packet PSI section a packet starts, or nil.
// Sections that span packets are reported as an error: fixtures never use them.
func psiSection(p []byte) ([]byte, error) {
	if !tsPUSI(p) {
		return nil, nil
	}
	pos := tsPayloadStart(p)
	if pos < 0 {
		return nil, nil
	}
	pos += 1 + int(p[pos])
	if pos+3 > tsPacket {
		return nil, fmt.Errorf("PSI header split")
	}
	n := 3 + int(binary.BigEndian.Uint16(p[pos+1:pos+3])&0xfff)
	if pos+n > tsPacket {
		return nil, fmt.Errorf("PSI section spans packets")
	}
	return p[pos : pos+n], nil
}

type tsStream struct {
	PID  uint16
	Type byte
}

// tsProgram is what the PAT/PMT of a segment declares.
type tsProgram struct {
	PMTPID     uint16
	PMTVersion int
	Streams    []tsStream
}

// parseProgram reads the first PAT and PMT in data.
func parseProgram(data []byte) (tsProgram, error) {
	var prog tsProgram
	prog.PMTVersion = -1
	for i := 0; i+tsPacket <= len(data); i += tsPacket {
		p := data[i : i+tsPacket]
		if p[0] != 0x47 {
			return prog, fmt.Errorf("sync lost at %d", i)
		}
		pid := tsPID(p)
		if pid != 0 && (prog.PMTPID == 0 || pid != prog.PMTPID) {
			continue
		}
		s, err := psiSection(p)
		if err != nil || s == nil || len(s) < 12 {
			continue
		}
		if mpegCRC(s) != 0 {
			return prog, fmt.Errorf("PSI CRC mismatch on PID %d", pid)
		}
		if pid == 0 && s[0] == 0 && prog.PMTPID == 0 {
			for j := 8; j+4 <= len(s)-4; j += 4 {
				if binary.BigEndian.Uint16(s[j:j+2]) != 0 {
					prog.PMTPID = binary.BigEndian.Uint16(s[j+2:j+4]) & 0x1fff
					break
				}
			}
			continue
		}
		if pid == prog.PMTPID && s[0] == 2 {
			prog.PMTVersion = int(s[5]>>1) & 31
			info := int(binary.BigEndian.Uint16(s[10:12]) & 0xfff)
			for j := 12 + info; j+5 <= len(s)-4; {
				es := int(binary.BigEndian.Uint16(s[j+3:j+5]) & 0xfff)
				prog.Streams = append(prog.Streams, tsStream{PID: binary.BigEndian.Uint16(s[j+1:j+3]) & 0x1fff, Type: s[j]})
				j += 5 + es
			}
			return prog, nil
		}
	}
	return prog, fmt.Errorf("no PAT/PMT")
}

// setPMTVersion rewrites every PMT section's version_number and CRC.
func setPMTVersion(data []byte, pmtPID uint16, version int) int {
	n := 0
	for i := 0; i+tsPacket <= len(data); i += tsPacket {
		p := data[i : i+tsPacket]
		if tsPID(p) != pmtPID {
			continue
		}
		s, err := psiSection(p)
		if err != nil || s == nil || len(s) < 12 || s[0] != 2 {
			continue
		}
		s[5] = s[5]&0xc1 | byte(version&31)<<1
		binary.BigEndian.PutUint32(s[len(s)-4:], mpegCRC(s[:len(s)-4]))
		n++
	}
	return n
}

// ccCounter assigns continuity counters so a stitched stream is continuous
// per PID. Packets without payload repeat the previous counter (H.222 2.4.3.3).
type ccCounter map[uint16]byte

func (c ccCounter) stamp(p []byte) {
	pid := tsPID(p)
	if pid == 0x1fff {
		return
	}
	cur, seen := c[pid]
	if tsHasPayload(p) {
		if seen {
			cur = (cur + 1) & 15
		} else {
			cur = 0
		}
		c[pid] = cur
	} else if !seen {
		c[pid] = 0
		cur = 0
	}
	p[3] = p[3]&0xf0 | cur
}

// payloadCounts counts payload-bearing packets per PID, for computing the
// continuity counter at any point of a loop without replaying it.
func payloadCounts(data []byte) map[uint16]int {
	out := map[uint16]int{}
	for i := 0; i+tsPacket <= len(data); i += tsPacket {
		p := data[i : i+tsPacket]
		if tsHasPayload(p) && tsPID(p) != 0x1fff {
			out[tsPID(p)]++
		}
	}
	return out
}
