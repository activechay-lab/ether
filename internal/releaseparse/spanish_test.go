package releaseparse

import (
	"reflect"
	"slices"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestSpanishEpisodeMarkers(t *testing.T) {
	for _, tc := range []struct {
		name            string
		season, episode int
	}{
		{"Materia Oscura [4k 2160p][Cap.205]", 2, 5},
		{"Materia Oscura [HDTV][Cap.205]", 2, 5},
		{"Show [Cap.105]", 1, 5},
		{"Show [Cap.1205]", 12, 5},
		{"Show [Capítulo 205]", 2, 5},
		{"Show Temporada 2 [Cap.205]", 2, 5},
		{"Show Temporada 2 [HDTV 720p][Cap.05][AC3 5.1 Castellano]", 2, 5},
		{"Show S02 [Cap.205]", 2, 5},
		{"Show [HDTV][Cap.101][Castellano] [2024]", 1, 1},
		{"Show [Cap.101][AC3 5.1 Castellano] (2024)", 1, 1},
		{"Materia Oscura 🍂 S02 🎞️ E05", 2, 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			info := Parse(tc.name)
			if !EpisodeMatches(info, tc.season, tc.episode) || info.FullSeason {
				t.Fatalf("episode identity: %+v", info)
			}
			if EpisodeMatches(info, tc.season, tc.episode+1) || EpisodeMatches(info, tc.season+1, tc.episode) {
				t.Fatalf("matched a different episode: %+v", info)
			}
		})
	}
}

func TestSpanishChapterPackRangesAndNewPCTTrailingYear(t *testing.T) {
	for _, tc := range []struct {
		name              string
		title             string
		year              int
		height            int
		source            core.Source
		seasons, episodes []int
		pack              bool
		lang              string
	}{
		{
			name:     "Dragon Ball Z – Temporada 1 [HDTV][Cap.101_120][Castellano][www.descargas2020.org][www.pctnew.org].part09",
			title:    "Dragon Ball Z",
			source:   core.SourceHDTV,
			seasons:  []int{1},
			episodes: episodeRange(1, 20),
			pack:     true,
			lang:     LanguageSpanishSpain,
		},
		{
			name:     "Dragon Ball – Temporada 1 [HDTV][Cap.101-120][Castellano] [1986]",
			title:    "Dragon Ball",
			year:     1986,
			source:   core.SourceHDTV,
			seasons:  []int{1},
			episodes: episodeRange(1, 20),
			pack:     true,
			lang:     LanguageSpanishSpain,
		},
		{
			name:     "Serie Española Temporada 2 [HDTV 720p][Cap.201 al 208][AC3 5.1 Español Castellano]",
			title:    "Serie Española",
			height:   720,
			source:   core.SourceHDTV,
			seasons:  []int{2},
			episodes: episodeRange(1, 8),
			pack:     true,
			lang:     LanguageSpanishSpain,
		},
		{
			name:     "Serie Española Temporada 1 [HDTV][Cap.01_10][Castellano]",
			title:    "Serie Española",
			source:   core.SourceHDTV,
			seasons:  []int{1},
			episodes: episodeRange(1, 10),
			pack:     true,
			lang:     LanguageSpanishSpain,
		},
		{
			name:   "Pelicula [BluRay 1080p][AC3 5.1 Castellano] (2019)",
			title:  "Pelicula",
			year:   2019,
			height: 1080,
			source: core.SourceBluRay,
			lang:   LanguageSpanishSpain,
		},
		{
			name:   "Pelicula [MicroHD 1080p][AC3 5.1 Español Castellano] [2019]",
			title:  "Pelicula",
			year:   2019,
			height: 1080,
			source: core.SourceBluRay,
			lang:   LanguageSpanishSpain,
		},
		{
			name:   "Pelicula [HDRip][AC3 5.1 Castellano] [2021]",
			title:  "Pelicula",
			year:   2021,
			source: core.SourceWEBRip,
			lang:   LanguageSpanishSpain,
		},
		{
			name:   "Pelicula [WEB][Spanish] [2022]",
			title:  "Pelicula",
			year:   2022,
			source: core.SourceWEBDL,
			lang:   "spanish",
		},
		{
			name:   "Pelicula [4KFullUHD 2160p][AC3 5.1 Castellano][Año 2020]",
			title:  "Pelicula",
			year:   2020,
			height: 2160,
			source: core.SourceBluRay,
			lang:   LanguageSpanishSpain,
		},
		{
			name:   "1917 [BluRay 1080p][AC3 5.1 Castellano] [2019]",
			title:  "1917",
			year:   2019,
			height: 1080,
			source: core.SourceBluRay,
			lang:   LanguageSpanishSpain,
		},
		{
			name:   "Blade Runner 2049 [BDRemux 1080p][AC3 5.1 Castellano] (2017)",
			title:  "Blade Runner 2049",
			year:   2017,
			height: 1080,
			source: core.SourceRemux,
			lang:   LanguageSpanishSpain,
		},
		{
			name:   "[www.descargas2020.org] Dolor y Gloria [BluRay 1080p][AC3 5.1 Castellano] [2019]",
			title:  "Dolor y Gloria",
			year:   2019,
			height: 1080,
			source: core.SourceBluRay,
			lang:   LanguageSpanishSpain,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.name)
			if got.Title != tc.title || got.Year != tc.year || got.Height != tc.height || got.Source != tc.source ||
				!reflect.DeepEqual(got.Seasons, tc.seasons) || !reflect.DeepEqual(got.Episodes, tc.episodes) || got.FullSeason != tc.pack {
				t.Fatalf("got title=%q year=%d height=%d source=%q seasons=%v episodes=%v pack=%v; want title=%q year=%d height=%d source=%q seasons=%v episodes=%v pack=%v",
					got.Title, got.Year, got.Height, got.Source, got.Seasons, got.Episodes, got.FullSeason,
					tc.title, tc.year, tc.height, tc.source, tc.seasons, tc.episodes, tc.pack)
			}
			if tc.lang != "" && !slices.Contains(got.Languages, tc.lang) {
				t.Fatalf("languages = %v, want %q", got.Languages, tc.lang)
			}
		})
	}
}

