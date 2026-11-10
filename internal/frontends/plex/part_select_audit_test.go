package plex

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// A failed track change must preserve both picks, even when the other
// supplied track is valid. Non-numeric values remain PMS's ignored inputs.
func TestPartSelectionRejectsInvalidTracksAtomically(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	key := fightClub.Key
	en := core.StreamInfo{Index: 1, Type: "audio", Language: "eng", Codec: "aac"}
	fr := core.StreamInfo{Index: 2, Type: "audio", Language: "fra", Codec: "aac"}
	sub := core.StreamInfo{Index: 3, Type: "subtitle", Language: "eng", Codec: "srt"}
	english := f.bindStream(key, 1, en)
	french := f.bindStream(key, 2, fr)
	subtitle := f.bindStream(key, 3, sub)
	other := f.bindStream(alien.Key, 1, en)
	target := "/library/parts/" + key.String()
	rec := f.request(t, http.MethodPost, fmt.Sprintf("%s?audioStreamID=%d&subtitleStreamID=%d", target, english, subtitle), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST selection: %d %s", rec.Code, rec.Body.String())
	}
	for _, q := range []string{
		"audioStreamID=0", "audioStreamID=999", fmt.Sprintf("audioStreamID=%d", subtitle),
		fmt.Sprintf("audioStreamID=%d", other),
		fmt.Sprintf("audioStreamID=%d&subtitleStreamID=999", french),
		"audioStreamID=999&subtitleStreamID=0",
		"audioStreamID=-1&subtitleStreamID=999",
		"audioStreamID=999&subtitleStreamID=-1",
		fmt.Sprintf("subtitleStreamID=%d", french),
	} {
		rec := f.request(t, http.MethodPut, target+"?"+q, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", q, rec.Code)
		}
		selected := f.srv.selections.get(ownerUser.ID, int64(key))
		if !selected.audioSet || !selected.subtitleSet || selected.audio != 1 || selected.subtitle != 3 {
			t.Fatalf("%s changed picks: %+v", q, selected)
		}
	}
	rec = f.request(t, http.MethodPost, target+"?audioStreamID=-1&subtitleStreamID=-1", nil)
	selected := f.srv.selections.get(ownerUser.ID, int64(key))
	if rec.Code != http.StatusOK || selected.audioSet || selected.subtitleSet {
		t.Fatalf("paired default reset: status=%d selection=%+v", rec.Code, selected)
	}
	rec = f.request(t, http.MethodPut, target+"?audioStreamID=&subtitleStreamID=abc", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("ignored values: %d", rec.Code)
	}
	rec = f.request(t, http.MethodPut, target+"?subtitleStreamID=0", nil)
	if rec.Code != http.StatusOK || f.srv.selections.get(ownerUser.ID, int64(key)).subtitle != -1 {
		t.Fatal("subtitle None was not accepted")
	}
}

func TestStreamWriteMethodRefusals(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	stream := f.bindStream(fightClub.Key, 3, core.StreamInfo{Index: 3, Type: "subtitle", Codec: "srt"})
	for _, tc := range []struct {
		method, id string
		status     int
	}{
		{http.MethodPut, fmt.Sprint(stream), 200},
		{http.MethodDelete, fmt.Sprint(stream), 403},
		{http.MethodPost, fmt.Sprint(stream), 404},
		{http.MethodPut, "999", 400},
		{http.MethodPut, "abc", 400},
		{http.MethodDelete, "999", 404},
		{http.MethodDelete, "abc", 400},
	} {
		rec := f.request(t, tc.method, "/library/streams/"+tc.id, nil)
		if rec.Code != tc.status {
			t.Errorf("%s %s: got %d, want %d: %s", tc.method, tc.id, rec.Code, tc.status, rec.Body.String())
		}
	}
}
