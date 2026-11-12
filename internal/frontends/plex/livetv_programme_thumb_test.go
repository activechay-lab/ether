package plex

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestLiveProgrammeThumbRewritesRelativePaths covers the bug: EPG programme
// posters came through as raw TMDB-style paths (e.g. "/abc123.jpg") that
// Ether has no route for, causing clients to 404 fetching guide artwork -
// unlike channel logos (liveGuideLogo) and Plex-matched programmes (already
// absolute via plexmeta.absURL), which both worked already.
func TestLiveProgrammeThumbRewritesRelativePaths(t *testing.T) {
	var s *Server
	key := core.RatingKey(1)
	got := s.liveProgrammeThumb(key, "/abc123.jpg")
	if !strings.HasPrefix(got, "/library/metadata/"+key.String()+"/thumb/") {
		t.Fatalf("relative EPG poster path was not rewritten to a local proxy URL: %q", got)
	}
}

func TestLiveProgrammeThumbLeavesAbsoluteURLsAlone(t *testing.T) {
	var s *Server
	for _, abs := range []string{
		"https://image.tmdb.org/t/p/original/abc123.jpg",
		"http://metadata.provider.plex.tv/library/metadata/1/thumb/1",
	} {
		if got := s.liveProgrammeThumb(core.RatingKey(1), abs); got != abs {
			t.Fatalf("already-absolute thumb URL was rewritten: got %q, want unchanged %q", got, abs)
		}
	}
}

func TestLiveProgrammeThumbLeavesEmptyOrUnkeyedAlone(t *testing.T) {
	var s *Server
	if got := s.liveProgrammeThumb(core.RatingKey(1), ""); got != "" {
		t.Fatalf("empty thumb should stay empty, got %q", got)
	}
	if got := s.liveProgrammeThumb(core.RatingKey(0), "/abc123.jpg"); got != "/abc123.jpg" {
		t.Fatalf("unkeyed (unmatched) programme should be left unchanged, got %q", got)
	}
}
