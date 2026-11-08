package catalog_test

import (
	"context"
	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"testing"
	"time"
)

func TestUpcomingMoviesOptIn(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	movie := core.Title{Key: core.MovieKey(987654), Type: core.TypeMovie, Title: "Future", Released: now.Add(72 * time.Hour), Popularity: 50}
	if err = c.UpsertTitle(ctx, movie, false); err != nil {
		t.Fatal(err)
	}
	if catalog.MeetsThreshold(config.Catalog{}, movie, now) {
		t.Fatal("future movies enabled by default")
	}
	for _, enabled := range []bool{false, true, false} {
		c.UpdateConfig(config.Catalog{ShowUpcomingMovies: enabled, ReleaseMode: config.ModeDigital})
		rows, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}})
		if err != nil {
			t.Fatal(err)
		}
		_, getErr := c.CachedGet(ctx, movie.Key)
		if enabled && getErr != nil {
			t.Fatalf("upcoming metadata lookup: %v", getErr)
		}
		if !enabled && getErr == nil {
			t.Fatal("future metadata visible while disabled")
		}
		want := 0
		if enabled {
			want = 1
		}
		if len(rows.Items) != want {
			t.Fatalf("enabled %v: %+v", enabled, rows)
		}
	}
}
