package plex

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestSubscriptionModes(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()

	for _, tc := range []struct {
		name      string
		key       string
		wantType  string
		wantTitle string
		wantSec   int
	}{
		{"movie", fightClub.Key.String(), "movie", fightClub.Title, 1},
		{"show", breakingBad.Key.String(), "show", breakingBad.Title, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.get(t, "/library/metadata/"+tc.key+"/subscription/modes", jsonAccept...)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var got struct {
				MediaContainer struct {
					Size             int    `json:"size"`
					LibrarySectionID int    `json:"librarySectionID"`
					Type             string `json:"type"`
					Directory        []struct {
						RatingKey string `json:"ratingKey"`
						Title     string `json:"title"`
						Type      string `json:"type"`
					} `json:"Directory"`
				} `json:"MediaContainer"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v (%s)", err, rec.Body.String())
			}
			mc := got.MediaContainer
			if mc.Size != 1 || len(mc.Directory) != 1 || mc.LibrarySectionID != tc.wantSec || mc.Type != tc.wantType {
				t.Fatalf("container = %+v", mc)
			}
			d := mc.Directory[0]
			if d.RatingKey != tc.key || d.Title != tc.wantTitle || d.Type != tc.wantType {
				t.Fatalf("directory = %+v", d)
			}
		})
	}

	if rec := f.get(t, "/library/metadata/999999999/subscription/modes", jsonAccept...); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown key status = %d, want 404", rec.Code)
	}
}
