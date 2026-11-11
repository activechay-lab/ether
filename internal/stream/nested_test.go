package stream

import (
	"archive/zip"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/archive/archivetest"
)

// nestedRAR builds a store-mode inner multi-volume RAR holding video, then
// packs every inner volume as a member of an outer multi-volume RAR.
func nestedRAR(t testing.TB, video []byte, innerVersion, innerVol int, outerVersion int, outerVol int, innerName func(int) string) [][]byte {
	t.Helper()
	inner := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2024.1080p.mkv", Data: video}},
		archivetest.RAROptions{Version: innerVersion, VolumeSize: int64(innerVol), NewNumbering: true})
	var files []archivetest.RARFile
	for i, v := range inner {
		files = append(files, archivetest.RARFile{Name: innerName(i), Data: v})
	}
	return buildRAR(t, files, archivetest.RAROptions{Version: outerVersion, VolumeSize: int64(outerVol), NewNumbering: true})
}

func TestNestedRARStreams(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		innerVer, innerVol       int
		outerVer, outerVol       int
		innerNaming, outerNaming func(string) func(int) string
	}{
		{"rar5-in-rar5", 5, 300_000, 5, 500_000, newStyle, newStyle},
		{"rar4-in-rar4-old", 4, 300_000, 4, 450_000, oldStyle, oldStyle},
		{"rar5-in-rar4", 5, 250_000, 4, 700_000, newStyle, newStyle},
		{"single-outer", 5, 400_000, 5, 0, newStyle, newStyle},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFetcher()
			video := mkvBytes(31, 2_000_000)
			outer := nestedRAR(t, video, tc.innerVer, tc.innerVol, tc.outerVer, tc.outerVol, tc.innerNaming("Movie.2024.1080p"))
			if tc.outerVol > 0 && len(outer) < 3 {
				t.Fatalf("only %d outer volumes", len(outer))
			}
			doc := postVolumes(t, f, 70_001, outer, tc.outerNaming("release"))
			e := newTestEngine(t, f, nil, Options{})
			src := openSource(t, e, "nested", doc)
			if src.Name() != "Movie.2024.1080p.mkv" || src.Size() != int64(len(video)) {
				t.Fatalf("name %q size %d", src.Name(), src.Size())
			}
			checkRandomReads(t, src, video, 17, 200)
			if got := readAll(t, src, 32<<10); !bytes.Equal(got, video) {
				t.Fatal("sequential mismatch")
			}
		})
	}
}

func TestNestedRARCompressedInnerRejected(t *testing.T) {
	f := newFakeFetcher()
	inner := buildRAR(t, []archivetest.RARFile{{Name: "m.mkv", Data: mkvBytes(1, 100_000), Method: 3}},
		archivetest.RAROptions{Version: 5})
	outer := buildRAR(t, []archivetest.RARFile{{Name: "m.rar", Data: inner[0]}}, archivetest.RAROptions{Version: 5})
	doc := postVolumes(t, f, 70_001, outer, newStyle("release"))
	e := newTestEngine(t, f, nil, Options{})
	if _, err := e.Open(t.Context(), "c", doc); !errors.Is(err, ErrCompressed) {
		t.Fatalf("Open err = %v", err)
	}
}

type encMember struct {
	name string
	data []byte
}

