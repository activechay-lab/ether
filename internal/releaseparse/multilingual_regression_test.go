package releaseparse

import (
	"slices"
	"testing"
)

func TestChapterNumbersDoNotInventEpisodes(t *testing.T) {
	for _, name := range []string{
		"Show Cap.2019 1080p", "Show Capítulo 2024 1080p",
		"Show Cap.1080 WEB", "Show Cap.720 WEB", "Show Cap.480 WEB",
		"Show Cap.0205 1080p", "Cap.205 2019 1080p",
		"The Cap 205 2019 1080p",
		"[Group] Show - 205 [Cap.205] [1080p]",
		"[Group] Show - 1071 [Cap.1071] [1080p]",
	} {
		info := Parse(name)
		if len(info.Seasons) != 0 || len(info.Episodes) != 0 {
			t.Errorf("%s invented season/episode: %+v", name, info)
		}
	}
}

func TestChapterWordsInsideTitles(t *testing.T) {
	for _, title := range []string{"Cap America", "Captain 205", "The Cap"} {
		name := title + " S02E05 APTV+ WEB-DL 1080p"
		if info := Parse(name); !TitleMatches(info, title) || !EpisodeMatches(info, 2, 5) {
			t.Errorf("title %q changed identity: %+v", title, info)
		}
	}
}

func TestChapterLikeYearInTitleKeepsExplicitEpisode(t *testing.T) {
	info := Parse("Cap 2019 S02E05 APTV WEB-DL 1080p")
	if !EpisodeMatches(info, 2, 5) {
		t.Fatalf("year in title contradicted explicit episode: %+v", info)
	}
}

func TestChapterPackedAndAbsoluteNumberingStaySeparate(t *testing.T) {
	for _, tc := range []struct {
		name            string
		season, episode int
	}{
		{"Show Cap.205 1080p", 2, 5},
		{"Show Capítulo 205 1080p", 2, 5},
		{"Show Cap.1205 1080p", 12, 5},
		{"Show Cap.1005 1080p", 10, 5},
	} {
		info := Parse(tc.name)
		if !EpisodeMatches(info, tc.season, tc.episode) || len(info.AbsoluteEpisodes) != 0 {
			t.Errorf("packed chapter: %s: %+v", tc.name, info)
		}
	}
	for _, name := range []string{"[Group] Show - 205 [Cap.205] [1080p]", "[Group] Show - 205 [1080p]"} {
		info := Parse(name)
		if !EpisodeMatchesAbsolute(info, 205) || EpisodeMatches(info, 2, 5) || info.Title != "Show" {
			t.Errorf("absolute episode: %s: %+v", name, info)
		}
	}
}

func TestAnimeEpisodeNumberIsNotPackedChapter(t *testing.T) {
	info := Parse("Show E205 1080p")
	if !slices.Equal(info.Episodes, []int{205}) || len(info.Seasons) != 0 || EpisodeMatches(info, 2, 5) {
		t.Fatalf("anime episode was packed into a season: %+v", info)
	}
}

func TestShortAudioAndSubtitleCodesRemainSeparate(t *testing.T) {
	info := Parse("Show S02E05 FR AAC SUBS DE JA 1080p")
	if !slices.Equal(info.Languages, []string{"french"}) || !slices.Contains(info.SubtitleLanguages, "german") || !slices.Contains(info.SubtitleLanguages, "japanese") {
		t.Fatalf("audio/subtitle languages mixed: %+v", info)
	}
}
