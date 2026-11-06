package releaseparse

import (
	"slices"
	"testing"
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
		{"Show S02 [Cap.205]", 2, 5},
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

func TestSpanishEpisodeMarkersDoNotInventIdentity(t *testing.T) {
	for _, name := range []string{
		"Show 205 2160p", "Show [Cap.20]", "Show [Cap.200]", "Show [Cap.00205]",
		"Show [Cap.2050p]", "Show S02E06 [Cap.205]", "Show S02E05 [Cap.206]",
		"Show S02E05 [Cap.305]", "Show S02 A Different Episode E05",
		"Show S03 [Cap.205]", "Show Temporada 3 [Cap.205]",
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
	} {
		if !EpisodeTitleMatches(name, "Ama y sé amado") {
			t.Errorf("technical tags became an episode title: %s", name)
		}
		info := Parse(name)
		if !slices.Contains(info.Languages, "spanish") {
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
