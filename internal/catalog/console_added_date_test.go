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
