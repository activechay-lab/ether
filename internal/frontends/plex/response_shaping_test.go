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

// A library listing honours excludeFields and PMS's allow-listed
// excludeElements: an excluded Part leaves Media@partCount, and names a PMS
// ignores (Image) are ignored.
func TestListingHonoursPMSExclusions(t *testing.T) {
	f := newFixture()
	full := f.getWarm(t, "/library/sections/1/all", jsonAccept...).Body.String()
	got := f.getWarm(t, "/library/sections/1/all?excludeFields=summary&excludeElements=Part,Genre,Image", jsonAccept...).Body.String()
	for _, want := range []string{`"summary"`, `"Part"`, `"Genre"`, `"Image"`} {
		if !strings.Contains(full, want) {
			t.Fatalf("the unexcluded listing lacks %s, so the check proves nothing: %s", want, full)
		}
	}
	for _, gone := range []string{`"summary"`, `"Part"`, `"Genre"`} {
		if strings.Contains(got, gone) {
			t.Errorf("excluded %s is still sent: %s", gone, got)
		}
	}
	if !strings.Contains(got, `"Image"`) || !strings.Contains(got, `"partCount":1`) {
		t.Errorf("want Image kept (PMS ignores it) and Media@partCount: %s", got)
	}
}
