package plex

import (
	"net/http"
	"testing"
)

// Both result projections share the grammar, but the flat form caps the
// combined title/person rows rather than each result family independently.
func TestSearchLimitGrammarAndCombinedCap(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	for _, endpoint := range []string{"/hubs/search", "/library/search"} {
		for _, tc := range []struct {
			value  string
			status int
		}{{"abc", 400}, {"", 400}, {"1.5", 400}, {"0", 500}, {"-1", 500}, {"01", 200}} {
			rec := f.get(t, endpoint+"?query=Fjord&limit="+tc.value, jsonAccept...)
			if rec.Code != tc.status {
				t.Errorf("%s limit=%q: %d want %d", endpoint, tc.value, rec.Code, tc.status)
			}
		}
	}
	// The fixture has multiple title hits; this also guards the successful
	// request after the invalid forms, rather than accepting any 500 panic.
	rec := f.get(t, "/library/search?query=the&limit=1", jsonAccept...)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body.String())
	}
	mc := decodeJSON(t, rec)
	if mc["size"].(float64) > 1 {
		t.Fatalf("flat cap: %v", mc)
	}
}
