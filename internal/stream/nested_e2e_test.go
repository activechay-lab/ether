package stream

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/braedonsaunders/ether/internal/archive"
	"github.com/braedonsaunders/ether/internal/archive/archivetest"
	"github.com/braedonsaunders/ether/internal/par2/par2test"
)

func nestedFixture(t *testing.T, seed uint64, size int) (video []byte, outer [][]byte) {
	t.Helper()
	video = mkvBytes(seed, size)
	return video, nestedRAR(t, video, 5, 300_000, 5, 450_000, newStyle("Movie.2024.1080p"))
}

func TestNestedRARPar2Verification(t *testing.T) {
	video, outer := nestedFixture(t, 71, 1_500_000)
	idx := par2Index(16_384, par2test.File{Name: "release.part01.rar", Data: outer[0]})
	post := func(t *testing.T, f *fakeFetcher, first []byte) []byte {
		vols := append([][]byte{first}, outer[1:]...)
		return postVolumes(t, f, 60_000, vols, newStyle("release"),
			postFile{name: "release.par2", data: idx},
			postFile{name: "release.vol000+01.par2", data: randBytes(5, 100_000)})
	}
	t.Run("intact verifies", func(t *testing.T) {
		f := newFakeFetcher()
		doc := post(t, f, outer[0])
		e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
		if ok, err := e.Check(t.Context(), doc); !ok || err != nil {
			t.Fatalf("Check = %v %v", ok, err)
		}
		src := openSource(t, e, "p2", doc)
		checkRandomReads(t, src, video, 5, 40)
		if st := e.Stats(); st.Par2Verified != 1 || st.Par2Mismatches != 0 {
			t.Fatalf("stats %+v", st)
		}
	})
	t.Run("corrupted head rejected", func(t *testing.T) {
		f := newFakeFetcher()
		bad := bytes.Clone(outer[0])
		bad[5000] ^= 0x55 // video bytes inside the first 16 KiB; headers stay valid
		doc := post(t, f, bad)
		e := newTestEngine(t, f, nil, Options{HealthSamples: 4})
		if ok, err := e.Check(t.Context(), doc); ok || !errors.Is(err, ErrPar2Mismatch) {
			t.Fatalf("Check = %v %v", ok, err)
		}
		if _, err := e.Open(t.Context(), "p2bad", doc); !errors.Is(err, ErrPar2Mismatch) {
			t.Fatalf("Open = %v", err)
		}
	})
}

func TestNestedRARMissingData(t *testing.T) {
	video, outer := nestedFixture(t, 72, 1_500_000)
	t.Run("lost article in the video fails the read, not the rest", func(t *testing.T) {
		f := newFakeFetcher()
		doc := postVolumes(t, f, 60_000, outer, newStyle("release"))
		f.setMissing(postID("release.part02.rar", 6))
		e := newTestEngine(t, f, nil, Options{ReadAheadMin: 1, ReadAheadMax: 1})
		src := openSource(t, e, "gap", doc)
		head := make([]byte, 100_000)
		if n, err := src.ReadAt(head, 0); err != nil || n != len(head) || !bytes.Equal(head, video[:n]) {
			t.Fatalf("head: %d %v", n, err)
		}
		all := make([]byte, len(video))
		n, err := src.ReadAt(all, 0)
		if err == nil || !bytes.Equal(all[:min(n, 100_000)], video[:min(n, 100_000)]) {
			t.Fatalf("read across the lost article: n=%d err=%v", n, err)
		}
	})
	t.Run("an outer article missing from the NZB is incomplete", func(t *testing.T) {
		f := newFakeFetcher()
		var files []postFile
		for i, v := range outer {
			pf := postFile{name: fmt.Sprintf("release.part%02d.rar", i+1), data: v}
			if i == 1 {
				pf.omit = []int{3}
			}
			files = append(files, pf)
		}
		doc := post(t, f, 60_000, files...)
		e := newTestEngine(t, f, nil, Options{})
		if _, err := e.Open(t.Context(), "inc", doc); !errors.Is(err, ErrIncomplete) {
			t.Fatalf("Open = %v", err)
		}
	})
	t.Run("a missing inner volume is not reported as no video", func(t *testing.T) {
		inner := innerRARVolumes(t, video, 300_000)
		var files []archivetest.RARFile
		for i, v := range inner {
			if i == 2 {
				continue
			}
			files = append(files, archivetest.RARFile{Name: fmt.Sprintf("Movie.part%02d.rar", i+1), Data: v})
		}
		o := buildRAR(t, files, archivetest.RAROptions{Version: 5, VolumeSize: 450_000, NewNumbering: true})
		f := newFakeFetcher()
		doc := postVolumes(t, f, 60_000, o, newStyle("release"))
		e := newTestEngine(t, f, nil, Options{})
		_, err := e.Open(t.Context(), "innergap", doc)
		if !errors.Is(err, ErrIncomplete) {
			t.Fatalf("Open = %v", err)
		}
	})
}

