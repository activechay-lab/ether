package resolver

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestSpanishReleaseIdentityFromMateriaOscuraTrace(t *testing.T) {
	title := &core.Title{Type: core.TypeEpisode, ShowTitle: "Materia oscura", ShowOriginalTitle: "Dark Matter", ShowYear: 2024,
		Title: "Ama y sé amado", Year: 2026, Season: 2, Episode: 5, Language: "en"}
	for _, tc := range []struct{ name, want string }{
		{"Materia Oscura [4k 2160p][Cap.205]", ""},
		{"Materia Oscura [HDTV][Cap.205]", ""},
		{"Materia Oscura Capítulo 205 1080p", ""},
		{"Materia Oscura 2026 Cap.205 1080p", "series year mismatch"},
		{"Materia Oscura 2024 Cap.205 1080p", ""},
		{"Materia Oscura [HDTV][Cap.205][Castellano] [2026]", ""},
		{"Materia Oscura Temporada 2 [HDTV 720p][Cap.05][AC3 5.1 Castellano]", ""},
		{"Materia Oscura Cap.205 Otro episodio APTV WEB-DL 1080p", "episode title mismatch"},
		{"Otra Serie Cap.205 APTV WEB-DL 1080p", `title mismatch (parsed "Otra Serie"; expected "Materia oscura" or "Dark Matter")`},
		{"Materia Oscura 🍂 S02 🎞️ E05", ""},
		{"Materia Oscura S02E05 [APTV WEB-DL 2160p HEVC DV-HDR10+ ES DD+ Atmos 5.1][HDO].mkv SPANiSH", ""},
		{"Materia Oscura S02E05 [APTV WEB-DL 1080p AVC ES DD+ Atmos 5.1][HDO].mkv SPANiSH", ""},
		{"Materia Oscura [Cap.206]", "season/episode mismatch"},
		{"Materia Oscura [Cap.305]", "season/episode mismatch"},
		{"Materia oscura 2026 S02E05 1080p WEBRip x265 DUAL DD 5.1 SUBS.mkv Spanish", "series year mismatch"},
		{"Materia oscura S02 2160p DV HDR APTV+ WEB-DL ESP-ENG DD+ Atmos 5.1 H.265", "season pack (skipped in v1)"},
		{"Materia Oscura S02E05 Otro episodio APTV WEB-DL 1080p SPANISH", "episode title mismatch"},
		{"Otra Serie S02E05 APTV WEB-DL 1080p SPANISH", `title mismatch (parsed "Otra Serie"; expected "Materia oscura" or "Dark Matter")`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := core.Release{Name: tc.name, Info: releaseparse.Parse(tc.name), Protocol: core.ProtocolHTTP}
			if got := rejectIdentity(r, title, searchRequestFor(title), "es-ES"); got != tc.want {
				t.Fatalf("rejection = %q, want %q", got, tc.want)
			}
		})
	}
}

