package plex

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestCollectionTitleSearchGrammar(t *testing.T) {
	cs := []core.Collection{{Title: "Archive Bright-Stars"}, {Title: "Archive Café"}, {Title: "Quiet (Moon)"}}
	for _, tc := range []struct {
		query string
		want  []string
	}{
		{"title=bright", []string{"Archive Bright-Stars"}},
		{"title=Stars", nil},
		{"title=right", nil},
		{"title=archive%20bright", []string{"Archive Bright-Stars"}},
		{"title=archive%20%20bright", nil},
		{"title=cafe", []string{"Archive Café"}},
		{"title!=CAFE", []string{"Archive Bright-Stars", "Archive Café", "Quiet (Moon)"}},
		{"title!=stars", []string{"Archive Café", "Quiet (Moon)"}},
		{"title%3C=quiet", []string{"Quiet (Moon)"}},
		{"title%3E=stars", []string{"Archive Bright-Stars"}},
		{"title==Archive", nil},
		{"title=absent,quiet", []string{"Quiet (Moon)"}},
		{"title=Moon", nil},
		{"title=%28moon", []string{"Quiet (Moon)"}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			q, err := url.ParseQuery(tc.query)
			if err != nil {
				t.Fatal(err)
			}
			var got []string
			for _, c := range filterCollectionTitles(cs, q) {
				got = append(got, c.Title)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestCollectionTitleFilterBeforePaging(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	for i, name := range []string{"Archive North", "Archive South", "Quiet Shore"} {
		key := core.SmartKey(int64(881 + i))
		f.cols.cols[key] = core.Collection{Key: key, Title: name, Kind: "user", Type: core.TypeMovie}
	}
	rec := f.get(t, "/library/sections/1/all?type=18&title=quiet&X-Plex-Container-Start=0&X-Plex-Container-Size=1", jsonAccept...)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	mc := decodeJSON(t, rec)
	if mc["totalSize"] != float64(1) || mc["size"] != float64(1) {
		t.Fatalf("filtered page: %v", mc)
	}
	if rec.Header().Get("X-Plex-Container-Total-Size") != "1" {
		t.Fatalf("total header: %v", rec.Header())
	}
}
