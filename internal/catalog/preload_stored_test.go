package catalog

import (
	"context"
	"reflect"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

// PreloadStored caches what loadByKey would read, detail time included, so a
// list's series checks are cache hits that Get still judges fresh or stale
// exactly as before.
func TestPreloadStoredMatchesLoadByKey(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := c.now()
	var keys []core.RatingKey
	for id := int64(1); id <= 3; id++ {
		show := core.Title{Key: core.ShowKey(id), Type: core.TypeShow, Title: "Show", Genres: []string{"Drama"}, Released: now.AddDate(-1, 0, 0)}
		if err := c.upsert(ctx, show, upsertOpts{browsable: true, detail: id != 2}); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, show.Key)
	}
	want := map[core.RatingKey]*storedTitle{}
	for _, k := range keys {
		st, err := c.loadByKey(ctx, k)
		if err != nil {
			t.Fatal(err)
		}
		want[k] = st
	}
	c.cacheMu.Lock()
	c.titleCache = nil
	c.cacheMu.Unlock()
	c.PreloadStored(ctx, append(keys, keys[0], core.ShowKey(99)))
	// Served from the cache: the rows are gone, the generation is not.
	if _, err := c.db.ExecContext(ctx, `DELETE FROM catalog_titles WHERE type='show'`); err != nil {
		t.Fatal(err)
	}
	for _, k := range keys {
		got, err := c.loadByKey(ctx, k)
		if err != nil {
			t.Fatalf("%v not preloaded: %v", k, err)
		}
		if !reflect.DeepEqual(got.t, want[k].t) || !got.detailAt.Equal(want[k].detailAt) || got.detail != want[k].detail {
			t.Fatalf("%v preloaded %+v, loaded %+v", k, got, want[k])
		}
	}
}
