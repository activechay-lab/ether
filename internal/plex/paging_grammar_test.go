package plex

import (
	"net/http/httptest"
	"net/url"
	"testing"
)

// Paging is a pair, not two independent defaults. In particular, validating
// size and validating start have different triggers in PMS.
func TestPagingGrammar(t *testing.T) {
	for _, tc := range []struct {
		name           string
		start, size    *string
		invalid, paged bool
	}{
		{name: "absent"},
		{name: "only start", start: modelString("4")},
		{name: "invalid start alone", start: modelString("invalid")},
		{name: "only size", size: modelString("3")},
		{name: "empty size", size: modelString(""), invalid: true},
		{name: "invalid size alone", size: modelString("3.0"), invalid: true},
		{name: "invalid start with negative size", start: modelString("x"), size: modelString("-1"), invalid: true},
		{name: "negative start", start: modelString("-2"), size: modelString("3")},
		{name: "negative size", start: modelString("0"), size: modelString("-1")},
		{name: "zero window", start: modelString("0"), size: modelString("0"), paged: true},
		{name: "positive signs", start: modelString("+01"), size: modelString("+3"), paged: true},
		{name: "signed32 overflow", start: modelString("0"), size: modelString("2147483648"), invalid: true},
		{name: "signed32 minimum", start: modelString("-2147483648"), size: modelString("1")},
		{name: "query whitespace", start: modelString("0"), size: modelString(" 1"), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, header := range []bool{false, true} {
				r := httptest.NewRequest("GET", "/identity", nil)
				q := url.Values{}
				for k, v := range map[string]*string{"X-Plex-Container-Start": tc.start, "X-Plex-Container-Size": tc.size} {
					if v != nil {
						if header {
							r.Header.Set(k, *v)
						} else {
							q.Set(k, *v)
						}
					}
				}
				r.URL.RawQuery = q.Encode()
				pr := ParseRequest(r)
				if pr.pagingInvalid != tc.invalid || pr.HasPage != tc.paged {
					t.Errorf("header=%v invalid=%v paged=%v", header, pr.pagingInvalid, pr.HasPage)
				}
			}
		})
	}
	r := httptest.NewRequest("GET", "/identity?X-Plex-Container-Start=0&X-Plex-Container-Size=8", nil)
	r.Header.Set("X-Plex-Container-Start", "3")
	pr := ParseRequest(r)
	if !pr.HasPage || pr.Start != 3 || pr.Size != 8 {
		t.Fatalf("mixed paging: %+v", pr)
	}
	r.Header.Set("X-Plex-Container-Size", "")
	if !ParseRequest(r).pagingInvalid {
		t.Fatal("empty header must override query")
	}
	f := newFixture()
	defer f.srv.Close()
	if rec := f.get(t, "/identity?X-Plex-Container-Size=bogus"); rec.Code != 500 {
		t.Fatalf("malformed paging status=%d", rec.Code)
	}
}

func modelString(s string) *string { return &s }
