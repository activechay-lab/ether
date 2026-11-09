package iso

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
	"time"
)

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// iso9660Record builds one directory record.
func iso9660Record(name string, lba, size uint32, dir bool) []byte {
	rec := make([]byte, 33+len(name))
	rec[0] = byte(len(rec))
	binary.LittleEndian.PutUint32(rec[2:], lba)
	binary.LittleEndian.PutUint32(rec[10:], size)
	if dir {
		rec[25] = 0x02
	}
	rec[32] = byte(len(name))
	copy(rec[33:], name)
	return rec
}

// loopingISO9660 is a PVD plus a root directory (sector 17) that lists itself.
func loopingISO9660() []byte {
	img := make([]byte, 18*iso9660SectorSize)
	pvd := img[16*iso9660SectorSize:]
	pvd[0] = 1
	copy(pvd[1:6], "CD001")
	binary.LittleEndian.PutUint32(pvd[156+2:], 17)
	binary.LittleEndian.PutUint32(pvd[156+10:], iso9660SectorSize)
	copy(img[17*iso9660SectorSize:], iso9660Record("LOOP", 17, iso9660SectorSize, true))
	return img
}

func TestISO9660DirectoryLoopTerminates(t *testing.T) {
	img := loopingISO9660()
	files, err := ListISOFiles(testCtx(t), bytes.NewReader(img))
	if err != nil {
		t.Fatalf("ListISOFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %v, want none", files)
	}
}

// udfInlineDir returns a File Entry sector (tag 261) whose inline data holds
// the given FIDs.
func udfInlineDir(fids ...[]byte) []byte {
	sec := make([]byte, iso9660SectorSize)
	binary.LittleEndian.PutUint16(sec[0:], 261)
	sec[34] = 3 // inline
	var data []byte
	for _, f := range fids {
		data = append(data, f...)
	}
	binary.LittleEndian.PutUint32(sec[172:], uint32(len(data)))
	copy(sec[176:], data)
	return sec
}

func udfDirFID(name string, block uint32) []byte {
	fid := make([]byte, 40) // 38 header + 2 (comp id + one char), 4-aligned
	binary.LittleEndian.PutUint16(fid[0:], 257)
	fid[18] = 0x02 // directory
	fid[19] = 2
	binary.LittleEndian.PutUint32(fid[24:], block)
	fid[38], fid[39] = 8, name[0]
	return fid
}

