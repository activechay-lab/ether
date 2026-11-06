package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

func TestSpanishReleaseIdentityFromMateriaOscuraTrace(t *testing.T) {
	title := &core.Title{Type: core.TypeEpisode, ShowTitle: "Materia oscura", ShowOriginalTitle: "Dark Matter", ShowYear: 2024,
		Title: "Ama y sé amado", Year: 2026, Season: 2, Episode: 5, Language: "en"}
	for _, tc := range []struct{ name, want string }{
		{"Materia Oscura [4k 2160p][Cap.205]", ""},
		{"Materia Oscura [HDTV][Cap.205]", ""},
		{"Materia Oscura 🍂 S02 🎞️ E05", ""},
		{"Materia Oscura S02E05 [APTV WEB-DL 2160p HEVC DV-HDR10+ ES DD+ Atmos 5.1][HDO].mkv SPANiSH", ""},
		{"Materia Oscura S02E05 [APTV WEB-DL 1080p AVC ES DD+ Atmos 5.1][HDO].mkv SPANiSH", ""},
		{"Materia Oscura [Cap.206]", "season/episode mismatch"},
		{"Materia Oscura [Cap.305]", "season/episode mismatch"},
		{"Materia oscura 2026 S02E05 1080p WEBRip x265 DUAL DD 5.1 SUBS.mkv Spanish", "series year mismatch"},
		{"Materia oscura S02 2160p DV HDR APTV+ WEB-DL ESP-ENG DD+ Atmos 5.1 H.265", "season pack (skipped in v1)"},
		{"Materia Oscura S02E05 Otro episodio APTV WEB-DL 1080p SPANISH", "episode title mismatch"},
		{"Otra Serie S02E05 APTV WEB-DL 1080p SPANISH", "title mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := core.Release{Name: tc.name, Info: releaseparse.Parse(tc.name), Protocol: core.ProtocolHTTP}
			if got := rejectIdentity(r, title, searchRequestFor(title), "es-ES"); got != tc.want {
				t.Fatalf("rejection = %q, want %q", got, tc.want)
			}
		})
	}
}