// Issue #360: Spanish NewPCT chapter ranges ("[Cap.101_120]") and trailing
// bracketed/parenthesized years ("[2019]", "(2019)", "[Año 2019]").
func TestSpanishNewPCTChapterPacksAndTrailingYears(t *testing.T) {
	const dbzPack = "Dragon Ball Z – Temporada 1 [HDTV][Cap.101_120][Castellano][www.descargas2020.org][www.pctnew.org].part09"

	dbzEp1 := &core.Title{Type: core.TypeEpisode, ShowTitle: "Dragon Ball Z", ShowYear: 1989, Title: "Mini Goku es un niño mimado", Season: 1, Episode: 1, Language: "ja"}
	dbzEp20 := &core.Title{Type: core.TypeEpisode, ShowTitle: "Dragon Ball Z", ShowYear: 1989, Title: "Episodio 20", Season: 1, Episode: 20, Language: "ja"}
	dbzEp21 := &core.Title{Type: core.TypeEpisode, ShowTitle: "Dragon Ball Z", ShowYear: 1989, Title: "Episodio 21", Season: 1, Episode: 21, Language: "ja"}
	dbOriginal := &core.Title{Type: core.TypeEpisode, ShowTitle: "Dragon Ball", ShowYear: 1986, Title: "El secreto de la esfera del dragón", Season: 1, Episode: 1, Language: "ja"}

	packRel := core.Release{ID: "dbz-pack", Name: dbzPack, Info: releaseparse.Parse(dbzPack), Protocol: core.ProtocolTorrent}
	if got := rejectIdentity(packRel, dbzEp1, searchRequestFor(dbzEp1), "es-ES"); got != "" {
		t.Fatalf("dbz Ep1 rejected: %q (parsed %+v)", got, packRel.Info)
	}
	if got := rejectIdentity(packRel, dbzEp20, searchRequestFor(dbzEp20), "es-ES"); got != "" {
		t.Fatalf("dbz Ep20 rejected: %q", got)
	}
	if got := rejectIdentity(packRel, dbzEp21, searchRequestFor(dbzEp21), "es-ES"); got != "season/episode mismatch" {
		t.Fatalf("dbz Ep21 rejection = %q, want season/episode mismatch", got)
	}
	if got := rejectIdentity(packRel, dbOriginal, searchRequestFor(dbOriginal), "es-ES"); got != `title mismatch (parsed "Dragon Ball Z"; expected "Dragon Ball")` {
		t.Fatalf("Dragon Ball vs Dragon Ball Z rejection = %q", got)
	}

	movie1917 := &core.Title{Key: core.MovieKey(530915), Type: core.TypeMovie, Title: "1917", OriginalTitle: "1917", Year: 2019, Runtime: 119 * time.Minute, Language: "en"}
	movieDolor := &core.Title{Key: core.MovieKey(522938), Type: core.TypeMovie, Title: "Dolor y gloria", OriginalTitle: "Dolor y gloria", Year: 2019, Runtime: 113 * time.Minute, Language: "es"}
	for _, tc := range []struct {
		title *core.Title
		name  string
	}{
		{movieDolor, "Dolor y gloria [BluRay 1080p][AC3 5.1 Castellano] (2019)"},
		{movieDolor, "Dolor y gloria [MicroHD 1080p][AC3 5.1 Español Castellano] [2019]"},
		{movieDolor, "Dolor y gloria [HDRip][AC3 5.1 Castellano][Año 2019]"},
		{movie1917, "1917 [BluRay 1080p][AC3 5.1 Castellano] [2019]"},
	} {
		r := core.Release{ID: "es-movie", Name: tc.name, Info: releaseparse.Parse(tc.name), Protocol: core.ProtocolTorrent}
		if got := rejectIdentity(r, tc.title, searchRequestFor(tc.title), "es-ES"); got != "" {
			t.Fatalf("%q rejected: %q (parsed %+v)", tc.name, got, r.Info)
		}
		if !audioAcceptable(r.Info, []string{"es-ES"}) {
			t.Fatalf("%q Spanish audio not acceptable: %+v", tc.name, r.Info.Languages)
		}
	}
}

func TestAmbiguousChapterNumbersCannotResolveAsPackedEpisodes(t *testing.T) {
	for _, tc := range []struct {
		name            string
		season, episode int
	}{
		{"Show Cap.2019 1080p", 20, 19},
		{"Show Cap.1080 WEB", 10, 80},
		{"Show Cap.720 WEB", 7, 20},
		{"Show Cap.0205 1080p", 2, 5},
		{"[Group] Show - 205 [Cap.205] [1080p]", 2, 5},
	} {
		title := &core.Title{Type: core.TypeEpisode, ShowTitle: "Show", Title: "Episode", Season: tc.season, Episode: tc.episode}
		r := core.Release{Name: tc.name, Info: releaseparse.Parse(tc.name), Protocol: core.ProtocolHTTP}
		if got := rejectIdentity(r, title, searchRequestFor(title), ""); got == "" {
			t.Errorf("ambiguous chapter accepted: %s: %+v", tc.name, r.Info)
		}
	}
}
