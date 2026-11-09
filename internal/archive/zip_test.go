package archive

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"testing"
)

func buildStoreZIP(t *testing.T, files map[string][]byte, method uint16) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		hdr := &zip.FileHeader{
			Name:   name,
			Method: method,
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestListZIPStoreAndSplit(t *testing.T) {
	payload := bytes.Repeat([]byte("ether-zip-stream-payload-"), 4000)
	raw := buildStoreZIP(t, map[string][]byte{
		"readme.nfo": []byte("info"),
		"Movie.mkv":  payload,
	}, zip.Store)

	if got := Detect(raw[:8]); got != FormatZIP {
		t.Fatalf("Detect = %v, want FormatZIP", got)
	}

	// Single volume
	vols := []Volume{{R: bytes.NewReader(raw), Size: int64(len(raw))}}
	members, err := ListZIP(vols)
	if err != nil {
		t.Fatalf("ListZIP single: %v", err)
	}
	var movie *Member
	for i := range members {
		if members[i].Name == "Movie.mkv" {
			movie = &members[i]
		}
	}
	if movie == nil || !movie.Stored || movie.Size != int64(len(payload)) {
		t.Fatalf("movie member = %+v", movie)
	}
	mr, err := NewMemberReader(*movie, vols)
	if err != nil {
		t.Fatalf("NewMemberReader: %v", err)
	}
	out := make([]byte, len(payload))
	if n, err := mr.ReadAt(out, 0); err != nil || n != len(payload) || !bytes.Equal(out, payload) {
		t.Fatalf("ReadAt single: n=%d err=%v", n, err)
	}

	// Multi-volume split (.zip.001, .zip.002, .zip.003)
	part := len(raw) / 3
	splitVols := []Volume{
		{R: bytes.NewReader(raw[:part]), Size: int64(part)},
		{R: bytes.NewReader(raw[part : 2*part]), Size: int64(part)},
		{R: bytes.NewReader(raw[2*part:]), Size: int64(len(raw) - 2*part)},
	}
	members2, err := ListZIP(splitVols)
	if err != nil {
		t.Fatalf("ListZIP split: %v", err)
	}
	var movie2 *Member
	for i := range members2 {
		if members2[i].Name == "Movie.mkv" {
			movie2 = &members2[i]
		}
	}
	if movie2 == nil || len(movie2.Extents) != 1 {
		t.Fatalf("split movie2 = %+v", movie2)
	}
	got := make([]byte, len(payload))
	concat := NewMultiReader(splitVols)
	if n, err := concat.ReadAt(got, movie2.Extents[0].Offset); err != nil || n != len(payload) || !bytes.Equal(got, payload) {
		t.Fatalf("ReadAt split: n=%d err=%v", n, err)
	}
}

func TestListZIPCompressedRejected(t *testing.T) {
	payload := bytes.Repeat([]byte("compressible-video-bytes-"), 1000)
	raw := buildStoreZIP(t, map[string][]byte{"Movie.mkv": payload}, zip.Deflate)
	vols := []Volume{{R: bytes.NewReader(raw), Size: int64(len(raw))}}
	members, err := ListZIP(vols)
	if err != nil {
		t.Fatalf("ListZIP: %v", err)
	}
	if len(members) != 1 || members[0].Stored {
		t.Fatalf("expected compressed member, got %+v", members)
	}
	if err := members[0].Streamable(); !errors.Is(err, ErrCompressed) {
		t.Fatalf("Streamable err = %v, want ErrCompressed", err)
	}
}

func TestListZIP64Store(t *testing.T) {
	payload := []byte("zip64-stored-video-stream-content")
	name := "Episode.01.mkv"
	crc := crc32.ChecksumIEEE(payload)

	var buf bytes.Buffer
	// Local file header
	var lfh [30]byte
	copy(lfh[0:4], sigZIPLocal)
	binary.LittleEndian.PutUint16(lfh[4:6], 45)
	binary.LittleEndian.PutUint32(lfh[14:18], crc)
	binary.LittleEndian.PutUint32(lfh[18:22], 0xffffffff)
	binary.LittleEndian.PutUint32(lfh[22:26], 0xffffffff)
	binary.LittleEndian.PutUint16(lfh[26:28], uint16(len(name)))
	binary.LittleEndian.PutUint16(lfh[28:30], 20)
	buf.Write(lfh[:])
	buf.WriteString(name)
	var lfhExtra [20]byte
	binary.LittleEndian.PutUint16(lfhExtra[0:2], 0x0001)
	binary.LittleEndian.PutUint16(lfhExtra[2:4], 16)
	binary.LittleEndian.PutUint64(lfhExtra[4:12], uint64(len(payload)))
	binary.LittleEndian.PutUint64(lfhExtra[12:20], uint64(len(payload)))
	buf.Write(lfhExtra[:])
	dataStart := buf.Len()
	buf.Write(payload)

	// Central directory
	cdOff := buf.Len()
	var cdh [46]byte
	copy(cdh[0:4], sigZIPCD)
	binary.LittleEndian.PutUint16(cdh[4:6], 45)
	binary.LittleEndian.PutUint16(cdh[6:8], 45)
	binary.LittleEndian.PutUint32(cdh[16:20], crc)
	binary.LittleEndian.PutUint32(cdh[20:24], 0xffffffff)
	binary.LittleEndian.PutUint32(cdh[24:28], 0xffffffff)
	binary.LittleEndian.PutUint16(cdh[28:30], uint16(len(name)))
	binary.LittleEndian.PutUint16(cdh[30:32], 28)
	binary.LittleEndian.PutUint32(cdh[42:46], 0xffffffff)
	buf.Write(cdh[:])
	buf.WriteString(name)
	var cdExtra [28]byte
	binary.LittleEndian.PutUint16(cdExtra[0:2], 0x0001)
	binary.LittleEndian.PutUint16(cdExtra[2:4], 24)
	binary.LittleEndian.PutUint64(cdExtra[4:12], uint64(len(payload)))
	binary.LittleEndian.PutUint64(cdExtra[12:20], uint64(len(payload)))
	binary.LittleEndian.PutUint64(cdExtra[20:28], uint64(0))
	buf.Write(cdExtra[:])
	cdSize := buf.Len() - cdOff

	// ZIP64 EOCD
	z64Off := buf.Len()
	var z64 [56]byte
	copy(z64[0:4], sigZIP64EOCD)
	binary.LittleEndian.PutUint64(z64[4:12], 44)
	binary.LittleEndian.PutUint16(z64[12:14], 45)
	binary.LittleEndian.PutUint16(z64[14:16], 45)
	binary.LittleEndian.PutUint64(z64[24:32], 1)
	binary.LittleEndian.PutUint64(z64[32:40], 1)
	binary.LittleEndian.PutUint64(z64[40:48], uint64(cdSize))
	binary.LittleEndian.PutUint64(z64[48:56], uint64(cdOff))
	buf.Write(z64[:])

	// ZIP64 EOCD Locator
	var loc [20]byte
	copy(loc[0:4], sigZIP64Locator)
	binary.LittleEndian.PutUint32(loc[4:8], 0)
	binary.LittleEndian.PutUint64(loc[8:16], uint64(z64Off))
	binary.LittleEndian.PutUint32(loc[16:20], 1)
	buf.Write(loc[:])

	// Standard EOCD
	var eocd [22]byte
	copy(eocd[0:4], sigZIPEOCD)
	binary.LittleEndian.PutUint16(eocd[8:10], 0xffff)
	binary.LittleEndian.PutUint16(eocd[10:12], 0xffff)
	binary.LittleEndian.PutUint32(eocd[12:16], 0xffffffff)
	binary.LittleEndian.PutUint32(eocd[16:20], 0xffffffff)
	buf.Write(eocd[:])

	raw := buf.Bytes()
	vols := []Volume{{R: bytes.NewReader(raw), Size: int64(len(raw))}}
	members, err := ListZIP(vols)
	if err != nil {
		t.Fatalf("ListZIP ZIP64: %v", err)
	}
	if len(members) != 1 || members[0].Name != name || !members[0].Stored {
		t.Fatalf("members = %+v", members)
	}
	if len(members[0].Extents) != 1 || members[0].Extents[0].Offset != int64(dataStart) || members[0].Extents[0].Size != int64(len(payload)) {
		t.Fatalf("extents = %+v, want offset %d size %d", members[0].Extents, dataStart, len(payload))
	}
}
