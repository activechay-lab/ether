package releaseparse

import (
	"slices"
	"testing"
)

func TestTemporadaSeasonEpisode(t *testing.T) {
	for _, tc := range []struct {
		name            string
		season, episode int
		title           string
	}{
		{"La.Casa.de.Papel.T03E05.1080p.WEB-DL.Spanish", 3, 5, "La Casa de Papel"},
		{"Serie.T07E09.720p.HDTV.Portugues", 7, 9, "Serie"},
		{"Serie t1e12 HDTV", 1, 12, "Serie"},
	} {
		i := Parse(tc.name)
		if !slices.Equal(i.Seasons, []int{tc.season}) || !slices.Equal(i.Episodes, []int{tc.episode}) || i.Title != tc.title {
			t.Errorf("%s: seasons=%v episodes=%v title=%q", tc.name, i.Seasons, i.Episodes, i.Title)
		}
	}
	// A title word such as "T2" is not a season tag.
	if i := Parse("T2.Trainspotting.2017.1080p.BluRay"); len(i.Seasons) != 0 {
		t.Errorf("T2 parsed as seasons %v", i.Seasons)
	}
}