func TestSpanishEpisodeMarkersDoNotInventIdentity(t *testing.T) {
	for _, name := range []string{
		"Show 205 2160p", "Show [Cap.20]", "Show [Cap.200]", "Show [Cap.00205]",
		"Show [Cap.2050p]", "Show S02E06 [Cap.205]", "Show S02E05 [Cap.206]",
		"Show S02E05 [Cap.305]", "Show S02 A Different Episode E05",
		"Show S03 [Cap.205]", "Show Temporada 3 [Cap.205]", "The Cap 205 2019",
	} {
		if info := Parse(name); EpisodeMatches(info, 2, 5) {
			t.Errorf("%s incorrectly matched S02E05: %+v", name, info)
		}
	}
}

func TestSpanishReleaseTechnicalTags(t *testing.T) {
	for _, name := range []string{
		"Materia Oscura S02E05 [APTV WEB-DL 2160p HEVC DV-HDR10+ ES DD+ Atmos 5.1][HDO].mkv SPANiSH",
		"Materia Oscura S02E05 [APTV WEB-DL 1080p AVC ES DD+ Atmos 5.1][HDO].mkv SPANiSH",
		"Materia Oscura S02E05 [APTV WEB-DL 2160p HEVC ES DD+ Atmos 5.1][HDO].mkv",
		"Materia Oscura S02E05 [APTV+ WEB-DL 2160p HEVC ES DD+ Atmos 5.1][HDO].mkv",
		"Materia Oscura [HDTV][Cap.205][Castellano][SUBT] [2026]",
	} {
		if !EpisodeTitleMatches(name, "Ama y sé amado") {
			t.Errorf("technical tags became an episode title: %s", name)
		}
		info := Parse(name)
		if !slices.Contains(info.Languages, "spanish") && !slices.Contains(info.Languages, LanguageSpanishSpain) {
			t.Errorf("Spanish audio was not recognized: %+v", info)
		}
	}
	if EpisodeTitleMatches("Materia Oscura S02E05 Otro episodio APTV WEB-DL 1080p", "Ama y sé amado") {
		t.Error("a different episode title was accepted")
	}
	for _, name := range []string{
		"Es Por Tu Bien 2017 1080p WEB-DL", "Show S02E05 ES 1080p WEB-DL",
		"Show S02E05 SUBS ES DD+ 5.1 1080p", "Show S02E05 ES SUBS DD+ 5.1 1080p",
	} {
		if info := Parse(name); slices.Contains(info.Languages, "spanish") {
			t.Errorf("title, country or subtitle tag became Spanish audio: %s", name)
		}
	}
}
