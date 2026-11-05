package plex

import (
	"net/http"
	"testing"
)

// Both stream selectors share a route but not a parser: malformed subtitles
// clear the choice, malformed audio preserves it, and -1 restores defaults.
func TestPartSelectionValueGrammars(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	const key int64 = 1000000000000550
	for _, tc := range []struct {
		query string
		code  int
	}{
		{"audioStreamID=0", 400}, {"audioStreamID=-2", 400}, {"subtitleStreamID=-2", 400},
		{"audioStreamID=nonsense", 200}, {"subtitleStreamID=nonsense", 200},
		{"audioStreamID=-1", 200}, {"subtitleStreamID=-1", 200}, {"subtitleStreamID=0", 200},
	} {
		f.srv.selections.update(1, key, func(s *selection) { s.audioSet = true; s.audio = 3; s.subtitleSet = true; s.subtitle = 4 })
		rec := f.request(t, http.MethodPut, "/library/parts/1000000000000550?"+tc.query, nil)
		if rec.Code != tc.code {
			t.Errorf("%s: status %d, want %d", tc.query, rec.Code, tc.code)
		}
		sel := f.srv.selections.get(1, key)
		switch tc.query {
		case "audioStreamID=nonsense":
			if !sel.audioSet || sel.audio != 3 {
				t.Error("malformed audio changed selection")
			}
		case "subtitleStreamID=nonsense", "subtitleStreamID=0":
			if !sel.subtitleSet || sel.subtitle != -1 {
				t.Error("subtitle was not cleared")
			}
		case "audioStreamID=-1":
			if sel.audioSet {
				t.Error("audio default did not reset")
			}
		case "subtitleStreamID=-1":
			if sel.subtitleSet {
				t.Error("subtitle default did not reset")
			}
		}
	}
}
