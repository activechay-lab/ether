package releaseparse

import (
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestAnimeAbsoluteEpisodes(t *testing.T) {
	for _, tc := range []struct {
		name     string
		title    string
		absolute []int
		episodes []int // the season-episode reading, unchanged by absolute parsing
	}{
		{"[SubsPlease] One Piece - 1071 (1080p) [ABCD1234].mkv", "One Piece", []int{1071}, nil},
		{"[Erai-raws] Jujutsu Kaisen - 05 [1080p][Multiple Subtitle][ABCDEF12]", "Jujutsu Kaisen", []int{5}, []int{5}},
		{"[Group] Show - 12v2 (1080p)", "Show", []int{12}, nil},
		{"[Group] Show - 24 END (1080p)", "Show", []int{24}, nil},
		{"Show - 1100 1080p WEB", "Show", []int{1100}, nil},
		{"[Group] Show - 7", "Show", []int{7}, nil},
	} {
		info := Parse(tc.name)
		if info.Title != tc.title || !reflect.DeepEqual(info.AbsoluteEpisodes, tc.absolute) || !reflect.DeepEqual(info.Episodes, tc.episodes) {
			t.Errorf("%s: title=%q absolute=%v episodes=%v, want %q %v %v", tc.name, info.Title, info.AbsoluteEpisodes, info.Episodes, tc.title, tc.absolute, tc.episodes)
		}
		if !EpisodeMatchesAbsolute(info, tc.absolute[0]) {
			t.Errorf("%s: does not match absolute episode %d", tc.name, tc.absolute[0])
		}
	}
}

func TestNumbersThatAreNotAbsoluteEpisodes(t *testing.T) {
	for _, name := range []string{
		"Godzilla - 1954 (1080p)",                         // a year
		"Blade Runner 2049 2017 1080p BluRay x264-SPARKS", // a title number
		"300 2006 1080p BluRay",
		"Ocean's 11 2001 1080p BluRay",
		"Apollo 13 1995 1080p BluRay",
		"1917 2019 1080p BluRay",
		"Show - 01-12 (1080p) [Batch]", // a batch range
		"Show S01E05 - 05 (1080p)",     // a season episode stays one
		"[Judas] Show - S01E05 [1080p]",
		"Movie - Part 2 (1080p)",
		"Movie - 2 Fast 2 Furious 2003 1080p", // words follow the number
	} {
		info := Parse(name)
		if len(info.AbsoluteEpisodes) != 0 {
			t.Errorf("%s: absolute episodes %v, want none", name, info.AbsoluteEpisodes)
		}
	}
	// A numeric movie title never gains season-episode numbers either.
	for _, name := range []string{"300 2006 1080p BluRay", "Godzilla - 1954 (1080p)", "1917 2019 1080p BluRay"} {
		if info := Parse(name); len(info.Episodes) != 0 || len(info.Seasons) != 0 {
			t.Errorf("%s: seasons=%v episodes=%v", name, info.Seasons, info.Episodes)
		}
	}
}

func TestSeasonDashEpisode(t *testing.T) {
	info := Parse("[SubsPlease] Spy x Family Season 2 - 05 (1080p)")
	if !reflect.DeepEqual(info.Seasons, []int{2}) || !reflect.DeepEqual(info.Episodes, []int{5}) || info.FullSeason || len(info.AbsoluteEpisodes) != 0 {
		t.Fatalf("got seasons=%v episodes=%v full=%v absolute=%v", info.Seasons, info.Episodes, info.FullSeason, info.AbsoluteEpisodes)
	}
	info = Parse("[Erai-raws] Kimetsu no Yaiba S3 - 05 [1080p][ABCDEF12].mkv")
	if !reflect.DeepEqual(info.Seasons, []int{3}) || !reflect.DeepEqual(info.Episodes, []int{5}) || info.FullSeason || info.Title != "Kimetsu no Yaiba" {
		t.Fatalf("S3 - 05: title=%q seasons=%v episodes=%v full=%v", info.Title, info.Seasons, info.Episodes, info.FullSeason)
	}
	// A cour ("Part 2") between the season and the number is parsed with Cour=2.
	if info = Parse("[G] Show S3 Part 2 - 03 (1080p)"); !reflect.DeepEqual(info.Seasons, []int{3}) || info.Cour != 2 || !reflect.DeepEqual(info.Episodes, []int{3}) || len(info.AbsoluteEpisodes) != 0 {
		t.Fatalf("S3 Part 2 - 03: got seasons=%v cour=%d episodes=%v absolute=%v", info.Seasons, info.Cour, info.Episodes, info.AbsoluteEpisodes)
	}
	// A season range is still a range.
	info = Parse("Show Seasons 1 - 5 1080p BluRay")
	if !info.FullSeason || len(info.Episodes) != 0 {
		t.Fatalf("range: got seasons=%v episodes=%v full=%v", info.Seasons, info.Episodes, info.FullSeason)
	}
}

// TestAnimeSearchEpisodeArcSeason covers the search episode for a TMDB
// story-arc season that keeps absolute numbering ("One Piece" S18E749): the
// catalogue supplies the absolute number, and it is used as-is. A normally
// numbered later season has no catalogue absolute number, so it is not
// searched by one; season 1 (a short anime) searches the episode itself.
func TestAnimeSearchEpisodeArcSeason(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  core.SearchRequest
		want int
	}{
		{"arc season with catalogue absolute", core.SearchRequest{Anime: true, Type: core.TypeEpisode, Season: 18, Episode: 749, AbsoluteEpisode: 749}, 749},
		{"normally numbered later season", core.SearchRequest{Anime: true, Type: core.TypeEpisode, Season: 2, Episode: 3}, 0},
		{"first season falls back to episode", core.SearchRequest{Anime: true, Type: core.TypeEpisode, Season: 1, Episode: 10}, 10},
		{"non-anime", core.SearchRequest{Type: core.TypeEpisode, Season: 18, Episode: 749}, 0},
	} {
		if got := AnimeSearchEpisode(tc.req); got != tc.want {
			t.Errorf("%s: AnimeSearchEpisode=%d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestEpisodeMatchesAbsolute(t *testing.T) {
	if EpisodeMatchesAbsolute(Parse("[G] Show - 1071 (1080p)"), 1070) {
		t.Error("1071 matched 1070")
	}
	if EpisodeMatchesAbsolute(Parse("Show S01E05 1080p"), 5) {
		t.Error("a season episode is not an absolute number")
	}
	if EpisodeMatchesAbsolute(Parse("[G] Show - 05 (1080p)"), 0) {
		t.Error("unknown absolute number matched")
	}
}
