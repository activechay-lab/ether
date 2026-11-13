package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func TestSwiftfinSearchTypes(t *testing.T) {
	movie := core.Title{Key: core.MovieKey(1), Type: "movie", Title: "1917"}
	episode := core.Title{Key: core.EpisodeKey(2, 1, 1), Type: "episode", Title: "1917 episode"}
	collection := frontend.Collection{Key: core.CollectionKey(3), Title: "1917 Collection", ChildCount: 1}

	exp := frontendtest.Experience{
		SearchFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ string, q frontend.ItemQuery) (frontend.SearchPage, error) {
			var items []core.Title
			for _, title := range []core.Title{movie, episode} {
				allowed := len(q.Types) == 0 && len(q.UnservedTypes) == 0
				for _, typ := range q.Types {
					if typ == title.Type {
						allowed = true
					}
				}
				for _, typ := range q.ExcludedTypes {
					if typ == title.Type {
						allowed = false
					}
				}
				if allowed {
					items = append(items, title)
				}
			}
			return frontend.SearchPage{Page: frontend.Page{Items: items, Total: len(items)}}, nil
		},
		CollectionsFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, q frontend.CollectionQuery) (frontend.CollectionPage, error) {
			if q.Query != "" && !strings.Contains(strings.ToLower(collection.Title), strings.ToLower(q.Query)) {
				return frontend.CollectionPage{}, nil
			}
			return frontend.CollectionPage{Items: []frontend.Collection{collection}, Total: 1}, nil
		},
		ChildrenFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, key frontend.RatingKey, _ frontend.ItemQuery) (frontend.Page, error) {
			if key != collection.Key {
				t.Fatalf("wrong collection key %v", key)
			}
			return frontend.Page{Items: []core.Title{movie}, Total: 1}, nil
		},
	}
	inst := mountJellyfin(t, exp)
	call := func(path, field string) (int, []map[string]any) {
		t.Helper()
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("X-Emby-Token", "grant")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		var body map[string]json.RawMessage
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
			t.Fatal(w.Code, w.Body.String())
		}
		var items []map[string]any
		var total int
		if err := json.Unmarshal(body[field], &items); err != nil {
			t.Fatal(err, w.Body.String())
		}
		json.Unmarshal(body["TotalRecordCount"], &total)
		return total, items
	}
	for _, endpoint := range []string{"/Items", "/Search/Hints"} {
		field := "Items"
		if endpoint == "/Search/Hints" {
			field = "SearchHints"
		}
		for _, tc := range []struct {
			types, exclude, term, kind string
			count                      int
		}{
			{"Movie", "", "1917", "Movie", 1}, {"Episode", "", "1917", "Episode", 1}, {"Series", "", "1917", "", 0},
			{"BoxSet", "", "1917", "BoxSet", 1}, {"BoxSet,UserView", "", "1917", "BoxSet", 1}, {"BoxSet", "", "missing", "", 0},
			{"MusicArtist", "", "1917", "", 0}, {"MusicAlbum", "", "1917", "", 0}, {"Audio", "", "1917", "", 0}, {"Person", "", "1917", "", 0},
			{"Movie,Episode", "Episode", "1917", "Movie", 1}, {"BoxSet", "BoxSet", "1917", "", 0},
		} {
			total, items := call(endpoint+"?Recursive=true&SearchTerm="+tc.term+"&IncludeItemTypes="+tc.types+"&ExcludeItemTypes="+tc.exclude, field)
			if total != tc.count || len(items) != tc.count {
				t.Fatalf("%s %s: total %d items %v", endpoint, tc.types, total, items)
			}
			for _, item := range items {
				if item["Type"] != tc.kind {
					t.Fatalf("%s: %v", tc.types, item)
				}
			}
		}
	}
	_, views := call("/UserViews", "Items")
	var collectionID string
	for _, view := range views {
		if view["CollectionType"] == "boxsets" {
			collectionID, _ = view["Id"].(string)
		}
	}
	if collectionID == "" {
		t.Fatal("Collections view missing", views)
	}
	total, items := call("/Items?ParentId="+collectionID, "Items")
	if total != 1 || items[0]["Type"] != "BoxSet" {
		t.Fatal(items)
	}
	total, items = call("/Items?ParentId="+ids.RatingKeyGUID(collection.Key), "Items")
	if total != 1 || items[0]["Name"] != "1917" {
		t.Fatal(items)
	}
}
