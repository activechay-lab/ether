package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestXtreamLargeCatalogueKeyset(t *testing.T) {
	c := largeCatalogue(t, 100001)
	ctx := context.Background()
	after := core.RatingKey(0)
	count, pages := 0, 0
	start := time.Now()
	for {
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Sort: core.SortKey, AfterKey: after, Limit: 250, NoTotal: true})
		if err != nil {
			t.Fatal(err)
		}
		pages++
		if len(page.Items) > 250 {
			t.Fatal("unbounded page")
		}
		for _, item := range page.Items {
			if item.Key <= after {
				t.Fatal("cursor failed to advance")
			}
			after = item.Key
			count++
		}
		if len(page.Items) < 250 {
			break
		}
	}
	elapsed := time.Since(start)
	if count != 100001 || pages != 401 {
		t.Fatalf("count=%d pages=%d", count, pages)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("keyset walk too slow: %v", elapsed)
	}
	t.Logf("100001 actual SQL catalogue rows: %v, %d bounded pages", elapsed, pages)
}
