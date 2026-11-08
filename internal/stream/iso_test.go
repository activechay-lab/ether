package stream

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/iso/isotest"
)

// bluray builds a disc image whose main feature is two clips, plus a short
// bonus playlist, and returns it with the main feature's bytes.
func bluray(seed uint64) (image, feature []byte) {
	c1, c2 := randBytes(seed, 900_000), randBytes(seed+1, 700_000)
	bonus := randBytes(seed+2, 100_000)
	image = isotest.Build("DISC", []isotest.File{
		{Path: "BDMV/PLAYLIST/00000.mpls", Data: isotest.MPLS([]isotest.PlayItem{{Clip: "00001", In: 0, Out: 450000}, {Clip: "00002", In: 0, Out: 270000}})},
		{Path: "BDMV/PLAYLIST/00001.mpls", Data: isotest.MPLS([]isotest.PlayItem{{Clip: "00003", In: 0, Out: 45000}})},
		{Path: "BDMV/STREAM/00001.m2ts", Data: c1},
		{Path: "BDMV/STREAM/00002.m2ts", Data: c2},
		{Path: "BDMV/STREAM/00003.m2ts", Data: bonus},
	})
	return image, append(append([]byte{}, c1...), c2...)
}

func TestISOMainFeature(t *testing.T) {
	image, feature := bluray(1)
	for _, tc := range []struct {
		name string
		post func(t *testing.T, f *fakeFetcher) []byte
	}{
		{"plain iso file", func(t *testing.T, f *fakeFetcher) []byte {
			return post(t, f, 60_000, postFile{name: "Movie.2024.iso", data: image})
		}},
		{"split iso", func(t *testing.T, f *fakeFetcher) []byte {
			return postVolumes(t, f, 60_000, cut(image, 500_000), func(i int) string { return fmt.Sprintf("Movie.2024.iso.%03d", i+1) })
		}},
		{"iso in a RAR set", func(t *testing.T, f *fakeFetcher) []byte {
			vols := buildRAR(t, []archivetest.RARFile{{Name: "Movie.2024.iso", Data: image}}, archivetest.RAROptions{Version: 5, VolumeSize: 400_000})
			return postVolumes(t, f, 60_000, vols, newStyle("movie"))
		}},
		{"iso in a 7z set", func(t *testing.T, f *fakeFetcher) []byte {
			arc := archivetest.Build7zCopy([]archivetest.SevenZipFile{{Name: "Movie.2024.iso", Data: image}}, false)
			return postVolumes(t, f, 60_000, cut(arc, 500_000), func(i int) string { return fmt.Sprintf("Movie.2024.7z.%03d", i+1) })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFetcher()
			doc := tc.post(t, f)
			e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
			src := openSource(t, e, "iso", doc)
			if src.Name() != "Movie.2024.m2ts" || src.Size() != int64(len(feature)) {
				t.Fatalf("plays %q (%d bytes), want Movie.2024.m2ts (%d bytes)", src.Name(), src.Size(), len(feature))
			}
			if got := readAll(t, src, 64<<10); !bytes.Equal(got, feature) {
				t.Fatal("main feature bytes differ from the playlist's clips")
			}
			checkRandomReads(t, src, feature, 7, 60)
			if ok, err := e.Check(t.Context(), doc); !ok || err != nil {
				t.Fatalf("Check = %v %v", ok, err)
			}
		})
	}
}

func TestISOPlainDiscPlaysLargestVideo(t *testing.T) {
	movie := mkvBytes(5, 800_000)
	image := isotest.Build("PLAIN", []isotest.File{
		{Path: "Movie.2024.mkv", Data: movie},
		{Path: "Extras/Sample/sample.mkv", Data: mkvBytes(6, 60_000)},
		{Path: "readme.txt", Data: []byte("hello")},
	})
	f := newFakeFetcher()
	doc := post(t, f, 60_000, postFile{name: "disc.iso", data: image})
	e := newTestEngine(t, f, nil, Options{})
	src := openSource(t, e, "iso", doc)
	if src.Name() != "Movie.2024.mkv" || !bytes.Equal(readAll(t, src, 64<<10), movie) {
		t.Fatalf("plays %q", src.Name())
	}
}

func TestISORejections(t *testing.T) {
	t.Run("no video on the disc", func(t *testing.T) {
		image := isotest.Build("DATA", []isotest.File{{Path: "setup.exe", Data: randBytes(1, 50_000)}})
		f := newFakeFetcher()
		doc := post(t, f, 60_000, postFile{name: "data.iso", data: image})
		e := newTestEngine(t, f, nil, Options{})
		if _, err := e.Open(t.Context(), "iso", doc); !errors.Is(err, ErrNoPlayableFile) {
			t.Fatalf("Open = %v, want ErrNoPlayableFile", err)
		}
	})
	t.Run("not a disc image", func(t *testing.T) {
		f := newFakeFetcher()
		doc := post(t, f, 60_000, postFile{name: "broken.iso", data: randBytes(2, 200_000)})
		e := newTestEngine(t, f, nil, Options{})
		if _, err := e.Open(t.Context(), "iso", doc); !errors.Is(err, ErrNoPlayableFile) {
			t.Fatalf("Open = %v, want ErrNoPlayableFile", err)
		}
	})
}

