package mediabrowser

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

func TestShowsNextUpPagesFullRow(t *testing.T) {
	series := core.ShowKey(77)
	var episodes []frontend.Title
	for i := 1; i <= 5; i++ {
		episodes = append(episodes, frontend.Title{
			Key:       core.EpisodeKey(77, 1, i),
			Type:      "episode",
			Title:     fmt.Sprintf("Ep %d", i),
			ShowKey:   series,
			Season:    1,
			Episode:   i,
			ShowTitle: "Paged Series",
		})
	}

	for _, dialect := range []string{"jellyfin", "emby"} {
		t.Run(dialect, func(t *testing.T) {
			exp := frontendtest.Experience{
				RowsFunc: func(_ context.Context, req frontend.RowRequest) ([]frontend.Row, error) {
					items := episodes
					if req.Limit > 0 && req.Limit < len(items) {
						items = items[:req.Limit]
					}
					return []frontend.Row{{Purpose: frontend.RowNextUp, Items: items}}, nil
				},
				UserDataFunc: func(_ context.Context, _ *frontend.Viewer, keys []frontend.RatingKey) (map[frontend.RatingKey]frontend.WatchStatus, error) {
					st := map[frontend.RatingKey]frontend.WatchStatus{}
					for _, k := range keys {
						st[k] = frontend.WatchStatus{}
					}
					return st, nil
				},
			}
			inst := mountDialect(t, dialect, exp)
			w := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/Shows/NextUp?StartIndex=2&Limit=2", nil)
			r.Header.Set("X-Emby-Token", "grant")
			inst.Handler().ServeHTTP(w, r)

			var body struct {
				Items            []map[string]any
				TotalRecordCount int
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil {
				t.Fatalf("%s: %d %s", dialect, w.Code, w.Body.String())
			}
			if body.TotalRecordCount != 5 || len(body.Items) != 2 {
				t.Fatalf("%s: total=%d len=%d, want 5 and 2", dialect, body.TotalRecordCount, len(body.Items))
			}
			if body.Items[0]["Name"] != "Ep 3" || body.Items[1]["Name"] != "Ep 4" {
				t.Fatalf("%s: got [%v, %v], want [Ep 3, Ep 4]", dialect, body.Items[0]["Name"], body.Items[1]["Name"])
			}
		})
	}
}
