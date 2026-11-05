package plex

import (
	"context"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestPlaylistSearchAndRecentProjection(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	a, _ := f.pls.Create(context.Background(), ownerUser.ID, "Night", []core.RatingKey{fightClub.Key})
	b, _ := f.pls.Create(context.Background(), ownerUser.ID, "Night Watch", []core.RatingKey{bbE1.Key})
	_, _ = f.pls.Create(context.Background(), friendUser.ID, "Night Secret", nil)
	// A recently played older list must precede a newer unplayed one.
	if err := f.srv.d.State.Scrobble(context.Background(), ownerUser.ID, a.Key); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/hubs/search?query=Night", "/library/search?query=Night"} {
		body := f.get(t, path).Body.String()
		if !strings.Contains(body, a.Key.String()) || !strings.Contains(body, b.Key.String()) || strings.Contains(body, "Night Secret") {
			t.Fatalf("%s: %s", path, body)
		}
	}
	body := f.get(t, "/hubs/search?query=Night&contentDirectoryID=1").Body.String()
	if !strings.Contains(body, a.Key.String()) || strings.Contains(body, b.Key.String()) {
		t.Fatal(body)
	}
	body = f.get(t, "/hubs/search?query=Night&contentDirectoryID=01").Body.String()
	if strings.Contains(body, `type="playlist" size="1"`) {
		t.Fatal(body)
	}
	body = f.get(t, "/hubs?contentDirectoryID=1,2").Body.String()
	if strings.Contains(body, "home.playlists") {
		t.Fatal(body)
	}
	for _, probe := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/playQueues", 200}, {"GET", "/playQueues?type=video", 400},
		{"POST", "/playlists/upload", 400}, {"POST", "/playlists/upload?path=x&sectionID=1", 500},
		{"DELETE", "/library/metadata/" + a.Key.String(), 200},
		{"PUT", "/library/metadata/" + a.Key.String() + "/prefs", 400},
		{"PUT", "/library/metadata/" + a.Key.String() + "/analyze", 400},
		{"GET", "/library/metadata/" + a.Key.String() + "/posters", 200},
		{"GET", "/library/metadata/" + a.Key.String() + "/themes", 200},
		{"PUT", "/playlists/" + a.Key.String() + "/items/1", 400},
		{"PUT", "/playlists/" + a.Key.String() + "/items/not-an-id", 404},
	} {
		if rec := f.request(t, probe.method, probe.path, nil); rec.Code != probe.status {
			t.Fatalf("%s %s: %d %s", probe.method, probe.path, rec.Code, rec.Body)
		}
	}
	if _, err := f.pls.Get(context.Background(), ownerUser.ID, a.Key); err != nil {
		t.Fatal("metadata DELETE removed playlist")
	}
	pr := &Request{User: &ownerUser}
	h, _, ok := f.srv.playlistsHubProjection(context.Background(), pr, 1)
	if !ok || len(h.Metadata) != 1 || h.Metadata[0].RatingKey != a.Key.String() {
		t.Fatalf("recent: %+v", h)
	}
}
