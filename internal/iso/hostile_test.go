package iso

import (
	"bytes"
	"context"
	"encoding/binary"
	"testing"

	"github.com/braedonsaunders/ether/internal/iso/isotest"
)

// An image comes from an untrusted release. Whatever its descriptors say,
// reading it must end, stay within a small memory budget and never panic:
// a stack overflow from a directory loop kills the whole server.
func TestHostileImagesAreBounded(t *testing.T) {
	ctx := context.Background()
	sector := func() []byte { return make([]byte, iso9660SectorSize) }

	// ISO 9660: the root lists itself as a subdirectory, then a record too
	// short to hold its header at the end of the sector.
	pvd, root := sector(), sector()
	pvd[0] = 1
	copy(pvd[1:6], "CD001")
	binary.LittleEndian.PutUint32(pvd[156+2:], 20)
	binary.LittleEndian.PutUint32(pvd[156+10:], iso9660SectorSize)
	root[0] = 36
	binary.LittleEndian.PutUint32(root[2:], 20)
	binary.LittleEndian.PutUint32(root[10:], iso9660SectorSize)
	root[25], root[32], root[33] = 0x02, 1, 'a'
	root[iso9660SectorSize-8] = 4
	if _, err := ListISOFiles(ctx, makeImage(t, map[uint32][]byte{16: pvd, 20: root})); err != nil {
		t.Logf("loop: %v", err)
	}

	// ISO 9660: a root directory claiming 4 GiB of records.
	binary.LittleEndian.PutUint32(pvd[156+10:], 0xFFFFFFFF)
	if _, err := ListISOFiles(ctx, makeImage(t, map[uint32][]byte{16: pvd, 20: root})); err == nil {
		t.Fatal("a 4 GiB directory was read")
	}

	// UDF: a directory whose only entry is itself.
	fe := sector()
	binary.LittleEndian.PutUint16(fe[0:], 261)
	fe[34] = 3 // inline
	fid := fe[176:]
	binary.LittleEndian.PutUint16(fid[0:], 257)
	fid[18], fid[19] = 0x02, 2
	binary.LittleEndian.PutUint32(fid[24:], 30) // icb block
	fid[38], fid[39] = 8, 'a'
	binary.LittleEndian.PutUint32(fe[172:], 40)
	img := makeImage(t, map[uint32][]byte{30: fe})
	if _, err := udfWalkAll(ctx, img, udfLongAD{loc: udfLBA{block: 30}}, nil, 0, ""); err != nil {
		t.Logf("udf loop: %v", err)
	}

	// UDF: extended attributes running past the sector.
	binary.LittleEndian.PutUint32(fe[168:], 5000)
	if _, err := udfReadDirEntries(ctx, makeImage(t, map[uint32][]byte{30: fe}), 30, nil, 0); err != nil {
		t.Logf("udf ea: %v", err)
	}

	// A playlist claiming an enormous size is not read.
	huge := isoFileEntry{path: "BDMV/PLAYLIST/00000.MPLS", size: 1 << 62, extents: []isoExtent{{lba: 1, length: 1 << 40}}}
	clip := mkEntry("BDMV/STREAM/00001.M2TS", 2, 4096)
	if mf := ResolveMainFeature(ctx, makeImage(t, map[uint32][]byte{3: sector()}), []isoFileEntry{huge, clip}, nil); mf != nil {
		t.Fatalf("main feature from an oversized playlist: %+v", mf)
	}
}

// FuzzAnalyze runs its seeds in every test run; `go test -fuzz` explores.
func FuzzAnalyze(f *testing.F) {
	f.Add(isotest.Build("DISC", []isotest.File{
		{Path: "BDMV/PLAYLIST/00000.mpls", Data: isotest.MPLS([]isotest.PlayItem{{Clip: "00001", In: 0, Out: 450000}})},
		{Path: "BDMV/STREAM/00001.m2ts", Data: bytes.Repeat([]byte{1}, 3000)},
		{Path: "VIDEO_TS/VTS_01_1.VOB", Data: bytes.Repeat([]byte{2}, 3000)},
	}))
	f.Fuzz(func(t *testing.T, img []byte) {
		_, _ = Analyze(context.Background(), bytes.NewReader(img), int64(len(img)), nil)
	})
}