// blurayDisc builds a disc of one clip, with label and playlist clip name.
func blurayDisc(label string, seed uint64, size int) (image, clip []byte) {
	clip = randBytes(seed, size)
	image = isotest.Build(label, []isotest.File{
		{Path: "BDMV/PLAYLIST/00000.mpls", Data: isotest.MPLS([]isotest.PlayItem{{Clip: "00001", In: 0, Out: 450000}})},
		{Path: "BDMV/STREAM/00001.m2ts", Data: clip},
	})
	return image, clip
}

func TestISOMultiDiscMainFeature(t *testing.T) {
	d1, c1 := blurayDisc("GREAT_MOVIE_DISC_1", 11, 700_000)
	d2, c2 := blurayDisc("GREAT_MOVIE_DISC_2", 12, 500_000)
	want := append(append([]byte{}, c1...), c2...)
	for _, tc := range []struct {
		name string
		post func(t *testing.T, f *fakeFetcher) []byte
	}{
		{"two iso files", func(t *testing.T, f *fakeFetcher) []byte {
			// Posted disc 2 first: the label's disc number orders them.
			return post(t, f, 60_000, postFile{name: "b.iso", data: d2}, postFile{name: "a.iso", data: d1})
		}},
		{"two RAR sets", func(t *testing.T, f *fakeFetcher) []byte {
			var files []postFile
			for i, d := range [][]byte{d1, d2} {
				vols := buildRAR(t, []archivetest.RARFile{{Name: fmt.Sprintf("disc%d.iso", i+1), Data: d}}, archivetest.RAROptions{Version: 5, VolumeSize: 400_000})
				for k, v := range vols {
					files = append(files, postFile{name: fmt.Sprintf("set%d.part%02d.rar", i+1, k+1), data: v})
				}
			}
			return post(t, f, 60_000, files...)
		}},
		{"two iso members of one RAR set", func(t *testing.T, f *fakeFetcher) []byte {
			vols := buildRAR(t, []archivetest.RARFile{{Name: "disc1.iso", Data: d1}, {Name: "disc2.iso", Data: d2}}, archivetest.RAROptions{Version: 5, VolumeSize: 400_000})
			return postVolumes(t, f, 60_000, vols, newStyle("movie"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFetcher()
			doc := tc.post(t, f)
			e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
			src := openSource(t, e, "iso", doc)
			if src.Name() != "great_movie.m2ts" || src.Size() != int64(len(want)) {
				t.Fatalf("plays %q (%d bytes), want great_movie.m2ts (%d bytes)", src.Name(), src.Size(), len(want))
			}
			if got := readAll(t, src, 64<<10); !bytes.Equal(got, want) {
				t.Fatal("discs were not joined in order")
			}
		})
	}
}

func TestISODifferentTitlesAreNotJoined(t *testing.T) {
	d1, c1 := blurayDisc("FIRST_MOVIE", 21, 600_000)
	d2, _ := blurayDisc("SECOND_MOVIE", 22, 300_000)
	f := newFakeFetcher()
	doc := post(t, f, 60_000, postFile{name: "a.iso", data: d1}, postFile{name: "b.iso", data: d2})
	e := newTestEngine(t, f, nil, Options{})
	src := openSource(t, e, "iso", doc)
	if src.Size() != int64(len(c1)) {
		t.Fatalf("plays %d bytes, want only the larger disc's %d", src.Size(), len(c1))
	}
}

// A disc image inside a password-protected archive plays its main feature,
// decrypted a range at a time; the image is never extracted.
func TestISOInEncryptedArchive(t *testing.T) {
	packed := readFixture(t, "iso-crypto/disc.7z")
	want := readFixture(t, "iso-crypto/feature.bin")
	for _, tc := range []struct {
		name string
		vols [][]byte
	}{{"single volume", [][]byte{packed}}, {"multi volume", cut(packed, 17_003)}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeFetcher()
			raw := postVolumes(t, f, 997, tc.vols, func(i int) string { return fmt.Sprintf("disc.7z.%03d", i+1) })
			doc := passwordNZB(raw, "Secret")
			e := newTestEngine(t, f, nil, Options{ReadAheadMin: 1, ReadAheadMax: 1})
			if ok, err := e.Check(t.Context(), doc); err != nil || !ok {
				t.Fatalf("Check: %v %v", ok, err)
			}
			if _, err := e.Open(t.Context(), "iso-nopass", raw); !errors.Is(err, archive.ErrPasswordRequired) {
				t.Fatalf("Open without the password = %v", err)
			}
			src := openSource(t, e, "iso-crypto", doc)
			if src.Name() != "disc.m2ts" || src.Size() != int64(len(want)) {
				t.Fatalf("plays %q (%d bytes), want disc.m2ts (%d bytes)", src.Name(), src.Size(), len(want))
			}
			checkRandomReads(t, src, want, 17, 100)
			if got := readAll(t, src, 4001); !bytes.Equal(got, want) {
				t.Fatal("decrypted main feature differs")
			}
		})
	}
	t.Run("wrong password", func(t *testing.T) {
		f := newFakeFetcher()
		raw := postVolumes(t, f, 997, [][]byte{packed}, func(i int) string { return "disc.7z.001" })
		e := newTestEngine(t, f, nil, Options{})
		if _, err := e.Open(t.Context(), "iso-bad", passwordNZB(raw, "wrong")); err == nil {
			t.Fatal("a wrong password opened the disc")
		}
	})
}
