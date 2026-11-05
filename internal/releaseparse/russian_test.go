package releaseparse

import (
	"reflect"
	"testing"
)

func TestRussianEpisodeIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, title       string
		seasons, episodes []int
		pack              bool
	}{
		{"Доктор Краснов / Сезон: 1 / Серии: 1 из 16 [2023 WEBRip-AVC]", "Доктор Краснов", []int{1}, []int{1}, false},
		{"Доктор Краснов / Сезон: 1 / Серии: 16 из 16 [2023 WEBRip 1080p]", "Доктор Краснов", []int{1}, []int{16}, false},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip]", "Доктор Краснов", []int{1}, episodeRange(1, 16), true},
		{"Доктор Краснов / Сезон: 1 / Серии: 1-16 из 16 [2023 WEBRip-AVC]", "Доктор Краснов", []int{1}, episodeRange(1, 16), true},
		{"Доктор Краснов / Сезон 2 / Серии: 3-5 из 16 [2023 WEBRip]", "Доктор Краснов", []int{2}, []int{3, 4, 5}, true},
		{"Доктор Краснов / 1 сезон / 2 серия [2023 WEBRip]", "Доктор Краснов", []int{1}, []int{2}, false},
		{"Доктор Краснов / Серия 2 [2023 WEBRip]", "Доктор Краснов", nil, []int{2}, false},
		{"Доктор Краснов / Сезон: 1 [2023 WEBRip]", "Доктор Краснов", []int{1}, nil, true},
		{"01. Доктор Краснов.2023.WEB-DL 1080p.Files-x", "Доктор Краснов", nil, []int{1}, false},
		{"01.Доктор Краснов.(2023).WEBRip.(AVC).by.Серый1779.Files-x", "Доктор Краснов", nil, []int{1}, false},
		{"16. Доктор Краснов.2023.WEB-DL 1080p", "Доктор Краснов", nil, []int{16}, false},
		{"Doktor.Krasnov.S01.E01.2023.WEB-DLRip.DenSBK", "Doktor Krasnov", []int{1}, []int{1}, false},
		{"Доктор Краснов [S01] (2023) WEBRip от Generalfilm | КПК", "Доктор Краснов", []int{1}, nil, true},
		{"Доктор Краснов / Doctor Krasnov (2023) Сезон: 1 / Серия: 1 WEBRip", "Доктор Краснов / Doctor Krasnov", []int{1}, []int{1}, false},
		{"12.Monkeys.1995.1080p.BluRay", "12 Monkeys", nil, nil, false},
		{"2001.A.Space.Odyssey.1968.1080p.BluRay", "2001 A Space Odyssey", nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Parse(tc.name)
			if got.Title != tc.title || !reflect.DeepEqual(got.Seasons, tc.seasons) || !reflect.DeepEqual(got.Episodes, tc.episodes) || got.FullSeason != tc.pack {
				t.Errorf("got title=%q seasons=%v episodes=%v pack=%v; want %q %v %v %v", got.Title, got.Seasons, got.Episodes, got.FullSeason, tc.title, tc.seasons, tc.episodes, tc.pack)
			}
		})
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
