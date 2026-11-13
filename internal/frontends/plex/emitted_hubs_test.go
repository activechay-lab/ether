package plex

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestEveryEmittedHubResolves(t *testing.T) {
	rec := &contextualRecommender{fakeRecommender: fakeRecommender{
		section: map[core.MediaType][]core.Hub{core.TypeMovie: {
			{ID: "movie.trending", Title: "Trending", Type: core.TypeMovie, Items: []core.Title{fightClub, alien}},
			{ID: "movie.cluster.adventure.action", Title: "Adventure", Type: core.TypeMovie, Items: []core.Title{alien, fightClub}},
		}},
		related: []core.Hub{{ID: "movie.person.934", Title: "Actor", Type: core.TypeMovie, Items: []core.Title{fightClub, alien}}},
	}}
	f := newContextualFixture(t, rec)
	for _, product := range []string{"Plex for Samsung", "Plex for Android (TV)", "Plex for Apple TV"} {
		for _, endpoint := range []string{"/hubs/sections/1", "/hubs/metadata/" + fightClub.Key.String() + "/related"} {
			response := f.get(t, endpoint, "Accept", "application/json", "X-Plex-Product", product)
			var page struct {
				MediaContainer struct {
					Hub []struct {
						Key, HubIdentifier string
						Metadata           []struct{ RatingKey string }
					}
				}
			}
			if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
				t.Fatal(err)
			}
			if response.Code != 200 || len(page.MediaContainer.Hub) == 0 {
				t.Fatalf("%s: %d %s", endpoint, response.Code, response.Body.String())
			}
			for _, hub := range page.MediaContainer.Hub {
				for start, item := range hub.Metadata {
					// Exercise Samsung's bare identifier as well as the full emitted key.
					paths := []string{hub.Key}
					if endpoint == "/hubs/sections/1" {
						paths = append(paths, "/hubs/items/"+url.PathEscape(hub.HubIdentifier)+"?contentDirectoryID=1")
					} else {
						paths = append(paths, "/hubs/items/"+url.PathEscape(hub.HubIdentifier))
					}
					for _, path := range paths {
						got := f.get(t, path, "Accept", "application/json", "X-Plex-Product", product, "X-Plex-Container-Start", fmt.Sprint(start), "X-Plex-Container-Size", "1")
						var result struct {
							MediaContainer struct {
								Metadata []struct{ RatingKey string }
								Offset   int
							}
						}
						if err := json.Unmarshal(got.Body.Bytes(), &result); err != nil {
							t.Fatal(err)
						}
						if got.Code != 200 || len(result.MediaContainer.Metadata) != 1 || result.MediaContainer.Metadata[0].RatingKey != item.RatingKey || result.MediaContainer.Offset != start {
							t.Fatalf("%s %s page %d: %d %s", product, path, start, got.Code, got.Body.String())
						}
					}
				}
			}
		}
	}
}
