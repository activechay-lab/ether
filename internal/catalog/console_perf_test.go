package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"os"
	"sort"
	"testing"
	"time"
)

// Run explicitly: ETHER_CATALOG_PERF=1 go test ./internal/catalog -run TestConsole600K -count=1 -v.
// Cold page caches, warm database pages. Counts are deliberately excluded from
// interactive pages and refreshed independently. A 150ms p95 is the target;
// this test reports violations instead of hiding them behind warmed result caches.
func TestConsole600K(t *testing.T) {
	if os.Getenv("ETHER_CATALOG_PERF") != "1" {
		t.Skip("set ETHER_CATALOG_PERF=1 for the 600k-title performance fixture")
	}
	c := largeCatalogue(t, 0)
	// Bulk fixture import defers FTS maintenance, then rebuilds the same index.
	// All production browse indexes and release-policy triggers remain active.
	if _, err := c.db.Exec(`DROP TRIGGER catalog_titles_fts_insert`); err != nil {
		t.Fatal(err)
	}
	t.Log("loading 600k fixture")
	if _, err := c.db.ExecContext(context.Background(), `WITH RECURSIVE seq(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i<600000)
      INSERT INTO catalog_titles(key,type,tmdb_id,title,sort_title,year,released_ts,popularity,rating,vote_count,runtime_sec,language,browsable,adult)
      SELECT ?+i,'movie',i,printf('Title %06d',i),printf('Title %06d',i),1970+(i%55),1600000000,600000-i,4+(i%60)/10.0,i%10000,3600+(i%180)*60,CASE WHEN i%3=0 THEN 'fr' ELSE 'en' END,1,0 FROM seq`, int64(core.MovieKey(0))); err != nil {
		t.Fatal(err)
	}
	t.Log("rebuilding FTS")
	if _, err := c.db.Exec(`INSERT INTO catalog_titles_fts(catalog_titles_fts) VALUES('rebuild'); CREATE TRIGGER catalog_titles_fts_insert AFTER INSERT ON catalog_titles BEGIN INSERT INTO catalog_titles_fts(rowid,title,original_title) VALUES(new.key,new.title,new.original_title); END;
      INSERT INTO catalog_genres(key,genre) SELECT key,CASE WHEN tmdb_id%2=0 THEN 'Thriller' ELSE 'Comedy' END FROM catalog_titles; ANALYZE;`); err != nil {
		t.Fatal(err)
	}
	t.Log("fixture ready")
	cases := []struct {
		name string
		q    core.BrowseQuery
	}{
		{"popular", core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Sort: core.SortPopularity, Desc: true, Limit: 49, NoTotal: true}},
		{"90s-thriller-under2h-rating7", core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, YearFrom: 1990, YearTo: 1999, Genres: []string{"Thriller"}, MinRating: 7, MaxRuntime: 2 * time.Hour}, Sort: core.SortPopularity, Desc: true, Limit: 49, NoTotal: true}},
		{"rare-language-votes", core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, Languages: []string{"fr"}, MinVotes: 9900}, Sort: core.SortRating, Desc: true, Limit: 49, NoTotal: true}},
		{"or-secondary", core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, Group: &core.FilterGroup{Operator: "or", Children: []core.FilterGroup{{Filter: &core.Filter{YearTo: 1971}}, {Filter: &core.Filter{MinRating: 9.8}}}}}, Sort: core.SortYear, Desc: true, SecondarySorts: []core.SortSpec{{Sort: core.SortTitle}}, Limit: 49, NoTotal: true}},
		{"zero-match", core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, YearFrom: 2199, Genres: []string{"Thriller"}, MinRating: 9.9}, Sort: core.SortPopularity, Desc: true, Limit: 49, NoTotal: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.q.Filter.LocalOnly = true
			times := make([]time.Duration, 31)
			for i := range times {
				c.cacheMu.Lock()
				c.browseCache = map[string]cachedBrowse{}
				c.cacheMu.Unlock()
				start := time.Now()
				_, err := c.Browse(context.Background(), tc.q)
				times[i] = time.Since(start)
				if err != nil {
					t.Fatal(err)
				}
			}
			sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
			p50, p95 := times[15], times[29]
			t.Logf("600k cold-result-cache n=31 p50=%s p95=%s target=150ms", p50, p95)
			if p95 > 150*time.Millisecond {
				t.Errorf("p95 exceeds interactive budget: %s", p95)
			}
		})
	}
}
