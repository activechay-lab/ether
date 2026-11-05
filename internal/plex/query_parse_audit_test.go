package plex

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

// The saved-filter editor needs the same tree in XML and JSON; its operators
// and scopes must survive the round trip without evaluating the filter values.
func TestQueryParseAuditContract(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	path := "/library/sections/1/query/parse?type=1&push=1&genre=28&pop=1&or=1&year%3E%3E=2000&and=1&episode.unwatched=1&sort=random:123,titleSort:desc:nocollate&limit=7&group=year&bogus=1"
	rec := f.get(t, path, jsonAccept...)
	var raw struct {
		Query struct {
			Type  int
			Rule  queryRule
			Order []queryOrder
			Limit []struct{ Limit int }
			Group []struct{ Field string }
			Error []struct{ Type, Value string }
		}
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	q := raw.Query
	if rec.Code != 200 || q.Type != 1 || len(q.Limit) != 1 || q.Limit[0].Limit != 7 || len(q.Group) != 1 || q.Group[0].Field != "year" || len(q.Error) != 1 || q.Error[0].Value != "bogus" {
		t.Fatalf("unexpected query: %s", rec.Body.String())
	}
	if q.Rule.Operator != "or" || len(q.Rule.Rule) != 2 || q.Rule.Rule[1].Operator != "and" || q.Rule.Rule[1].Rule[1].Type != "episode" || q.Rule.Rule[1].Rule[1].Field != "unwatched" {
		t.Fatalf("lost grouping/scope: %s", rec.Body.String())
	}
	if len(q.Order) != 2 || q.Order[0].Field != "random" || !q.Order[0].Ascending || q.Order[1].Ascending {
		t.Fatalf("sort: %s", rec.Body.String())
	}
	rec = f.get(t, path)
	var doc struct {
		XMLName xml.Name
		Type    int `xml:"type,attr"`
		Order   []struct {
			Ascending string `xml:"ascending,attr"`
		} `xml:"Order"`
	}
	if err := xml.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.XMLName.Local != "Query" || doc.Type != 1 || len(doc.Order) != 2 || doc.Order[1].Ascending != "0" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/xml") {
		t.Fatalf("XML: %s", rec.Body.String())
	}
	for _, tc := range []struct {
		Query string
		Code  int
	}{
		{"type=abc", 400}, {"type=", 400}, {"type=99", 200}, {"sort=bogusKey", 500}, {"limit=abc", 500}, {"genre%3E%3E=6", 200},
	} {
		got := f.get(t, "/library/sections/1/query/parse?"+tc.Query, jsonAccept...)
		if got.Code != tc.Code {
			t.Errorf("%s = %d: %s", tc.Query, got.Code, got.Body.String())
		}
	}
	if got := f.request(t, http.MethodPost, "/library/sections/1/query/parse", nil); got.Code != 404 {
		t.Fatalf("POST=%d", got.Code)
	}
}

func TestQueryParseAuditMalformedGroups(t *testing.T) {
	titles := map[string]string{"genre": "Genre", "year": "Year"}
	for _, tc := range []struct {
		Query         string
		Rules, Errors int
		Empty         bool
	}{
		{"push=1&genre=6", 0, 1, false},
		{"and=1&genre=6", 0, 1, true},
		{"genre=6&and=1", 2, 1, false},
	} {
		q := parseQueryTree(tc.Query, 1, titles)
		if (q.Rule == nil) != tc.Empty || len(q.Error) != tc.Errors {
			t.Errorf("%s: %+v", tc.Query, q)
			continue
		}
		if q.Rule != nil && len(q.Rule.Rule) != tc.Rules {
			t.Errorf("%s rules: %+v", tc.Query, q.Rule)
		}
	}
}