// encryptedRAR5 builds a multi-volume, data-encrypted (headers in clear) RAR5
// set: volume 1 holds the start of members[0], volume 2 its end and all of
// members[1], and each further member fills a volume of its own.
func encryptedRAR5(t testing.TB, password string, members []encMember) [][]byte {
	t.Helper()
	type piece struct {
		m                       encMember
		plain                   []byte
		part                    []byte
		salt, iv, key           []byte
		before, after, crcWhole bool
	}
	var pieces [][]piece
	for i, m := range members {
		salt := randBytes(uint64(100+i), 16)
		iv := randBytes(uint64(200+i), 16)
		key, err := pbkdf2.Key(sha256.New, password, salt, 1<<4, 32)
		if err != nil {
			t.Fatal(err)
		}
		blk, _ := aes.NewCipher(key)
		ct := make([]byte, (len(m.data)+15)&^15)
		copy(ct, m.data)
		cipher.NewCBCEncrypter(blk, iv).CryptBlocks(ct, ct)
		mk := func(part []byte, before, after bool) piece {
			return piece{m: m, plain: m.data, part: part, salt: salt, iv: iv, key: key, before: before, after: after}
		}
		if i == 0 {
			cut := 4096 * (len(ct) / 4096 / 2)
			pieces = append(pieces, []piece{mk(ct[:cut], false, true)}, []piece{mk(ct[cut:], true, false)})
		} else if i == 1 {
			pieces[1] = append(pieces[1], mk(ct, false, false))
		} else {
			pieces = append(pieces, []piece{mk(ct, false, false)})
		}
	}
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}
	var vols [][]byte
	for vi, ps := range pieces {
		out := cat(archivetest.RAR5Signature, archivetest.RAR5MainHeader(true, vi, false))
		for pi, p := range ps {
			sum := crc32.ChecksumIEEE(p.plain)
			if p.after {
				sum = crc32.ChecksumIEEE(p.part)
			}
			var crc [4]byte
			binary.LittleEndian.PutUint32(crc[:], sum)
			fields := cat(archivetest.Vint(4), archivetest.Vint(uint64(len(p.plain))), archivetest.Vint(0x20), crc[:],
				archivetest.Vint(0), archivetest.Vint(1), archivetest.Vint(uint64(len(p.m.name))), []byte(p.m.name))
			rec := archivetest.RAR5Record(1, cat(archivetest.Vint(0), archivetest.Vint(0), []byte{4}, p.salt, p.iv))
			var flags uint64
			if p.before {
				flags |= 0x8
			}
			if p.after {
				flags |= 0x10
			}
			_ = pi
			out = cat(out, archivetest.RAR5Block(2, flags, fields, rec, true, int64(len(p.part))), p.part)
		}
		vols = append(vols, cat(out, archivetest.RAR5EndHeader(vi < len(pieces)-1)))
	}
	return vols
}

func TestNestedRAREncryptedOuter(t *testing.T) {
	const password = "Sëcret"
	f := newFakeFetcher()
	video := mkvBytes(41, 1_500_000)
	inner := buildRAR(t, []archivetest.RARFile{{Name: "Movie.mkv", Data: video}},
		archivetest.RAROptions{Version: 5, VolumeSize: 400_000, NewNumbering: true})
	if len(inner) < 3 {
		t.Fatalf("only %d inner volumes", len(inner))
	}
	var ms []encMember
	for i, v := range inner {
		ms = append(ms, encMember{name: fmt.Sprintf("Movie.part%02d.rar", i+1), data: v})
	}
	outer := encryptedRAR5(t, password, ms)
	doc := passwordNZB(postVolumes(t, f, 30_011, outer, newStyle("release")), password)
	e := newTestEngine(t, f, nil, Options{ReadAheadMin: 1, ReadAheadMax: 1})
	src := openSource(t, e, "nested-enc", doc)
	if src.Name() != "Movie.mkv" || src.Size() != int64(len(video)) {
		t.Fatalf("name %q size %d", src.Name(), src.Size())
	}
	checkRandomReads(t, src, video, 23, 150)
	if got := readAll(t, src, 31<<10); !bytes.Equal(got, video) {
		t.Fatal("sequential mismatch")
	}
	noPass := postVolumes(t, newFakeFetcher(), 30_011, outer, newStyle("release"))
	if _, err := e.Open(t.Context(), "nested-nopass", noPass); !errors.Is(err, archive.ErrPasswordRequired) {
		t.Fatalf("no password: %v", err)
	}
}

