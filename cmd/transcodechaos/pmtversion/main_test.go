package main

import (
	"encoding/binary"
	"testing"
)

func psi(pid uint16, s []byte) []byte {
	p := make([]byte, 188)
	for i := range p {
		p[i] = 0xff
	}
	p[0] = 0x47
	p[1] = 0x40 | byte(pid>>8)
	p[2] = byte(pid)
	p[3] = 0x10
	p[4] = 0
	copy(p[5:], s)
	return p
}
func withCRC(s []byte) []byte {
	b := append([]byte(nil), s...)
	b = append(b, 0, 0, 0, 0)
	binary.BigEndian.PutUint32(b[len(b)-4:], crc(b[:len(b)-4]))
	return b
}
func TestPMTVersionAndCRC(t *testing.T) {
	pat := withCRC([]byte{0, 0xb0, 13, 0, 1, 0xc1, 0, 0, 0, 1, 0xe1, 0})
	pmt := withCRC([]byte{2, 0xb0, 13, 0, 1, 0xff, 0, 0, 0xe1, 0, 0xf0, 0})
	data := append(psi(0, pat), psi(256, pmt)...)
	n, err := rewrite(data)
	if err != nil || n != 1 {
		t.Fatalf("rewrite %d: %v", n, err)
	}
	got, err := section(data[188:])
	if err != nil || got[5] != 0xc1 || crc(got) != 0 {
		t.Fatalf("version wrap/CRC: %x, %v", got, err)
	}
}
