package releaseparse

import (
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestRussianEpisodeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, title       string
		seasons, episodes []int
		pack              bool
		seriesYear        int
	}{
		{"Доктор Краснов / Сезон: 1 / Серии: 1 из 16 [2023 WEBRip-AVC]", "Доктор Краснов", []int{1}, []int{1}, false, 0},
		{"Доктор Краснов / Сезон: 1 / Серии: 16 из 16 [2023 WEBRip 1080p]", "Доктор Краснов", []int{1}, []int{16}, false, 0},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", "Доктор Краснов", []int{1}, episodeRange(1, 16), true, 0},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip-AVC]", "Доктор Краснов", []int{1}, episodeRange(1, 16), true, 0},
		{"Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip 1080p]", "Доктор Краснов", []int{2}, episodeRange(1, 16), true, 0},
		{"Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip-AVC]", "Доктор Краснов", []int{2}, episodeRange(1, 16), true, 0},
		{"Доктор Краснов / Сезон: 2 / Серии: 1-16 из 16 [2025 WEBRip]", "Доктор Краснов", []int{2}, episodeRange(1, 16), true, 0},
		{"Доктор Краснов [S02] (2025) WEBRip 1080p от Files-x", "Доктор Краснов", []int{2}, nil, true, 0},
		{"Доктор Краснов [02x01-12 из 16] (2025) WEBRip 1080p от Files-x", "Доктор Краснов", []int{2}, episodeRange(1, 12), true, 0},
		{"17.Доктор Краснов.s02.E01.(2025).WEBRip (1080p).by.Nicodem.Files-x", "Доктор Краснов", []int{2}, []int{1}, false, 0},
		{"Доктор Краснов / Сезон 2 / Серии: 3-5 из 16 [2023 WEBRip]", "Доктор Краснов", []int{2}, []int{3, 4, 5}, true, 0},
		{"Доктор Краснов / 1 сезон / 2 серия [2023 WEBRip]", "Доктор Краснов", []int{1}, []int{2}, false, 0},
		{"Доктор Краснов / Серия 2 [2023 WEBRip]", "Доктор Краснов", nil, []int{2}, false, 0},
		{"Доктор Краснов / Сезон: 1 [2023 WEBRip]", "Доктор Краснов", []int{1}, nil, true, 0},
		{"01. Доктор Краснов.2023.WEB-DL 1080p.Files-x", "Доктор Краснов", nil, []int{1}, false, 2023},
		{"01.Доктор Краснов.(2023).WEBRip.(AVC).by.Серый1779.Files-x", "Доктор Краснов", nil, []int{1}, false, 2023},
		{"01. Доктор Краснов.2023.WEBRip", "Доктор Краснов", nil, []int{1}, false, 2023},
		{"16. Доктор Краснов.2023.WEB-DL 1080p", "Доктор Краснов", nil, []int{16}, false, 2023},
		{"Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK", "Doktor Krasnov", []int{1}, []int{1}, false, 0},
		{"Доктор Краснов [S01] (2023) WEBRip от Generalfilm | КПК", "Доктор Краснов", []int{1}, nil, true, 0},
		{"Доктор Краснов / Doctor Krasnov (2023) Сезон: 1 / Серия: 1 WEBRip", "Доктор Краснов / Doctor Krasnov", []int{1}, []int{1}, false, 2023},
		{"Доктор Краснов / Doctor Krasnov (1985) Сезон: 2 / Серии: 1-16 из 16 WEBRip 1080p", "Доктор Краснов / Doctor Krasnov", []int{2}, episodeRange(1, 16), true, 1985},
		{"12.Monkeys.1995.1080p.BluRay", "12 Monkeys", nil, nil, false, 1995},
		{"2001.A.Space.Odyssey.1968.1080p.BluRay", "2001 A Space Odyssey", nil, nil, false, 1968},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.name)
			if got.Title != tc.title || !reflect.DeepEqual(got.Seasons, tc.seasons) || !reflect.DeepEqual(got.Episodes, tc.episodes) || got.FullSeason != tc.pack {
				t.Errorf("got title=%q seasons=%v episodes=%v pack=%v; want %q %v %v %v", got.Title, got.Seasons, got.Episodes, got.FullSeason, tc.title, tc.seasons, tc.episodes, tc.pack)
			}
			if sy := EpisodeSeriesYear(tc.name, got.Year); sy != tc.seriesYear {
				t.Errorf("EpisodeSeriesYear(%q, %d) = %d; want %d", tc.name, got.Year, sy, tc.seriesYear)
			}
		})
	}
	if got := Parse("Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK").Source; got != core.SourceWEBRip {
		t.Errorf("WEB-DLRip source = %q; want %q", got, core.SourceWEBRip)
	}
	for _, name := range []string{
		"Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK",
		"17.Доктор Краснов.s02.E01.(2025).WEBRip (1080p).by.Nicodem.Files-x",
	} {
		if !EpisodeTitleMatches(name, "Серия 1") || !EpisodeTitleMatches(name, "The First") {
			t.Errorf("post-episode year treated as conflicting episode title for %q", name)
		}
	}
}

func TestIsPlaceholderResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"No results! Make sure to use the IMDb ID to search", true},
		{"No results found", true},
		{"No results", true},
		{"No.Results.2022.1080p.WEB-DL.H264-GRP", false},
		{"Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK", false},
		{"", false},
	} {
		if got := IsPlaceholderResult(tc.name); got != tc.want {
			t.Errorf("IsPlaceholderResult(%q) = %v; want %v", tc.name, got, tc.want)
		}
	}
}

func episodeRange(first, last int) []int {
	var out []int
	for n := first; n <= last; n++ {
		out = append(out, n)
	}
	return out
}

func TestCyrillicTitleMatching(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{"Доктор Краснов", "Doktor.Krasnov", true},
		{"ДОКТОР КРАСНОВ", "Доктор Краснов", true},
		{"Орёл", "Орел", true},
		{"Орёл", "Oryol", true},
		{"Орёл", "Orel", true},
		{"Доктор Краснов / Doctor Krasnov", "Доктор Краснов", true},
		{"Доктор Краснов / Doctor Krasnov", "Doctor Krasnov", true},
		{"Жизнь Щуки", "Zhizn Shchuki", true},
		{"Холодное сердце", "Kholodnoe serdtse", true},
		{"Юлия и Яков", "Yuliya i Yakov", true},
		{"Face / Off", "Face", false},
		{"Доктор Краснов", "Doktor.Krasnov.2", false},
		{"Доктор Краснов", "Doktor.Krasnikov", false},
		{"Доктор Краснов", "Doctor Kracsnov", false},
	} {
		if got := TitlesMatch(tc.a, tc.b); got != tc.want {
			t.Errorf("TitlesMatch(%q,%q)=%v", tc.a, tc.b, got)
		}
	}
}
