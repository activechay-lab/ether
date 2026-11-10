package plex

import (
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontends/plex/model"
)

// TestParseRequestHeaderPlatformBeatsQueryGeneric proves the real wire
// protection for Plezy on Android/tvOS: plexParam reads the header before
// the query, so a decision request whose query says X-Plex-Platform=Generic
// still yields the platform caps from the header, not htpcCaps/genericCaps.
func TestParseRequestHeaderPlatformBeatsQueryGeneric(t *testing.T) {
	cases := []struct {
		name      string
		platform  string // X-Plex-Platform header
		device    string // X-Plex-Device header
		maxHeight int
		hdr, dv   bool
	}{
		{"plezy android phone", "Android", "Pixel 8", 1080, false, false}, // androidCaps
		{"plezy apple tv", "tvOS", "Apple TV", 2160, true, true},          // appleTVCaps
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/video/:/transcode/universal/decision?X-Plex-Platform=Generic", nil)
			r.Header.Set("X-Plex-Product", "Plezy")
			r.Header.Set("X-Plex-Platform", tc.platform)
			r.Header.Set("X-Plex-Device", tc.device)
			pr := ParseRequest(r)
			if pr.Platform != tc.platform {
				t.Fatalf("Platform = %q, want header %q to beat query Generic", pr.Platform, tc.platform)
			}
			caps := pr.ClientCaps(0)
			if caps.MaxHeight != tc.maxHeight || caps.HDR10 != tc.hdr || caps.DolbyVision != tc.dv {
				t.Fatalf("caps = %+v, want maxHeight=%d hdr=%v dv=%v", caps, tc.maxHeight, tc.hdr, tc.dv)
			}
		})
	}
}

func TestParseRequestNegotiatesFormatFromAccept(t *testing.T) {
	cases := []struct {
		name   string
		accept []string
		want   model.Format
	}{
		{"json only", []string{"application/json"}, model.JSON},
		{"plexapi prefers xml", []string{"application/xml, application/json;q=0.9,*/*;q=0.5"}, model.XML},
		{"plex web", []string{"application/json, text/plain, */*"}, model.JSON},
		{"xml only", []string{"application/xml"}, model.XML},
		{"no accept", nil, model.XML},
		{"wildcard", []string{"*/*"}, model.XML},
		{"json lower q", []string{"application/json;q=0.5, application/xml"}, model.XML},
		{"json higher q", []string{"application/xml;q=0.8, application/json"}, model.JSON},
		{"equal q json first", []string{"application/json, application/xml"}, model.JSON},
		{"equal q xml first", []string{"application/xml, application/json"}, model.XML},
		{"zero weight json", []string{"application/json;q=0"}, model.JSON},
		{"json with charset", []string{"application/json; charset=utf-8"}, model.XML},
		{"text xml does not qualify", []string{"text/xml, application/json"}, model.JSON},
		{"uppercase media does not qualify", []string{"APPLICATION/JSON"}, model.XML},
		{"uppercase q does not qualify", []string{"application/json;Q=0.5"}, model.XML},
		{"space after semicolon does not qualify", []string{"application/json; q=0.5"}, model.XML},
		{"space before semicolon qualifies", []string{"application/json ;q=0.5"}, model.JSON},
		{"non q first parameter", []string{"application/json;level=1;q=0.5"}, model.XML},
		{"empty parameter does not qualify", []string{"application/json;"}, model.XML},
		{"extra parameter weighs zero", []string{"application/json;q=0.9;charset=utf-8,application/xml;q=0.1"}, model.XML},
		{"extra parameter alone still qualifies", []string{"application/json;q=0.9;charset=utf-8"}, model.JSON},
		{"invalid quality alone qualifies", []string{"application/json;q=bogus"}, model.JSON},
		{"empty quality alone qualifies", []string{"application/json;q="}, model.JSON},
		{"negative quality alone qualifies", []string{"application/json;q=-1"}, model.JSON},
		{"quoted quality weighs zero", []string{`application/json;q="0.5",application/xml;q=0.1`}, model.XML},
		{"trailing junk weighs zero", []string{"application/json;q=0.5x,application/xml;q=0.1"}, model.XML},
		{"zero weight tie xml first", []string{"application/xml;q=0,application/json;q=0"}, model.XML},
		{"quality above one", []string{"application/json;q=2,application/xml;q=3"}, model.XML},
		{"split headers", []string{"application/xml", "application/json;q=0.9"}, model.XML},
		{"split tie xml first", []string{"application/xml", "application/json"}, model.XML},
		{"split tie json first", []string{"application/json", "text/xml"}, model.JSON},
		{"xml refused", []string{"application/xml;q=0, application/json;q=0.2"}, model.JSON},
		{"zero weight tie json first", []string{"application/json;q=0, application/xml;q=0"}, model.JSON},
		{"invalid json quality", []string{"application/json;q=bogus, application/xml"}, model.XML},
		{"negative json quality", []string{"application/json;q=-1, application/xml"}, model.XML},
		{"wildcard with explicit json", []string{"*/*, application/json"}, model.JSON},
		{"wildcard with zero weight json", []string{"*/*, application/json;q=0"}, model.JSON},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/identity", nil)
			for _, a := range tc.accept {
				r.Header.Add("Accept", a)
			}
			if got := ParseRequest(r).Format; got != tc.want {
				t.Fatalf("Accept %q: format %v, want %v", tc.accept, got, tc.want)
			}
		})
	}
}
