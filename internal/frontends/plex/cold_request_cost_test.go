package plex

import (
	"context"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Measure real SQL on fresh catalogues, including URI expansion and rendering.
// Unrelated library rows must not increase either route's statement budget.
func TestColdQueueAndPostplayQueryBudget(t *testing.T) {
	for _, route := range []string{"queue", "postplay"} {
		t.Run(route, func(t *testing.T) {
			var small int64
			for _, unrelated := range []int{0, 1000} {
				db := store.OpenTest(t)
				c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
				if err != nil {
					t.Fatal(err)
				}
				for _, title := range []core.Title{breakingBad, bbS1, bbE1, bbE2} {
					if title.Released.IsZero() {
						title.Released = fixedNow.Add(-48 * time.Hour)
					}
					if err := c.UpsertTitle(context.Background(), title, true); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := db.Exec(`UPDATE catalog_titles SET detail=1,detail_at=?,fetched_at=?`, fixedNow.Unix(), fixedNow.Unix()); err != nil {
					t.Fatal(err)
				}
				if unrelated > 0 {
					_, err := db.Exec(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<?) INSERT INTO catalog_titles(key,type,show_key,season,episode,title,released_ts) SELECT ?+n,'episode',?,1,n,'Unrelated',? FROM seq`, unrelated, int64(core.EpisodeKey(999, 1, 1)), core.ShowKey(999), fixedNow.Add(-24*time.Hour).Unix())
					if err != nil {
						t.Fatal(err)
					}
				}
				f := newFixture(func(d *testServerDeps) { d.Catalog = c; d.DB = db; d.DisableEnrichment = true; d.Recommender = nil })
				ctx, stats := store.WithQueryStats(context.Background())
				method, path := "GET", "/hubs/metadata/"+bbE1.Key.String()+"/postplay"
				if route == "queue" {
					method = "POST"
					path = "/playQueues?" + url.Values{"type": {"video"}, "continuous": {"1"}, "uri": {"/library/metadata/" + bbE1.Key.String()}}.Encode()
				}
				req := httptest.NewRequest(method, path, nil).WithContext(ctx)
				req.Header.Set("X-Plex-Token", ownerToken)
				req.Header.Set("Accept", "application/json")
				req.Header.Set("X-Plex-Client-Identifier", "cold-budget")
				response := httptest.NewRecorder()
				f.srv.ServeHTTP(response, req)
				f.srv.Close()
				if response.Code != 200 {
					t.Fatalf("%s: %d %s", route, response.Code, response.Body)
				}
				t.Logf("%s unrelated=%d queries=%d rows=%d", route, unrelated, stats.Queries(), stats.Rows())
				if unrelated == 0 {
					small = stats.Queries()
				} else if stats.Queries() > small {
					t.Fatalf("library growth increased statements: %d -> %d", small, stats.Queries())
				}
				if stats.Queries() > 65 {
					t.Fatalf("%s exceeded fixed SQL budget: %d", route, stats.Queries())
				}
			}
		})
	}
}