func innerRARVolumes(t testing.TB, video []byte, volSize int64) [][]byte {
	t.Helper()
	return buildRAR(t, []archivetest.RARFile{{Name: "Movie.2024.1080p.mkv", Data: video}},
		archivetest.RAROptions{Version: 5, VolumeSize: volSize, NewNumbering: true})
}

func checkNested(t *testing.T, e *Engine, id string, doc, video []byte) {
	t.Helper()
	src := openSource(t, e, id, doc)
	if src.Name() != "Movie.2024.1080p.mkv" || src.Size() != int64(len(video)) {
		t.Fatalf("name %q size %d", src.Name(), src.Size())
	}
	checkRandomReads(t, src, video, 29, 150)
	if got := readAll(t, src, 32<<10); !bytes.Equal(got, video) {
		t.Fatal("sequential mismatch")
	}
}

func TestNestedRARInside7zAndZIP(t *testing.T) {
	video := mkvBytes(51, 1_800_000)
	inner := innerRARVolumes(t, video, 350_000)
	var files []archivetest.SevenZipFile
	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	for i, v := range inner {
		name := fmt.Sprintf("Movie.part%02d.rar", i+1)
		files = append(files, archivetest.SevenZipFile{Name: name, Data: v})
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(v)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	t.Run("7z", func(t *testing.T) {
		f := newFakeFetcher()
		vols := cut(archivetest.Build7zCopy(files, false), 500_000)
		doc := postVolumes(t, f, 60_000, vols, func(i int) string { return fmt.Sprintf("release.7z.%03d", i+1) })
		checkNested(t, newTestEngine(t, f, nil, Options{}), "n7z", doc, video)
	})
	t.Run("zip", func(t *testing.T) {
		f := newFakeFetcher()
		vols := cut(zbuf.Bytes(), 500_000)
		doc := postVolumes(t, f, 60_000, vols, func(i int) string {
			if i == len(vols)-1 {
				return "release.zip"
			}
			return fmt.Sprintf("release.z%02d", i+1)
		})
		checkNested(t, newTestEngine(t, f, nil, Options{}), "nzip", doc, video)
	})
}

func TestNestedRARObfuscatedInnerNames(t *testing.T) {
	video := mkvBytes(52, 1_500_000)
	for _, tc := range []struct {
		name   string
		volume int64
	}{{"single-inner-volume", 0}, {"multi-inner-volume", 400_000}} {
		t.Run(tc.name, func(t *testing.T) {
			inner := innerRARVolumes(t, video, tc.volume)
			var files []archivetest.RARFile
			for i, v := range inner {
				files = append(files, archivetest.RARFile{Name: fmt.Sprintf("a%07x9e", 0x3f2a1c+i*7919), Data: v})
			}
			outer := buildRAR(t, files, archivetest.RAROptions{Version: 5, VolumeSize: 600_000, NewNumbering: true})
			f := newFakeFetcher()
			doc := postVolumes(t, f, 60_000, outer, newStyle("release"))
			checkNested(t, newTestEngine(t, f, nil, Options{}), "obf", doc, video)
		})
	}
}

// A plain obfuscated video is still played as itself, not taken for a RAR set.
func TestObfuscatedVideoNotTakenForNestedRAR(t *testing.T) {
	video := mkvBytes(54, 800_000)
	outer := buildRAR(t, []archivetest.RARFile{{Name: "x93kd02jf", Data: video}}, archivetest.RAROptions{Version: 5, VolumeSize: 300_000})
	f := newFakeFetcher()
	doc := postVolumes(t, f, 60_000, outer, newStyle("release"))
	src := openSource(t, newTestEngine(t, f, nil, Options{}), "anonvid", doc)
	if src.Size() != int64(len(video)) {
		t.Fatalf("size %d", src.Size())
	}
	checkRandomReads(t, src, video, 3, 50)
}
