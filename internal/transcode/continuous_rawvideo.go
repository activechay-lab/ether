package transcode

import (
	"encoding/binary"
	"io"
	"time"
)

// Timestamped I420 lets the switcher reduce input picture cadence without
// removing any audio ticks. The output fps filter repeats after conversion.
// Clusters are complete and bounded; the live Segment has unknown length.
func rawVideoHeader(f RawFormat) []byte {
	bitmap := make([]byte, 40)
	binary.LittleEndian.PutUint32(bitmap, 40)
	binary.LittleEndian.PutUint32(bitmap[4:], uint32(f.Width))
	binary.LittleEndian.PutUint32(bitmap[8:], uint32(f.Height))
	binary.LittleEndian.PutUint16(bitmap[12:], 1)
	binary.LittleEndian.PutUint16(bitmap[14:], 12)
	copy(bitmap[16:], "I420")
	binary.LittleEndian.PutUint32(bitmap[20:], uint32(f.Width*f.Height*3/2))
	header := rawElement(0x1a45dfa3, rawUint(0x4286, 1), rawUint(0x42f7, 1), rawUint(0x42f2, 4), rawUint(0x42f3, 8), rawElement(0x4282, []byte("matroska")), rawUint(0x4287, 4), rawUint(0x4285, 2))
	header = append(header, 0x18, 0x53, 0x80, 0x67, 0x01, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	header = append(header, rawElement(0x1549a966, rawUint(0x2ad7b1, 1000), rawElement(0x4d80, []byte("Ether")), rawElement(0x5741, []byte("Ether")))...)
	return append(header, rawElement(0x1654ae6b, rawElement(0xae, rawUint(0xd7, 1), rawUint(0x73c5, 1), rawUint(0x83, 1), rawElement(0x86, []byte("V_MS/VFW/FOURCC")), rawElement(0x63a2, bitmap), rawUint(0x23e383, uint64(time.Second)*uint64(f.FPSDen)/uint64(f.FPSNum)), rawElement(0xe0, rawUint(0xb0, uint64(f.Width)), rawUint(0xba, uint64(f.Height)))))...)
}

func writeRawVideo(w io.Writer, picture []byte, tick int64, f RawFormat) error {
	timestamp := rawUint(0xe7, uint64(tick)*uint64(f.FPSDen)*1000000/uint64(f.FPSNum))
	block := append(rawID(0xa3), rawSize(uint64(len(picture)+4))...)
	block = append(block, 0x81, 0, 0, 0x80)
	prefix := append(rawID(0x1f43b675), rawSize(uint64(len(timestamp)+len(block)+len(picture)))...)
	prefix = append(prefix, timestamp...)
	prefix = append(prefix, block...)
	if _, err := w.Write(prefix); err != nil {
		return err
	}
	_, err := w.Write(picture)
	return err
}
func rawElement(id uint64, parts ...[]byte) []byte {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	b := append(rawID(id), rawSize(uint64(n))...)
	for _, p := range parts {
		b = append(b, p...)
	}
	return b
}
func rawUint(id, n uint64) []byte { return rawElement(id, rawID(n)) }
func rawID(n uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], n)
	at := 0
	for at < 7 && b[at] == 0 {
		at++
	}
	return append([]byte(nil), b[at:]...)
}
func rawSize(n uint64) []byte {
	for width := 1; width <= 8; width++ {
		if n < (uint64(1)<<uint(width*7))-1 {
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], n|uint64(1)<<uint(width*7))
			return append([]byte(nil), b[8-width:]...)
		}
	}
	panic("raw video element too large")
}
