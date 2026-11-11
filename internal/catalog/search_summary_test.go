package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSearchSummaryProjection(t *testing.T) {
	for _, indexed := range []bool{false, true} {
		t.Run(fmt.Sprintf("indexed=%t", indexed), func(t *testing.T) {
			c := newChunkTestCatalog(t)
			t.Logf("backend: %s", store.DialectOf(c.db))
			ctx := context.Background()
			var titles []core.Title
			for i := int64(1); i <= 200; i++ {
				title := core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, IDs: core.IDs{TMDB: i}, Title: fmt.Sprintf("Star %d", i), Summary: "Synopsis", Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Year: 2000, Popularity: 100, VoteCount: 1000, PosterPath: "/poster.jpg", Genres: []string{"Drama"}, Keywords: []string{"space"}}
				for n := int64(1); n <= 20; n++ {
					title.Cast = append(title.Cast, core.Person{TMDB: n, Name: fmt.Sprintf("Actor %d", n), ThumbPath: "/actor.jpg"})
				}
				titles = append(titles, title)
			}
			if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true, detail: true}); err != nil {
				t.Fatal(err)
			}
			if indexed {
				if c.searchIndex(ctx, true) == nil {
					t.Fatal("index unavailable")
				}
			} else {
				c.searchIx.next = c.now().Add(time.Hour)
			}
			c.cacheMu.Lock()
			c.titleCache = nil
			c.cacheMu.Unlock()
			measured, stats := store.WithQueryStats(ctx)
			full, err := c.Search(measured, "star", []core.MediaType{core.TypeMovie}, 200)
			if err != nil || len(full) != 200 {
				t.Fatalf("full results %d %v", len(full), err)
			}
			t.Logf("full 200 candidate window: queries=%d rows=%d", stats.Queries(), stats.Rows())
			if stats.Rows() < 4000 {
				t.Fatal("fixture failed to expose full credit hydration")
			}

			// The full read has populated the detail cache. Summary mode must neither
			// reuse its credits nor append fresh facets to cached facet slices.
			summaryCtx := core.WithSearchSummaryFacts(ctx)
			readCtx, summaryStats := store.WithQueryStats(summaryCtx)
			summary, err := c.Search(readCtx, "star", []core.MediaType{core.TypeMovie}, 200)
			if err != nil || len(summary) != len(full) {
				t.Fatalf("summary results %d: %v", len(summary), err)
			}
			for i, got := range summary {
				want := full[i]
				if got.Key != want.Key || got.Type != want.Type || got.IDs != want.IDs || got.Title != want.Title || got.Summary != want.Summary || got.Year != want.Year || got.PosterPath != want.PosterPath || got.SearchScore != want.SearchScore || !reflect.DeepEqual(got.Genres, want.Genres) || !reflect.DeepEqual(got.Keywords, want.Keywords) {
					t.Fatalf("summary metadata/order mismatch at %d: %+v vs %+v", i, got, want)
				}
				if len(got.Cast) != 0 || len(want.Cast) != 20 {
					t.Fatalf("summary/full credits: %d/%d", len(got.Cast), len(want.Cast))
				}
			}
			t.Logf("summary 200 candidate window: queries=%d rows=%d", summaryStats.Queries(), summaryStats.Rows())
			if summaryStats.Rows() >= 1000 || summaryStats.Writes() != 0 {
				t.Fatalf("summary work: %d rows, %d writes", summaryStats.Rows(), summaryStats.Writes())
			}
			// Bypass generation invalidation deliberately: fresh policy facets must
			// still win over a warm full-title cache.
			key := titles[0].Key
			if _, err := c.db.ExecContext(ctx, `UPDATE catalog_genres SET genre=? WHERE key=?`, "Blocked", int64(key)); err != nil {
				t.Fatal(err)
			}
			summary, err = c.Search(summaryCtx, "star", []core.MediaType{core.TypeMovie}, 200)
			if err != nil {
				t.Fatal(err)
			}
			fresh := false
			for _, title := range summary {
				if title.Key == key {
					fresh = reflect.DeepEqual(title.Genres, []string{"Blocked"})
				}
			}
			if !fresh {
				t.Fatal("summary reused stale full-title facets")
			}
			setTestPolicies(t, c, []core.LibraryExclusion{{MediaType: "movie", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "is", Value: json.RawMessage(`"Blocked"`)}}}}, "")
			summary, err = c.Search(summaryCtx, "star", []core.MediaType{core.TypeMovie}, 200)
			if err != nil || len(summary) != 199 {
				t.Fatalf("active policy results %d: %v", len(summary), err)
			}
			for _, title := range summary {
				if title.Key == key {
					t.Fatal("active catalogue policy bypassed")
				}
			}
			// A cold summary read must not poison a subsequent full-detail read.
			c.UpdateConfig(config.Catalog{})
			c.cacheMu.Lock()
			c.titleCache = nil
			c.cacheMu.Unlock()
			if _, err = c.Search(summaryCtx, "star", []core.MediaType{core.TypeMovie}, 200); err != nil {
				t.Fatal(err)
			}
			c.cacheMu.Lock()
			cached := len(c.titleCache)
			c.cacheMu.Unlock()
			if cached != 0 {
				t.Fatal("summary populated full-title cache")
			}
			st, err := c.loadByKey(ctx, titles[1].Key)
			if err != nil || len(st.t.Cast) != 20 {
				t.Fatalf("full detail after summary lost credits: %v", err)
			}
		})
	}
}
