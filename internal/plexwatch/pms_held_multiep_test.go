package plexwatch

import (
	"reflect"
	"testing"
)

func TestSpannedEpisodesFromCombinedFiles(t *testing.T) {
	file := func(name string) pmsMetadata {
		var md pmsMetadata
		md.Media = make([]struct {
			Part []struct {
				File string `json:"file"`
			} `json:"Part"`
		}, 1)
		md.Media[0].Part = append(md.Media[0].Part, struct {
			File string `json:"file"`
		}{File: name})
		return md
	}
	for _, tc := range []struct {
		name          string
		season, first int
		want          []int
	}{
		{"/tv/Once Upon a Time (2011) - S05E22-23 - Only You + An Untold Story Bluray-1080p.mkv", 5, 22, []int{23}},
		{`D:\TV\Show - S04E08E09 - Smash the Mirror.mkv`, 4, 8, []int{9}},
		{"Show.S06E21-E22.1080p.mkv", 6, 21, []int{22}},
		{"Show.s01e01e02e03.mkv", 1, 1, []int{2, 3}},
		{"Show - S05E22 - Only You.mkv", 5, 22, nil},
		{"Show - S05E22-23.mkv", 4, 22, nil},
		{"Show S02E05 - 1080p.mkv", 2, 5, nil},
	} {
		if got := spannedEpisodes(file(tc.name), tc.season, tc.first); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}
