package catalog

import (
	"context"
	"errors"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestOwnerTagsAndMetadataFlags(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	key := core.MovieKey(2)
	query := core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, Tags: []string{"Friday"}}, Limit: 10}
	page, err := c.Browse(ctx, query)
	if err != nil || page.Total != 0 {
		t.Fatalf("empty tags: %+v %v", page, err)
	}
	if err := c.SetTitleTags(ctx, key, []string{" Friday ", "friday", "Favourites"}); err != nil {
		t.Fatal(err)
	}
	page, err = c.Browse(ctx, query)
	if err != nil || page.Total != 1 || page.Items[0].Key != key {
		t.Fatalf("tag generation invalidation: %+v %v", page, err)
	}
	tags, err := c.TitleTags(ctx, key)
	if err != nil || len(tags) != 2 {
		t.Fatalf("tags %v %v", tags, err)
	}
	if err := c.SetTitleTags(ctx, core.MovieKey(999), []string{"tag"}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("unknown title: %v", err)
	}
	if _, err := c.db.ExecContext(ctx, `INSERT INTO catalog_keywords(key,keyword) VALUES(?,'anime'); INSERT INTO catalog_genres(key,genre) VALUES(?,'Kids')`, int64(key), int64(key)); err != nil {
		t.Fatal(err)
	}
	// Kids must combine with the selected genre, rather than widening its OR list.
	for _, tt := range []struct {
		filter core.Filter
		total  int
	}{
		{core.Filter{Anime: true}, 1}, {core.Filter{Kids: true}, 1},
		{core.Filter{Kids: true, Genres: []string{"Thriller"}}, 0},
	} {
		tt.filter.Type = core.TypeMovie
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: tt.filter, Limit: 10})
		if err != nil || page.Total != tt.total {
			t.Fatalf("flag %+v: %+v %v", tt.filter, page, err)
		}
	}
}
