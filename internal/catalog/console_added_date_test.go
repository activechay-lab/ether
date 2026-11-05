package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestConsoleAddedDateUsesReleaseAndAiredProjection(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	now := c.now().Unix()
	key := core.ShowKey(10)
	if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_titles(key,type,tmdb_id,title,sort_title,released_ts,browsable,adult) VALUES(?,'show',10,'Series','Series',?,1,0)`, int64(key), now-10*86400); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET show_added_ts=? WHERE key=?`, now-3600, int64(key)); err != nil {
		t.Fatal(err)
	}
	threshold := now - int64(2*time.Hour/time.Second)
	for _, tt := range []struct {
		f     core.Filter
		total int
	}{
		{core.Filter{DateAddedFrom: threshold}, 1},
		{core.Filter{DateAddedTo: threshold}, 3},
	} {
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: tt.f, Limit: 10})
		if err != nil || page.Total != tt.total {
			t.Fatalf("filter %+v: %+v %v", tt.f, page, err)
		}
		if tt.f.DateAddedFrom > 0 && page.Items[0].Key != key {
			t.Fatalf("show's latest air date lost: %+v", page)
		}
	}
}

func TestConsoleAddedDateIncludesClassicFilmsBeforeEpoch(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	key := core.MovieKey(1)
	release := time.Date(1955, 6, 1, 0, 0, 0, 0, time.UTC).Unix()
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET released_ts=? WHERE key=?`, release, int64(key)); err != nil {
		t.Fatal(err)
	}
	from := time.Date(1950, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(1959, 12, 31, 23, 59, 59, 0, time.UTC).Unix()
	page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{LocalOnly: true, DateAddedFrom: from, DateAddedTo: to}, Sort: core.SortPopularity, Desc: true, Limit: 10, NoTotal: true, CursorPage: true})
	if err != nil || page.Total != 1 || page.Items[0].Key != key {
		t.Fatalf("classic film missing: %+v %v", page, err)
	}
}
