package mediabrowser

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

// Jellyfin and Emby send a decade as Years=1990,...,1999 (#401).
func TestItemQueryYearsAndPremiereDates(t *testing.T) {
	for _, tc := range []struct {
		query      string
		from, to   int
		years      []int
		dateFilter int
	}{
		{query: "Years=1999", from: 1999, to: 1999},
		{query: "Years=1990,1991,1992,1993,1994,1995,1996,1997,1998,1999", from: 1990, to: 1999},
		{query: "Years=1999,1990,1995,1991,1992,1993,1994,1996,1997,1998", from: 1990, to: 1999},
		{query: "Years=1984,1999,2001", years: []int{1984, 1999, 2001}},
		{query: "Years=1984&Years=1985", from: 1984, to: 1985},
		{query: "Years=x,2001", from: 2001, to: 2001},
		{query: "MinPremiereDate=2001-02-03T00:00:00.0000000Z&MaxPremiereDate=2004-05-06", dateFilter: 2},
	} {
		q := itemQuery(httptest.NewRequest("GET", "/Items?"+tc.query, nil))
		if q.Filter.YearFrom != tc.from || q.Filter.YearTo != tc.to || !slices.Equal(q.Filter.Years, tc.years) || len(q.Filter.ReleasedFilters) != tc.dateFilter {
			t.Errorf("%s: from %d to %d years %v dates %v", tc.query, q.Filter.YearFrom, q.Filter.YearTo, q.Filter.Years, q.Filter.ReleasedFilters)
		}
	}
	q := itemQuery(httptest.NewRequest("GET", "/Items?MinPremiereDate=2001-02-03T04:05:06Z&MaxPremiereDate=2004-05-06", nil))
	got := map[string]int64{}
	for _, df := range q.Filter.ReleasedFilters {
		got[df.Op] = df.Unix
		if df.Op == "<=" && !df.SpanDay {
			t.Error("a MaxPremiereDate date does not span its day")
		}
	}
	if got[">="] != time.Date(2001, 2, 3, 4, 5, 6, 0, time.UTC).Unix() || got["<="] != time.Date(2004, 5, 6, 0, 0, 0, 0, time.UTC).Unix() {
		t.Error(got)
	}
}

// TestItemQueryUnservedTypes records the item kinds the catalogue cannot hold
// so a listing of only those answers empty instead of dropping the filter
// (#405: Person, MusicVideo, MusicArtist, MusicAlbum, Audio, MusicVideo). The
// four catalogue kinds still map to Types.
func TestItemQueryUnservedTypes(t *testing.T) {
	q := itemQuery(httptest.NewRequest("GET", "/Items?IncludeItemTypes=Episode", nil))
	if len(q.Types) != 1 || q.Types[0] != "episode" || len(q.UnservedTypes) != 0 {
		t.Fatalf("Episode: types %v unserved %v", q.Types, q.UnservedTypes)
	}
	for _, kind := range []string{"Person", "MusicVideo", "MusicArtist", "MusicAlbum", "Audio"} {
		q := itemQuery(httptest.NewRequest("GET", "/Items?IncludeItemTypes="+kind, nil))
		if len(q.Types) != 0 {
			t.Fatalf("%s: mapped to types %v", kind, q.Types)
		}
		if len(q.UnservedTypes) != 1 || q.UnservedTypes[0] != strings.ToLower(kind) {
			t.Fatalf("%s: unserved %v", kind, q.UnservedTypes)
		}
	}
	// A mixed request keeps the mappable kinds and records the rest.
	q = itemQuery(httptest.NewRequest("GET", "/Items?IncludeItemTypes=Movie,Person", nil))
	if len(q.Types) != 1 || q.Types[0] != "movie" || len(q.UnservedTypes) != 1 || q.UnservedTypes[0] != "person" {
		t.Fatalf("mixed: types %v unserved %v", q.Types, q.UnservedTypes)
	}
	// No IncludeItemTypes at all is unrestricted, not unserved.
	q = itemQuery(httptest.NewRequest("GET", "/Items?Recursive=true", nil))
	if len(q.Types) != 0 || len(q.UnservedTypes) != 0 {
		t.Fatalf("unrestricted: types %v unserved %v", q.Types, q.UnservedTypes)
	}
}