func TestNestedRAREncryptedWrongPassword(t *testing.T) {
	video := mkvBytes(73, 900_000)
	inner := innerRARVolumes(t, video, 300_000)
	var ms []encMember
	for i, v := range inner {
		ms = append(ms, encMember{name: fmt.Sprintf("Movie.part%02d.rar", i+1), data: v})
	}
	f := newFakeFetcher()
	raw := postVolumes(t, f, 40_000, encryptedRAR5(t, "right", ms), newStyle("release"))
	e := newTestEngine(t, f, nil, Options{})
	_, err := e.Open(t.Context(), "wrong", passwordNZB(raw, "wrong"))
	// The data-only encryption has no password check: the RAR signature check
	// of the decrypted inner volume must catch it.
	if err == nil {
		t.Fatal("a wrong password opened the nested archive")
	}
	if _, err := e.Open(t.Context(), "none", raw); !errors.Is(err, archive.ErrPasswordRequired) {
		t.Fatalf("no password: %v", err)
	}
}

func TestNestedRARDiscImage(t *testing.T) {
	image, feature := bluray(81)
	inner := buildRAR(t, []archivetest.RARFile{{Name: "Disc.iso", Data: image}},
		archivetest.RAROptions{Version: 5, VolumeSize: 600_000, NewNumbering: true})
	var files []archivetest.RARFile
	for i, v := range inner {
		files = append(files, archivetest.RARFile{Name: fmt.Sprintf("Disc.part%02d.rar", i+1), Data: v})
	}
	outer := buildRAR(t, files, archivetest.RAROptions{Version: 5, VolumeSize: 700_000, NewNumbering: true})
	f := newFakeFetcher()
	doc := postVolumes(t, f, 50_000, outer, newStyle("release"))
	e := newTestEngine(t, f, nil, Options{ReadAheadMin: 1, ReadAheadMax: 1})
	src := openSource(t, e, "nested-iso", doc)
	if src.Size() != int64(len(feature)) {
		t.Fatalf("plays %q (%d bytes), want the %d byte main feature", src.Name(), src.Size(), len(feature))
	}
	checkRandomReads(t, src, feature, 9, 80)
}

func TestNestedRARCacheAndPrefetch(t *testing.T) {
	video, outer := nestedFixture(t, 74, 1_500_000)
	f := newFakeFetcher()
	doc := postVolumes(t, f, 60_000, outer, newStyle("release"))
	cache := openCache(t, 64<<20)
	e := newTestEngine(t, f, cache, Options{ReadAheadMin: 1, ReadAheadMax: 1})
	if err := e.Prefetch(t.Context(), "pf", doc, 500_000, 400_000); err != nil {
		t.Fatalf("Prefetch: %v", err)
	}
	src := openSource(t, e, "pf", doc)
	checkRandomReads(t, src, video, 11, 80)
	if got := readAll(t, src, 31<<10); !bytes.Equal(got, video) {
		t.Fatal("sequential mismatch")
	}
	// A second engine on the same disc cache plays it without the network.
	src.Close()
	e.Close()
	f.resetCounts()
	e2 := newTestEngine(t, f, cache, Options{ReadAheadMin: 1, ReadAheadMax: 1})
	src2 := openSource(t, e2, "pf", doc)
	checkRandomReads(t, src2, video, 13, 40)
}

func TestNestedRARSeasonEpisodeHint(t *testing.T) {
	// A season held in one nested archive: the wanted episode is chosen from
	// the inner members, not the largest.
	ep1, ep2 := mkvBytes(91, 700_000), mkvBytes(92, 600_000)
	inner := buildRAR(t, []archivetest.RARFile{
		{Name: "Show.S01E01.1080p.mkv", Data: ep1},
		{Name: "Show.S01E02.1080p.mkv", Data: ep2},
	}, archivetest.RAROptions{Version: 5, VolumeSize: 400_000, NewNumbering: true})
	var files []archivetest.RARFile
	for i, v := range inner {
		files = append(files, archivetest.RARFile{Name: fmt.Sprintf("Show.S01.part%02d.rar", i+1), Data: v})
	}
	outer := buildRAR(t, files, archivetest.RAROptions{Version: 5, VolumeSize: 500_000, NewNumbering: true})
	f := newFakeFetcher()
	doc := postVolumes(t, f, 50_000, outer, newStyle("release"))
	src := openSource(t, newTestEngine(t, f, nil, Options{}), "season", doc)
	if src.Name() != "Show.S01E01.1080p.mkv" || src.Size() != int64(len(ep1)) {
		t.Fatalf("plays %q (%d bytes)", src.Name(), src.Size())
	}
	checkRandomReads(t, src, ep1, 17, 40)
}
