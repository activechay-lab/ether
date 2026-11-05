package plex

import (
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"testing"
)

func TestStateMutationRefusalOrder(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.srv.d.State = state.New(store.OpenTest(t), state.Options{})
	k := fightClub.Key.String()
	cases := []struct {
		path   string
		status int
	}{
		{"/:/scrobble?key=" + k, 400},
		{"/:/scrobble?identifier=other&key=" + k, 404},
		{"/:/scrobble?identifier=com.plexapp.plugins.library&key=/library/metadata/" + k, 404},
		{"/:/rate?identifier=other&key=" + k + "&rating=bad", 400},
		{"/:/rate?identifier=com.plexapp.plugins.library&key=/library/metadata/" + k + "&rating=7", 500},
		{"/:/scrobble?identifier=com.plexapp.plugins.library&key=1000000000999999", 404},
		{"/:/scrobble?identifier=com.plexapp.plugins.library&key=" + k, http.StatusOK},
		{"/:/rate?identifier=com.plexapp.plugins.library&key=" + k + "&rating=7", http.StatusOK},
	}
	for _, c := range cases {
		r := f.get(t, c.path)
		if r.Code != c.status {
			t.Errorf("%s status %d want %d: %s", c.path, r.Code, c.status, r.Body.String())
		}
		if c.status == 200 && (r.Body.Len() != 0 || r.Header().Get("Content-Type") != "text/html") {
			t.Errorf("mutation body/type %s %v", r.Body.String(), r.Header())
		}
	}
}
