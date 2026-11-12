package plex

import (
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
)

// TestLiveProgrammeThumbRewritesRelativePaths covers the bug: EPG programme
// posters came through as raw TMDB-style paths (e.g. "/abc123.jpg") that
// Ether has no route for, causing clients to 404 fetching guide artwork -
// unlike channel logos (liveGuideLogo) and Plex-matched programmes (already
// absolute via plexmeta.absURL), which both worked already.
func TestLiveProgrammeThumbRewritesRelativePaths(t *testing.T) {
	var s *Server
	key := frontend.RatingKey(1)
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
		if got := s.liveProgrammeThumb(frontend.RatingKey(1), abs); got != abs {
			t.Fatalf("already-absolute thumb URL was rewritten: got %q, want unchanged %q", got, abs)
		}
	}
}

func TestLiveProgrammeThumbLeavesEmptyOrUnkeyedAlone(t *testing.T) {
	var s *Server
	if got := s.liveProgrammeThumb(frontend.RatingKey(1), ""); got != "" {
		t.Fatalf("empty thumb should stay empty, got %q", got)
	}
	if got := s.liveProgrammeThumb(frontend.RatingKey(0), "/abc123.jpg"); got != "/abc123.jpg" {
		t.Fatalf("unkeyed (unmatched) programme should be left unchanged, got %q", got)
	}
}

// TestLiveProgrammeArtUsesArtRoute covers the review fix: a backdrop must go
// through the /art route, not /thumb, or the guide's background would render
// the title's poster instead of its backdrop.
func TestLiveProgrammeArtUsesArtRoute(t *testing.T) {
	key := frontend.RatingKey(1)
	got := liveProgrammeArt(key, "/backdrop123.jpg")
	if !strings.HasPrefix(got, "/library/metadata/"+key.String()+"/art/") {
		t.Fatalf("relative backdrop path was not rewritten to the art route: %q", got)
	}
}

// TestLiveProgrammeMetadataGrandparentUsesShowKey covers the review fix: for
// an airing episode, GrandparentThumb/GrandparentArt must resolve against the
// show's own catalog key, not the episode's - the episode key's /thumb route
// serves the episode's still, not the show's poster.
func TestLiveProgrammeMetadataGrandparentUsesShowKey(t *testing.T) {
	var s *Server
	episodeKey := frontend.RatingKey(2)
	showKey := frontend.RatingKey(1)
	p := frontend.LiveTVProgramme{
		ID: "p1",
		Item: frontend.LiveTVItem{
			Key:     episodeKey,
			ShowKey: showKey,
			GUID:    "plex://episode/abc",
			Thumb:   "/episode-still.jpg",
			Art:     "/show-backdrop.jpg",
		},
	}
	md := s.liveProgrammeMetadata(frontend.LiveTVChannel{}, p)
	if !strings.HasPrefix(md.GrandparentThumb, "/library/metadata/"+showKey.String()+"/thumb/") {
		t.Fatalf("GrandparentThumb did not use the show's key: %q", md.GrandparentThumb)
	}
	if !strings.HasPrefix(md.GrandparentArt, "/library/metadata/"+showKey.String()+"/art/") {
		t.Fatalf("GrandparentArt did not use the show's key: %q", md.GrandparentArt)
	}
	if !strings.HasPrefix(md.Art, "/library/metadata/"+episodeKey.String()+"/art/") {
		t.Fatalf("episode-level Art unexpectedly used the show's key: %q", md.Art)
	}
}

// TestLiveProgrammeMetadataGrandparentFallsBackToEpisodeKey covers the case
// where the guide never resolved a show key (e.g. a standalone airing): the
// grandparent fields should fall back to the episode's own key rather than
// going unresolved.
func TestLiveProgrammeMetadataGrandparentFallsBackToEpisodeKey(t *testing.T) {
	var s *Server
	episodeKey := frontend.RatingKey(3)
	p := frontend.LiveTVProgramme{
		ID: "p2",
		Item: frontend.LiveTVItem{
			Key:   episodeKey,
			GUID:  "plex://episode/def",
			Thumb: "/episode-still.jpg",
		},
	}
	md := s.liveProgrammeMetadata(frontend.LiveTVChannel{}, p)
	if !strings.HasPrefix(md.GrandparentThumb, "/library/metadata/"+episodeKey.String()+"/thumb/") {
		t.Fatalf("GrandparentThumb did not fall back to the episode key: %q", md.GrandparentThumb)
	}
}
