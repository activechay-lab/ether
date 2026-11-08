package plex

import (
	"strings"
	"testing"
)

// Rows that skip building the Media a client excluded must encode exactly
// as rows that build it and leave it out, in both formats.
func TestExcludedMediaSkipAnswersSameBytes(t *testing.T) {
	f := newFixture()
	paths := []string{
		"/library/sections/1/all?excludeElements=Media,Genre&excludeFields=summary",
		"/library/sections/2/allLeaves?excludeElements=Media&includeGuids=1",
		"/library/metadata/" + bbS1.Key.String() + "/children?excludeElements=Media",
	}
	for _, accept := range [][]string{nil, jsonAccept} {
		for _, p := range paths {
			listMediaSkip = false
			want := f.getWarm(t, p, accept...).Body.String()
			listMediaSkip = true
			got := f.getWarm(t, p, accept...).Body.String()
			if !strings.Contains(want, "mediaCount") {
				t.Errorf("%s: no row stood in for its excluded Media: %s", p, want)
			}
			if got != want {
				t.Errorf("%s (accept %v): skipping excluded Media changed the body\nskip: %s\nfull: %s", p, accept, got, want)
			}
		}
	}
}
