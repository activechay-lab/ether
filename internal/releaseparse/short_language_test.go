package releaseparse

import (
	"slices"
	"testing"
)

func TestAudioLanguageCodesUseSharedLanguageNames(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{"ES", "spanish"}, {"EN", "english"}, {"FR", "french"}, {"DE", "german"},
		{"IT", "italian"}, {"PT", "portuguese"}, {"JA", "japanese"}, {"KO", "korean"},
		{"RU", "russian"}, {"UK", "ukrainian"}, {"HI", "hindi"}, {"AR", "arabic"},
		{"DA", "danish"}, {"NO", "norwegian"}, {"SV", "swedish"}, {"NL", "dutch"},
		{"RO", "romanian"}, {"CA", "catalan"}, {"ZH", "chinese"},
		{"PT-BR", "portuguese-brazil"}, {"ES-MX", "spanish-latino"}, {"FR-CA", "french"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			for _, codec := range []string{"DD+ Atmos 5.1", "DDP5.1", "AAC", "DTS-HD", "OPUS"} {
				name := "Show S02E05 " + tc.code + " " + codec + " 1080p"
				info := Parse(name)
				if !slices.Contains(info.Languages, tc.want) {
					t.Errorf("%s %s: audio = %v, want %s", tc.code, codec, info.Languages, tc.want)
				}
				if !EpisodeTitleMatches(name, "The Return") {
					t.Errorf("audio language became episode title: %s", name)
				}
			}
		})
	}
	info := Parse("Show S02E05 FR AAC EN AAC 1080p")
	if !slices.Contains(info.Languages, "french") || !slices.Contains(info.Languages, "english") {
		t.Fatalf("multiple audio tracks lost: %v", info.Languages)
	}
}

func TestShortLanguageCodesKeepSubtitlesAndTitleWordsSeparate(t *testing.T) {
	for _, tc := range []struct{ code, want string }{
		{"ES", "spanish"}, {"FR", "french"}, {"DE", "german"}, {"IT", "italian"},
		{"JA", "japanese"}, {"AR", "arabic"}, {"PT-BR", "portuguese-brazil"},
	} {
		for _, tags := range []string{"SUBS " + tc.code, tc.code + " SUBS", "SUBS " + tc.code + " AAC"} {
			info := Parse("Show S02E05 1080p " + tags)
			if slices.Contains(info.Languages, tc.want) || !slices.Contains(info.SubtitleLanguages, tc.want) {
				t.Errorf("%s: audio=%v subtitles=%v", tags, info.Languages, info.SubtitleLanguages)
			}
		}
	}
	for _, name := range []string{
		"No Time To Die 2021 1080p AAC", "It 2017 1080p AAC", "En Famille S01E01 1080p AAC",
		"Show S02E05 FR 1080p AAC", "Show S02E05 US AAC 1080p", "Show S02E05 XX AAC 1080p",
	} {
		if info := Parse(name); len(info.Languages) != 0 {
			t.Errorf("ambiguous or unknown code became audio: %s: %v", name, info.Languages)
		}
	}
}

func TestDecoratedEpisodeAndProviderTagsAreLanguageIndependent(t *testing.T) {
	for _, tc := range []struct{ name, title string }{
		{"Le Spectacle", "Le retour"}, {"Die Serie", "Die Rückkehr"},
		{"Il Racconto", "Il ritorno"}, {"物語", "帰還"},
	} {
		name := tc.name + " 🍂 S02 🎞️ E05 APTV+ WEB-DL 1080p"
		info := Parse(name)
		if !TitleMatches(info, tc.name) || !EpisodeMatches(info, 2, 5) || !EpisodeTitleMatches(name, tc.title) {
			t.Errorf("language-independent episode naming failed: %s: %+v", name, info)
		}
	}
}
