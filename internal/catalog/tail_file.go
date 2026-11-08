package catalog

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unsafe"
)

// A tail index file is the index's flat arrays, back to back, so a loader
// memory-maps the file and slices it without copying or decoding:
//
//	header (64 bytes): magic "ETHTAIL1", version, byte-order mark,
//	  section count, export day, entry count, CRC-32 of the body
//	section table: per section, its offset and length in bytes
//	body: the sections, each 8-byte aligned
//
// Files are written once under a temporary name in the index directory,
// fsynced, verified and renamed to tail-<day>-<seq>.idx; they are never
// modified afterwards. A loader verifies the header, the table and the
// body CRC before using a file, and a file that fails is deleted.

const (
	tailMagic     = "ETHTAIL1"
	tailVersion   = 1
	tailBOM       = 0x01020304
	tailHeaderLen = 64
)

var errTailFile = errors.New("catalog: tail index file invalid")

// tailSections lists the index's arrays in file order. Each entry is a
// pointer to a slice or string field, so the writer and the loader walk
// the same list.
func (t *tailIndex) sections() []any {
	ix := t.ix
	if ix.lx == nil {
		ix.lx = &searchLexicon{}
	}
	lx := ix.lx
	return []any{
		&ix.keys, &ix.kinds, &ix.pops, &ix.votes, &ix.years,
		&ix.nameAt, &ix.nameOff, &ix.nameBlob, &ix.postAt, &ix.posts,
		&ix.joinOff, &ix.joinTitle, &ix.joinBlob,
		&lx.blob, &lx.offs, &lx.runes, &lx.docs, &lx.gramKeys, &lx.gramAt, &lx.posts,
		&t.sorted, &t.sortAt, &t.titleAt, &t.titleBlob, &t.origAt, &t.origBlob,
		&t.posterAt, &t.posterBlob, &t.released, &t.flags,
	}
}

func rawBytes(p any) []byte {
	switch v := p.(type) {
	case *[]int64:
		return sliceBytes(*v)
	case *[]uint64:
		return sliceBytes(*v)
	case *[]uint32:
		return sliceBytes(*v)
	case *[]int32:
		return sliceBytes(*v)
	case *[]float32:
		return sliceBytes(*v)
	case *[]uint16:
		return sliceBytes(*v)
	case *[]uint8:
		return *v
	case *string:
		return unsafe.Slice(unsafe.StringData(*v), len(*v))
	}
	panic(fmt.Sprintf("catalog: tail section %T", p))
}

func sliceBytes[T any](s []T) []byte {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), len(s)*int(unsafe.Sizeof(s[0])))
}

func bytesAs[T any](b []byte) ([]T, error) {
	var zero T
	size := int(unsafe.Sizeof(zero))
	if len(b) == 0 {
		return nil, nil
	}
	if len(b)%size != 0 || uintptr(unsafe.Pointer(&b[0]))%uintptr(unsafe.Alignof(zero)) != 0 {
		return nil, errTailFile
	}
	return unsafe.Slice((*T)(unsafe.Pointer(&b[0])), len(b)/size), nil
}

// setRaw points section p at b (memory the caller keeps alive).
func setRaw(p any, b []byte) error {
	var err error
	switch v := p.(type) {
	case *[]int64:
		*v, err = bytesAs[int64](b)
	case *[]uint64:
		*v, err = bytesAs[uint64](b)
	case *[]uint32:
		*v, err = bytesAs[uint32](b)
	case *[]int32:
		*v, err = bytesAs[int32](b)
	case *[]float32:
		*v, err = bytesAs[float32](b)
	case *[]uint16:
		*v, err = bytesAs[uint16](b)
	case *[]uint8:
		*v = b
	case *string:
		if len(b) > 0 {
			*v = unsafe.String(&b[0], len(b))
		} else {
			*v = ""
		}
	default:
		return errTailFile
	}
	return err
}

func align8(n int64) int64 { return (n + 7) &^ 7 }

