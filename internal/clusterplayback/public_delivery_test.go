package clusterplayback

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicRepresentationMatcherLeavesControlAndUnsupportedRoutes(t *testing.T) {
	for _, test := range []struct {
		method, path string
		want         bool
	}{{"GET", "/jellyfin/sessions/media/id/grant/file", true}, {"HEAD", "/emby/sessions/media/id/grant/manifest.mpd", true}, {"GET", "/sessions/media/id/grant/master.m3u8", true}, {"GET", "/video/:/transcode/universal/session/id/base/index.m3u8", true}, {"HEAD", "/video/:/transcode/universal/session/id/0/header", true}, {"GET", "/video/:/transcode/universal/session/id/0/1.m4s", true}, {"POST", "/sessions/media/id/grant/file", false}, {"GET", "/video/:/transcode/universal/start.m3u8", false}, {"GET", "/video/:/transcode/universal/session/id/subtitles/1.ass", false}, {"GET", "/video/:/transcode/universal/session/id/base/index.mpd", false}, {"GET", "/subtitles/:/transcode/universal/start", false}, {"GET", "/silo/sessions/media/id/grant/file", false}, {"GET", "/sessions/media/id/grant/../control", false}} {
		if got := testPublicRepresentation(httptest.NewRequest(test.method, test.path, nil)); got != test.want {
			t.Errorf("%s %s shape %v", test.method, test.path, got)
		}
	}
}

func TestPlexRepresentationComponentBoundMatchesOwner(t *testing.T) {
	for _, n := range []int{128, 129} {
		q := httptest.NewRequest("GET", "/video/:/transcode/universal/session/id/"+strings.Repeat("a", n)+"/0.ts", nil)
		if got := testPublicRepresentation(q); got != (n == 128) {
			t.Errorf("component length %d recognized %v", n, got)
		}
	}
}
