// Command pmtversion increments PMT versions in MPEG-TS packets. It fails
// closed if a PAT or PMT section spans packets; it is a fixture utility, not
// a general purpose transport-stream demultiplexer.
package main

import (
	"encoding/binary"
	"fmt"
	"os"
)

func section(p []byte) ([]byte, error) {
	if len(p) != 188 || p[0] != 0x47 {
		return nil, fmt.Errorf("invalid TS packet")
	}
	if p[1]&0x40 == 0 || p[3]&0x10 == 0 {
		return nil, nil
	}
	pos := 4
	if p[3]&0x20 != 0 {
		pos += 1 + int(p[4])
	}
	if pos >= 188 {
		return nil, fmt.Errorf("invalid adaptation field")
	}
	pos += 1 + int(p[pos])
	if pos >= 188 {
		return nil, fmt.Errorf("invalid pointer field")
	}
	if pos+3 > 188 {
		return nil, fmt.Errorf("section header split")
	}
	n := 3 + int(binary.BigEndian.Uint16(p[pos+1:pos+3])&0xfff)
	if pos+n > 188 {
		return nil, fmt.Errorf("section spans packets")
	}
	return p[pos : pos+n], nil
}
func crc(b []byte) uint32 {
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
func rewrite(data []byte) (int, error) {
	if len(data)%188 != 0 {
		return 0, fmt.Errorf("input length is not TS aligned")
	}
	pids := map[uint16]bool{}
	count := 0
	for i := 0; i < len(data); i += 188 {
		p := data[i : i+188]
		if p[0] != 0x47 {
			return count, fmt.Errorf("sync lost at %d", i)
		}
		pid := uint16(p[1]&31)<<8 | uint16(p[2])
		if pid != 0 && !pids[pid] {
			continue
		}
		s, err := section(p)
		if err != nil {
			return count, err
		}
		if s == nil {
			continue
		}
		if len(s) < 12 || crc(s) != 0 {
			return count, fmt.Errorf("invalid PSI section or CRC")
		}
		if pid == 0 && s[0] == 0 {
			for j := 8; j+4 <= len(s)-4; j += 4 {
				if binary.BigEndian.Uint16(s[j:j+2]) != 0 {
					pids[binary.BigEndian.Uint16(s[j+2:j+4])&0x1fff] = true
				}
			}
			continue
		}
		if pids[pid] && s[0] == 2 {
			s[5] = (s[5] & 0xc1) | (((s[5]>>1 + 1) & 31) << 1)
			binary.BigEndian.PutUint32(s[len(s)-4:], crc(s[:len(s)-4]))
			count++
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("no PMT found")
	}
	return count, nil
}
func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: pmtversion input.ts output.ts")
		os.Exit(2)
	}
	b, e := os.ReadFile(os.Args[1])
	if e == nil {
		_, e = rewrite(b)
	}
	if e == nil {
		e = os.WriteFile(os.Args[2], b, 0600)
	}
	if e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
