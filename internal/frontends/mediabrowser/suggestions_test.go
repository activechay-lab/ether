package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func TestSuggestionsViewerFiltersAndPaging(t *testing.T) {
	for _, protocol := range []string{"jellyfin", "emby"} {
		t.Run(protocol, func(t *testing.T) {
			viewer := &frontend.Viewer{ID: 1}
			calls := 0
			exp := frontendtest.Experience{RowsFunc: func(_ context.Context, req frontend.RowRequest) ([]frontend.Row, error) {
				calls++
				if req.Viewer.ID != 1 || req.Device.ID != "phone" || req.Placement != frontend.PlacementHome {
					t.Fatalf("scope %+v", req)
				}
				return []frontend.Row{
					{ID: "continue", Purpose: frontend.RowContinue, Items: []frontend.Title{{Key: 99, Type: "movie"}}},
					{ID: "picks", Purpose: frontend.RowRecommendation, Items: []frontend.Title{{Key: 1, Type: "movie", Title: "First"}, {Key: 2, Type: "show", Title: "Series"}, {Key: 3, Type: "movie", Title: "Third"}}},
					{ID: "duplicate", Purpose: frontend.RowRecommendation, Items: []frontend.Title{{Key: 1, Type: "movie"}}},
				}, nil
			}}
			host := frontend.Host{Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }}, Experience: exp}
			var inst frontend.Instance
			var err error
			if protocol == "jellyfin" {
				inst, err = (Jellyfin{MachineID: "test"}).Mount(context.Background(), host)
			} else {
				inst, err = (Emby{MachineID: "test"}).Mount(context.Background(), host)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer inst.Close(context.Background())
			for _, prefix := range []string{"", "/" + protocol} {
				call := func(query string) *httptest.ResponseRecorder {
					r := httptest.NewRequest("GET", prefix+"/Items/Suggestions"+query, nil)
					r.Header.Set("X-Emby-Token", "token")
					r.Header.Set("X-Emby-Device-Id", "phone")
					w := httptest.NewRecorder()
					inst.Handler().ServeHTTP(w, r)
					return w
				}
				for _, tc := range []struct {
					query        string
					count, total int
					name         string
				}{
					{"?type=Movie,Series&limit=6&enableTotalRecordCount=true", 3, 3, "First"},
					{"?type=Movie&mediaType=Video&startIndex=1&limit=1&enableTotalRecordCount=true", 1, 2, "Third"},
					{"?type=Series&limit=1", 1, 0, "Series"},
					{"?mediaType=Audio", 0, 0, ""}, {"?limit=0&enableTotalRecordCount=true", 0, 3, ""},
					{"?startIndex=100&enableTotalRecordCount=true", 0, 3, ""},
				} {
					w := call(tc.query)
					var result struct {
						Items            []struct{ Name string }
						TotalRecordCount int
					}
					if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &result) != nil || result.Items == nil || len(result.Items) != tc.count || result.TotalRecordCount != tc.total {
						t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body)
					}
					if tc.name != "" && result.Items[0].Name != tc.name {
						t.Fatal(result)
					}
				}
				before := calls
				other, _ := ids.GUID(ids.User, 2)
				if w := call("?userId=" + other); w.Code != 401 || calls != before {
					t.Fatalf("scope %d rows=%d", w.Code, calls)
				}
				for _, query := range []string{"?limit=-1", "?startIndex=oops", "?limit=1&limit=2", "?enableTotalRecordCount=nope"} {
					if w := call(query); w.Code != 400 {
						t.Fatalf("%s: %d", query, w.Code)
					}
				}
			}
		})
	}
}
