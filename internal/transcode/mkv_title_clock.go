package transcode

import (
	"encoding/binary"
	"fmt"
	"io"
	"time"
)

// Keep the packager's positive clock private. Android's enhanced player
// starts its presentation at the requested title offset, so a ten-second
// mux origin leaves it waiting for frames that the playlist never describes.
// Overlay only Cluster Timestamp and CRC elements; skip frame payloads and
// preserve byte offsets, lengths and Range responses. Work is bounded by one
// requested segment, with no duplicate media buffer or on-disk rewrite.
type mkvClockPatch struct {
	at   int64
	data []byte
}
type mkvClockReader struct {
	src       io.ReaderAt
	size, pos int64
	patches   []mkvClockPatch
}

func matroskaTitleReader(src io.ReaderAt, size int64) (io.ReadSeeker, error) {
	r := &mkvClockReader{src: src, size: size}
	for off := int64(0); off < size; {
		id, body, n, err := ebmlElement(src, off, size)
		if err != nil || id != mkvCluster {
			return nil, fmt.Errorf("matroska: invalid cluster at %d", off)
		}
		end := size
		if n >= 0 {
			end = body + n
		}
		if end > size || end <= off {
			return nil, io.ErrUnexpectedEOF
		}
		next := end
		var underflow uint64
		for at := body; at < end; {
			cid, cb, cn, err := ebmlElement(src, at, end)
			if err == nil && cid == mkvCluster && n < 0 {
				next = at
				break
			}
			if err != nil || cn < 0 || cb+cn > end {
				return nil, io.ErrUnexpectedEOF
			}
			switch cid {
			case mkvTimestamp:
				ms, err := ebmlUint(src, cb, cn)
				if err != nil {
					return nil, err
				}
				origin := uint64(matroskaOrigin.Milliseconds())
				if ms < origin {
					// Decoder/audio preroll can precede title zero. Cluster
					// timestamps are unsigned; retain that negative time in
					// each Block's signed relative timestamp instead.
					underflow, ms = origin-ms, 0
				} else {
					ms -= origin
				}
				b := make([]byte, cn)
				for i := len(b) - 1; i >= 0; i-- {
					b[i] = byte(ms)
					ms >>= 8
				}
				r.patches = append(r.patches, mkvClockPatch{cb, b})
			case mkvSimpleBlock, mkvBlockGroup:
				if underflow > 0 {
					block, ok := mkvReadBlock(src, cid, cb, cn)
					if !ok {
						return nil, fmt.Errorf("matroska: invalid preroll block")
					}
					rel := int64(block.at/time.Millisecond) - int64(underflow)
					if rel < -32768 || rel > 32767 {
						return nil, fmt.Errorf("matroska: preroll timestamp overflow")
					}
					b := make([]byte, 2)
					binary.BigEndian.PutUint16(b, uint16(int16(rel)))
					r.patches = append(r.patches, mkvClockPatch{block.data - 3, b})
				}
			case mkvCRC32:
				// A same-sized Void replaces the optional CRC, whose protected
				// Timestamp is now different. No encoded packet bytes change.
				r.patches = append(r.patches, mkvClockPatch{at, []byte{0xec}})
			}
			at = cb + cn
		}
		off = next
	}
	return r, nil
}
func (r *mkvClockReader) Read(b []byte) (int, error) {
	if r.pos >= r.size {
		return 0, io.EOF
	}
	if int64(len(b)) > r.size-r.pos {
		b = b[:r.size-r.pos]
	}
	n, err := r.src.ReadAt(b, r.pos)
	for _, p := range r.patches {
		lo, hi := max(r.pos, p.at), min(r.pos+int64(n), p.at+int64(len(p.data)))
		if lo < hi {
			copy(b[lo-r.pos:hi-r.pos], p.data[lo-p.at:hi-p.at])
		}
	}
	r.pos += int64(n)
	return n, err
}
func (r *mkvClockReader) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		off += r.pos
	case io.SeekEnd:
		off += r.size
	default:
		return 0, fmt.Errorf("invalid seek")
	}
	if off < 0 {
		return 0, fmt.Errorf("negative seek")
	}
	r.pos = off
	return off, nil
}