// writeTailFile writes t to dir as tail-<day>-<seq>.idx and returns the path.
func writeTailFile(dir string, t *tailIndex, seq int) (string, error) {
	secs := t.sections()
	table := make([]byte, 16*len(secs))
	off := align8(int64(tailHeaderLen + len(table)))
	bodyStart := off
	for i, s := range secs {
		n := int64(len(rawBytes(s)))
		binary.LittleEndian.PutUint64(table[16*i:], uint64(off))
		binary.LittleEndian.PutUint64(table[16*i+8:], uint64(n))
		off = align8(off + n)
	}
	f, err := os.CreateTemp(dir, ".tail-build-*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	ok := false
	defer func() {
		if !ok {
			f.Close()
			_ = os.Remove(tmp)
		}
	}()
	// Body first (after a placeholder header), CRC on the way.
	crc := crc32.NewIEEE()
	if _, err := f.Seek(bodyStart, io.SeekStart); err != nil {
		return "", err
	}
	pos := bodyStart
	var pad [8]byte
	for _, s := range secs {
		b := rawBytes(s)
		if _, err := f.Write(b); err != nil {
			return "", err
		}
		crc.Write(b)
		pos += int64(len(b))
		if p := align8(pos) - pos; p > 0 {
			f.Write(pad[:p])
			crc.Write(pad[:p])
			pos += p
		}
	}
	head := make([]byte, tailHeaderLen)
	copy(head, tailMagic)
	binary.LittleEndian.PutUint32(head[8:], tailVersion)
	binary.LittleEndian.PutUint32(head[12:], tailBOM)
	binary.LittleEndian.PutUint32(head[16:], uint32(len(secs)))
	binary.LittleEndian.PutUint64(head[24:], uint64(t.day.Unix()))
	binary.LittleEndian.PutUint64(head[32:], uint64(t.len()))
	binary.LittleEndian.PutUint32(head[40:], crc.Sum32())
	binary.LittleEndian.PutUint64(head[48:], uint64(pos))
	if _, err := f.WriteAt(head, 0); err != nil {
		return "", err
	}
	if _, err := f.WriteAt(table, tailHeaderLen); err != nil {
		return "", err
	}
	if err := f.Sync(); err != nil {
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	final := filepath.Join(dir, tailFileName(t.day, seq))
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	ok = true
	return final, nil
}

func tailFileName(day time.Time, seq int) string {
	return fmt.Sprintf("tail-%s-%03d.idx", day.UTC().Format("2006-01-02"), seq)
}

// parseTailFile checks data (a whole file) and slices a tailIndex out of
// it. The index aliases data: data must stay mapped while it is used.
func parseTailFile(data []byte) (*tailIndex, error) {
	if len(data) < tailHeaderLen || string(data[:8]) != tailMagic {
		return nil, errTailFile
	}
	if binary.LittleEndian.Uint32(data[8:]) != tailVersion {
		return nil, fmt.Errorf("%w: version %d", errTailFile, binary.LittleEndian.Uint32(data[8:]))
	}
	// The arrays are host-order; a file from another byte order is rebuilt.
	if binary.NativeEndian.Uint32(data[12:]) != tailBOM {
		return nil, fmt.Errorf("%w: byte order", errTailFile)
	}
	t := &tailIndex{ix: &titleIndex{lx: &searchLexicon{}}}
	secs := t.sections()
	if int(binary.LittleEndian.Uint32(data[16:])) != len(secs) {
		return nil, fmt.Errorf("%w: sections", errTailFile)
	}
	end := binary.LittleEndian.Uint64(data[48:])
	tableEnd := tailHeaderLen + 16*len(secs)
	if end > uint64(len(data)) || uint64(tableEnd) > end {
		return nil, fmt.Errorf("%w: truncated", errTailFile)
	}
	bodyStart := align8(int64(tableEnd))
	if crc32.ChecksumIEEE(data[bodyStart:end]) != binary.LittleEndian.Uint32(data[40:]) {
		return nil, fmt.Errorf("%w: checksum", errTailFile)
	}
	for i, s := range secs {
		off := binary.LittleEndian.Uint64(data[tailHeaderLen+16*i:])
		n := binary.LittleEndian.Uint64(data[tailHeaderLen+16*i+8:])
		if off < uint64(bodyStart) || off+n > end {
			return nil, fmt.Errorf("%w: section %d", errTailFile, i)
		}
		if err := setRaw(s, data[off:off+n]); err != nil {
			return nil, fmt.Errorf("%w: section %d", err, i)
		}
	}
	t.day = time.Unix(int64(binary.LittleEndian.Uint64(data[24:])), 0).UTC()
	n := int(binary.LittleEndian.Uint64(data[32:]))
	if err := t.check(n); err != nil {
		return nil, err
	}
	return t, nil
}

// check verifies the arrays agree in length, so no lookup can index past
// a section of a damaged file the CRC did not catch.
func (t *tailIndex) check(n int) error {
	ix := t.ix
	per := []int{len(ix.keys), len(ix.kinds), len(ix.pops), len(ix.votes), len(ix.years), len(t.sorted), len(t.sortAt), len(t.released), len(t.flags)}
	for _, l := range per {
		if l != n {
			return fmt.Errorf("%w: entry arrays", errTailFile)
		}
	}
	for _, at := range []struct {
		offs []uint32
		blob int
	}{{t.titleAt, len(t.titleBlob)}, {t.origAt, len(t.origBlob)}, {t.posterAt, len(t.posterBlob)}} {
		if len(at.offs) != n+1 || (n >= 0 && len(at.offs) > 0 && int(at.offs[n]) > at.blob) {
			return fmt.Errorf("%w: blob offsets", errTailFile)
		}
	}
	for _, i := range t.sortAt {
		if int(i) >= n {
			return fmt.Errorf("%w: key table", errTailFile)
		}
	}
	return nil
}

// bytes is the index's size in memory or on disk.
func (t *tailIndex) bytes() int64 {
	var n int64
	for _, s := range t.sections() {
		n += int64(len(rawBytes(s)))
	}
	return n
}

// openTailFile maps path and parses it. close unmaps (it does not delete).
func openTailFile(path string) (*tailIndex, func() error, error) {
	data, unmap, err := mapFile(path)
	if err != nil {
		return nil, nil, err
	}
	t, err := parseTailFile(data)
	if err != nil {
		_ = unmap()
		return nil, nil, err
	}
	return t, unmap, nil
}

// tailFiles lists the index files in dir, newest first.
func tailFiles(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "tail-*.idx"))
	// Names sort by day then sequence.
	slices.Sort(matches)
	slices.Reverse(matches)
	return matches
}

func isTailTemp(name string) bool { return strings.HasPrefix(name, ".tail-build-") }
