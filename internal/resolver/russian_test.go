package resolver

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/releaseparse"
)

// Identity must use the show's original title while preserving episode, season,
// year and protocol restrictions, including explicit coverage of partial packs.
func TestRussianOriginalTitleIdentity(t *testing.T) {
	title := &core.Title{Type: core.TypeEpisode, Title: "Episode 1", ShowTitle: "Doctor Kracsnov", ShowOriginalTitle: "Доктор Краснов"}
	req := core.SearchRequest{Type: core.TypeEpisode, Title: title.ShowTitle, Year: 2023, Season: 1, Episode: 1}
	for _, tc := range []struct {
		name            string
		episode, season int
		protocol, want  string
	}{
		{"01. Доктор Краснов.2023.WEB-DL 1080p.Files-x", 1, 1, core.ProtocolTorrent, ""},
		{"01.Доктор Краснов.(2023).WEBRip.(AVC).by.Серый1779.Files-x", 1, 1, core.ProtocolTorrent, ""},
		{"Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK", 1, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 1 из 16 [2023 WEBRip-AVC]", 1, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 16 из 16 [2023 WEBRip 1080p]", 1, 1, core.ProtocolTorrent, "season/episode mismatch"},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", 1, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip 1080p]", 1, 2, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip-AVC]", 16, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 3-5 из 16 [2023 WEBRip]", 1, 1, core.ProtocolTorrent, "season/episode mismatch"},
		{"Доктор Краснов / Сезон: 1 / Серии: 3-5 из 16 [2023 WEBRip]", 4, 1, core.ProtocolTorrent, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", 1, 2, core.ProtocolTorrent, "season/episode mismatch"},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", 1, 1, core.ProtocolUsenet, ""},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", 1, 1, core.ProtocolHTTP, "season pack (skipped in v1)"},
		{"Doktor.Krasnikov.S01.E01.2023.WEB-DL", 1, 1, core.ProtocolTorrent, "title mismatch"},
		{"Doktor.Krasnov.2024.S01.E01.WEB-DL", 1, 1, core.ProtocolTorrent, "series year mismatch"},
		{"No results! Make sure to use the IMDb ID to search", 1, 1, core.ProtocolTorrent, "title mismatch"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req.Episode, req.Season = tc.episode, tc.season
			r := core.Release{ID: "russian", Name: tc.name, Info: releaseparse.Parse(tc.name), Protocol: tc.protocol}
			if got := rejectIdentity(r, title, req, ""); got != tc.want {
				t.Fatalf("rejectIdentity = %q, want %q; parsed %+v", got, tc.want, r.Info)
			}
			if tc.want == "" && r.Info.FullSeason {
				target := withTarget(r, req)
				if target.Target == nil || target.Target.Episode != req.Episode || target.Target.Season != req.Season {
					t.Fatalf("pack missing episode target: %+v", target.Target)
				}
			}
		})
	}
}
