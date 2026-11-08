package stream

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

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
