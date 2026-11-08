package plex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type shelfRow struct {
	HubIdentifier string `json:"hubIdentifier"`
	Context       string `json:"context"`
	Key           string `json:"key"`
	Title         string `json:"title"`
}

func shelfRows(t *testing.T, body []byte) []shelfRow {
	t.Helper()
	var v struct {
		MediaContainer struct {
			Hub []shelfRow `json:"Hub"`
		} `json:"MediaContainer"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatal(err)
	}
	return v.MediaContainer.Hub
}

// Infuse Direct Mode draws a library's promoted rows only when they are
// PMS's own kinds and folds any other row's items into the next row it
// draws. Its per-library request must lead with Recently Added, carry every
// Ether row as a promoted collection shelf, and show Home rows once.
func TestInfusePromotedRowsAreCollectionShelves(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	f.srv.d.Recommender = &fakeRecommender{
		home:    []core.Hub{{ID: "home.trending", Title: "Trending Now", Promoted: true, Items: []core.Title{fightClub, alien, breakingBad}}},
		section: map[core.MediaType][]core.Hub{core.TypeMovie: {{ID: "movie.toprated", Title: "Top Rated", Type: core.TypeMovie, Promoted: true, Items: []core.Title{alien}}}},
	}
	infuse := append([]string{"X-Plex-Product", "Infuse-Direct", "X-Plex-Device", "Apple TV"}, jsonAccept...)
	const q = "/hubs/promoted?count=20&excludeContinueWatching=1&contentDirectoryID="

	movies := shelfRows(t, f.get(t, q+"1", infuse...).Body.Bytes())
	if len(movies) < 3 || movies[0].HubIdentifier != "movie.recentlyadded.1" {
		t.Fatalf("Recently Added must lead the library's rows: %+v", movies)
	}
	titles := map[string]bool{}
	for _, row := range movies[1:] {
		if !strings.HasPrefix(row.HubIdentifier, "custom.collection.1.") || row.Context != "hub.custom.collection" {
			t.Fatalf("row is not a promoted collection shelf: %+v", row)
		}
		titles[row.Title] = true
		if rec := f.get(t, row.Key, jsonAccept...); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ratingKey"`) {
			t.Fatalf("shelf %s key %s: %d", row.Title, row.Key, rec.Code)
		}
	}
	if !titles["Trending Now"] || !titles["Top Rated"] {
		t.Fatalf("Ether rows missing: %v", titles)
	}
	again := shelfRows(t, f.get(t, q+"1", infuse...).Body.Bytes())
	for i := range movies {
		if i >= len(again) || again[i].HubIdentifier != movies[i].HubIdentifier {
			t.Fatalf("shelf identifiers must be stable: %+v then %+v", movies, again)
		}
	}
	for _, row := range shelfRows(t, f.get(t, q+"2", infuse...).Body.Bytes()) {
		if row.Title == "Trending Now" {
			t.Fatalf("Home row repeated in the second library: %+v", row)
		}
	}

	// Plex's own apps keep Ether's row identities.
	plex := shelfRows(t, f.get(t, q+"1", jsonAccept...).Body.Bytes())
	for _, row := range plex {
		if strings.HasPrefix(row.HubIdentifier, "custom.collection.") {
			t.Fatalf("non-Infuse client got a collection shelf: %+v", row)
		}
	}
}