func TestUDFDirectoryLoopTerminates(t *testing.T) {
	img := make([]byte, 12*iso9660SectorSize)
	// Directory at block 10 lists itself, and block 11 which lists block 10.
	copy(img[10*iso9660SectorSize:], udfInlineDir(udfDirFID("A", 10), udfDirFID("B", 11)))
	copy(img[11*iso9660SectorSize:], udfInlineDir(udfDirFID("C", 10)))
	root := udfLongAD{loc: udfLBA{block: 10}}
	files, err := udfWalkAll(testCtx(t), bytes.NewReader(img), root, nil, 0, "")
	if err != nil {
		t.Fatalf("udfWalkAll: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("files = %v, want none", files)
	}
}

func TestUDFAllocAreaClampsHostileLengths(t *testing.T) {
	for _, tag := range []uint16{261, 266} {
		buf := make([]byte, iso9660SectorSize)
		off := 168
		if tag == 266 {
			off = 208
		}
		for _, ea := range []uint32{0, 2000, 2048, 0xFFFFFFF0, 0xFFFFFFFF} {
			for _, ad := range []uint32{0, 100, 0xFFFFFFFF} {
				binary.LittleEndian.PutUint32(buf[off:], ea)
				binary.LittleEndian.PutUint32(buf[off+4:], ad)
				if area := udfAllocArea(tag, buf); len(area) > len(buf) {
					t.Fatalf("tag %d ea %d ad %d: area %d bytes", tag, ea, ad, len(area))
				}
			}
		}
	}
	// And through the readers that used to slice with the raw offsets.
	img := make([]byte, 11*iso9660SectorSize)
	sec := img[10*iso9660SectorSize:]
	binary.LittleEndian.PutUint16(sec[0:], 261)
	binary.LittleEndian.PutUint32(sec[168:], 0xFFFFFFF0)
	binary.LittleEndian.PutUint32(sec[172:], 64)
	for _, alloc := range []byte{0, 1, 3} {
		sec[34] = alloc
		if _, err := udfReadDirEntries(testCtx(t), bytes.NewReader(img), 10, nil, 0); err != nil {
			t.Fatalf("alloc %d: %v", alloc, err)
		}
	}
}

func TestISO9660ListDirShortAndHugeRecords(t *testing.T) {
	// A record shorter than 34 bytes at the very end of the directory.
	dir := make([]byte, iso9660SectorSize)
	good := iso9660Record("A.MKV;1", 20, 100, false)
	copy(dir, good)
	dir[len(good)] = 5 // recLen 5: too short to hold a name length byte
	dir[iso9660SectorSize-3] = 3
	img := make([]byte, 11*iso9660SectorSize)
	copy(img[10*iso9660SectorSize:], dir)
	entries, err := iso9660ListDir(bytes.NewReader(img), 10, iso9660SectorSize)
	if err != nil || len(entries) != 1 || entries[0].name != "A.MKV" {
		t.Fatalf("entries %+v err %v", entries, err)
	}
	// A directory size from the image is capped, not allocated.
	if _, err := iso9660ListDir(bytes.NewReader(img), 10, 1<<32-1); err == nil {
		t.Fatal("expected an error for a 4 GiB directory")
	}
}

func TestUDFParseVDSPartitionMapAtSectorEnd(t *testing.T) {
	img := make([]byte, 3*iso9660SectorSize)
	lvd := img[:iso9660SectorSize]
	binary.LittleEndian.PutUint16(lvd[0:], 6)
	binary.LittleEndian.PutUint32(lvd[264:], 1<<30) // clamped to the sector
	// Chain of partition maps whose last one ends exactly at byte 2047, so
	// the next read would be buf[2047+1].
	off := 440
	for _, l := range []int{255, 255, 255, 255, 255, 255, 77} {
		lvd[off+1] = byte(l)
		off += l
	}
	if off != 2047 {
		t.Fatalf("fixture ends at %d", off)
	}
	binary.LittleEndian.PutUint16(img[2*iso9660SectorSize:], 8)
	if _, _, _, err := udfParseVDS(bytes.NewReader(img), udfExtent{length: 3 * iso9660SectorSize, sector: 0}); err != nil {
		t.Fatal(err)
	}
}

func TestReadPlaylistsCoalescedBoundsHostileExtents(t *testing.T) {
	rs := bytes.NewReader(make([]byte, 64*iso9660SectorSize))
	huge := isoFileEntry{path: "BDMV/PLAYLIST/00000.mpls", size: 1 << 62, extents: []isoExtent{{lba: 1, length: 1 << 30}}}
	liar := isoFileEntry{path: "BDMV/PLAYLIST/00001.mpls", size: 1 << 62, extents: []isoExtent{{lba: 2, length: 512}}}
	got := readPlaylistsCoalesced(rs, []isoFileEntry{huge, liar})
	if _, ok := got[huge.path]; ok {
		t.Fatal("a 1 GiB playlist extent must be skipped")
	}
	if len(got[liar.path]) != 512 {
		t.Fatalf("a small playlist with a lying size must still read, got %d bytes", len(got[liar.path]))
	}
}

func TestReadPlaylistsCoalescedTotalCap(t *testing.T) {
	rs := bytes.NewReader(make([]byte, 64*iso9660SectorSize))
	var entries []isoFileEntry
	for i := range 40 { // 40 × 512 KiB declared via extents: well past the total cap
		entries = append(entries, isoFileEntry{
			path:    "BDMV/PLAYLIST/" + string(rune('A'+i%26)) + string(rune('a'+i/26)) + ".mpls",
			size:    512 << 10,
			extents: []isoExtent{{lba: 1, length: 512 << 10}},
		})
	}
	if got := readPlaylistsCoalesced(rs, entries); len(got)*(512<<10) > maxPlaylistTotalBytes {
		t.Fatalf("read %d playlists, total cap is %d bytes", len(got), maxPlaylistTotalBytes)
	}
}

func TestBudgetReaderStopsReading(t *testing.T) {
	b := newBudgetReader(bytes.NewReader(make([]byte, 100)), 10)
	n, err := io.ReadFull(b, make([]byte, 4))
	if n != 4 || err != nil {
		t.Fatalf("first read: %d %v", n, err)
	}
	if _, err := io.ReadFull(b, make([]byte, 20)); !errors.Is(err, errReadBudget) {
		t.Fatalf("over-budget read err = %v, want errReadBudget", err)
	}
}

// A chain of Allocation Extent Descriptors that points back at itself must end.
func TestCollectFileExtentsLoopingAED(t *testing.T) {
	img := make([]byte, 12*iso9660SectorSize)
	aed := img[10*iso9660SectorSize:]
	binary.LittleEndian.PutUint16(aed[0:], 258)
	binary.LittleEndian.PutUint32(aed[20:], 16)
	// Its only AD is a continuation (type 3) pointing at its own sector.
	binary.LittleEndian.PutUint32(aed[24:], 3<<30|2048)
	binary.LittleEndian.PutUint32(aed[28:], 10)

	inline := make([]byte, 16)
	binary.LittleEndian.PutUint32(inline[0:], 3<<30|2048)
	binary.LittleEndian.PutUint32(inline[4:], 10)

	done := make(chan []isoExtent, 1)
	go func() {
		done <- collectFileExtents(testCtx(t), bytes.NewReader(img), inline, 1, nil, 0, 1<<20, 0)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("looping AED chain did not terminate")
	}
}
