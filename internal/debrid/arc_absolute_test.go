package debrid

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestChooseFileArcAbsoluteEpisode covers a TMDB story-arc season that keeps
// absolute episode numbers ("One Piece" S18E749): the target carries both the
// in-season number and the (equal) absolute number, and a pack of fan-sub
// files numbered from the series start must resolve to the wanted episode,
// while a per-season-numbered show is unaffected.
func TestChooseFileArcAbsoluteEpisode(t *testing.T) {
	// S18E749 = absolute 749; prior seasons sum to 748.
	target := &core.ReleaseTarget{Season: 18, Episode: 749, AbsoluteEpisode: 749, ShowYear: 1999, ExpectedIDs: core.IDs{TMDB: 37854}, ReportedIDs: core.IDs{TMDB: 37854}}

	cases := []struct {
		name string
		path string
		want bool
	}{
		{"dash absolute", "One Piece - 749.mkv", true},
		{"fansub bracket", "[SubsPlease] One Piece - 749 (1080p).mkv", true},
		{"bracket dash", "[Group] One Piece - 749 [1080p][HEVC].mkv", true},
		{"season folder absolute", "One Piece/Season 18/One Piece - 749.mkv", true},
		{"SxxExx absolute number", "One Piece S18E749 1080p.mkv", true},
		{"other episode", "One Piece - 748.mkv", false},
		{"other episode high", "One Piece - 780.mkv", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ChooseFile([]File{{Path: tc.path, Size: 1_400_000_000}}, target)
			if tc.want && (err != nil || got.Path != tc.path) {
				t.Fatalf("want %q, got %+v err=%v", tc.path, got, err)
			}
			if !tc.want && err == nil {
				t.Fatalf("path %q must not match S18E749", tc.path)
			}
		})
	}
}

// TestChooseFileArcAbsoluteBatchPack picks the wanted episode out of an
// absolute-numbered batch pack.
func TestChooseFileArcAbsoluteBatchPack(t *testing.T) {
	target := &core.ReleaseTarget{Season: 18, Episode: 749, AbsoluteEpisode: 749, ShowYear: 1999, ExpectedIDs: core.IDs{TMDB: 37854}, ReportedIDs: core.IDs{TMDB: 37854}}
	files := []File{
		{ID: "e747", Path: "[Group] One Piece - 747 (1080p).mkv", Size: 1_300_000_000},
		{ID: "e748", Path: "[Group] One Piece - 748 (1080p).mkv", Size: 1_350_000_000},
		{ID: "e749", Path: "[Group] One Piece - 749 (1080p).mkv", Size: 1_400_000_000},
		{ID: "e750", Path: "[Group] One Piece - 750 (1080p).mkv", Size: 1_450_000_000},
	}
	got, err := ChooseFile(files, target)
	if err != nil || got.ID != "e749" {
		t.Fatalf("want e749, got %+v err=%v", got, err)
	}
}

// TestChooseFileSeasonNumberedUnaffected ensures a normally numbered show's
// per-season file selection is unchanged: the absolute branch never applies
// because each file's number is a per-season index.
func TestChooseFileSeasonNumberedUnaffected(t *testing.T) {
	target := &core.ReleaseTarget{Season: 2, Episode: 3, ShowYear: 2015, ExpectedIDs: core.IDs{TMDB: 999}, ReportedIDs: core.IDs{TMDB: 999}}
	files := []File{
		{ID: "e1", Path: "Show.S02E01.1080p.mkv", Size: 900_000_000},
		{ID: "e3", Path: "Show.S02E03.1080p.mkv", Size: 1_000_000_000},
		{ID: "e4", Path: "Show.S02E04.1080p.mkv", Size: 1_100_000_000},
	}
	got, err := ChooseFile(files, target)
	if err != nil || got.ID != "e3" {
		t.Fatalf("want e3, got %+v err=%v", got, err)
	}
	if chosenFileMatchesTarget("Show.S02E01.1080p.mkv", target) {
		t.Fatal("S02E01 matched S02E03 target")
	}
}
