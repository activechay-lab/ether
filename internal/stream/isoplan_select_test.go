package stream

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/iso"
)

func isoFiles(sizes map[string]int64) []iso.File {
	var out []iso.File
	for p, n := range sizes {
		out = append(out, iso.File{Path: p, Size: n})
	}
	return out
}

func TestIsoFeatureDVDPlaysTheWholeLargestTitleSet(t *testing.T) {
	const gib = 1 << 30
	an := &iso.Analysis{Files: isoFiles(map[string]int64{
		"VIDEO_TS/VIDEO_TS.VOB": 10 << 20, // menu
		"VIDEO_TS/VTS_01_0.VOB": 5 << 20,  // title set 1 menu
		"VIDEO_TS/VTS_01_1.VOB": 100 << 20,
		"VIDEO_TS/VTS_02_0.VOB": 5 << 20,
		"VIDEO_TS/VTS_02_1.VOB": gib,
		"VIDEO_TS/VTS_02_2.VOB": gib,
		"VIDEO_TS/VTS_02_3.VOB": 400 << 20,
		"VIDEO_TS/VTS_02_5.VOB": gib, // part 4 is missing: not contiguous
	})}
	files, name := isoFeature(an, "Movie.iso")
	if name != "Movie.vob" {
		t.Fatalf("name %q", name)
	}
	if len(files) != 3 {
		t.Fatalf("played %d files, want VTS_02_1..3", len(files))
	}
	for i, want := range []string{"VIDEO_TS/VTS_02_1.VOB", "VIDEO_TS/VTS_02_2.VOB", "VIDEO_TS/VTS_02_3.VOB"} {
		if files[i].Path != want {
			t.Fatalf("file %d = %s, want %s", i, files[i].Path, want)
		}
	}
}

func TestJoinableDiscsDropsABonusDisc(t *testing.T) {
	mk := func(ticks int64) *disc {
		return &disc{an: &iso.Analysis{DurationTicks: ticks, MainFeature: []iso.Clip{{File: iso.File{Size: ticks}}}}}
	}
	film, tail, bonus := mk(45000*7000), mk(45000*5000), mk(45000*1200)
	if got := joinableDiscs([]*disc{film, tail}); len(got) != 2 {
		t.Fatalf("a film split across two discs must join, got %d", len(got))
	}
	if got := joinableDiscs([]*disc{film, bonus}); len(got) != 1 || got[0] != film {
		t.Fatalf("a short bonus disc must not join, got %d discs", len(got))
	}
	if got := joinableDiscs([]*disc{film, tail, bonus}); len(got) != 2 {
		t.Fatalf("got %d discs, want film + tail", len(got))
	}
}
