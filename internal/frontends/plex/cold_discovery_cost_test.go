package plex

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type slowDiscoveryTMDB struct{ catalog.TMDB }

func (*slowDiscoveryTMDB) SearchMulti(ctx context.Context, _ string, _ int) (*tmdb.Paged[tmdb.MultiResult], error) {
	select {
	case <-time.After(1500 * time.Millisecond):
	case <-ctx.Done():
	}
	return nil, errors.New("offline")
}
func (*slowDiscoveryTMDB) MovieDetails(ctx context.Context, _ int64) (*tmdb.MovieDetails, error) {
	select {
	case <-time.After(1500 * time.Millisecond):
	case <-ctx.Done():
	}
	return nil, errors.New("offline")
}

// Cold local responses on a 50k-title mirror must survive a slow provider.
func TestColdDiscoveryLocalResponse(t *testing.T) {
	for _, immediate := range []bool{false, true} {
		name := "previous-policy"
		if immediate {
			name = "immediate-policy"
		}
		t.Run(name, func(t *testing.T) {
			coldDiscoveryResponse(t, immediate)
		})
	}
}

func coldDiscoveryResponse(t *testing.T, immediate bool) {
	db := store.OpenTest(t)
	c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return fixedNow }))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`WITH RECURSIVE seq(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM seq WHERE n<50000) INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,popularity,poster) SELECT ?+n,'movie',printf('Local Feature %06d',n),printf('Local Feature %06d',n),?,1,n,'/poster.jpg' FROM seq`, int64(core.MovieKey(1))-1, fixedNow.Add(-24*time.Hour).Unix())
	if err != nil {
		t.Fatal(err)
	}
	c.SetTMDB(&slowDiscoveryTMDB{})
	f := newFixture(func(d *testServerDeps) {
		d.DB = db
		d.Catalog = c
		d.DisableEnrichment = true
		d.HomeCache = &testHomeCache{NoProviderWait: func(ctx context.Context) context.Context {
			if !immediate {
				return ctx
			}
			return core.WithLocalResponse(catalog.WithReadWaitBudget(ctx, 0))
		}}
	})
	defer f.srv.Close()
	defer c.WaitRefreshes()
	rec := f.srv.recommender().(*fakeRecommender)
	titles, err := c.CachedTitles(context.Background(), []core.RatingKey{core.MovieKey(1), core.MovieKey(2)})
	if err != nil {
		t.Fatal(err)
	}
	rec.home = []core.Hub{{ID: "collection.local", Title: "Local", Type: core.TypeMovie, Items: titles}}
	rec.section[core.TypeMovie] = rec.home
	for _, target := range []struct{ name, path string }{
		{"search", "/hubs/search?query=Local+Feature+00000&limit=30"},
		{"metadata", "/library/metadata/" + core.MovieKey(3).String()},
		{"promoted", "/hubs/promoted?count=20&contentDirectoryID=1&includeCollections=1&pinnedContentDirectoryID=1"},
	} {
		ctx, stats := store.WithQueryStats(context.Background())
		req := httptest.NewRequest("GET", target.path, nil).WithContext(ctx)
		req.Header.Set("Accept", "application/json")
		req.Header.Set("X-Plex-Token", ownerToken)
		req.Header.Set("X-Plex-Client-Identifier", "local-cost")
		response := httptest.NewRecorder()
		start := time.Now()
		f.srv.ServeHTTP(response, req)
		elapsed := time.Since(start)
		t.Logf("%s 50000 titles elapsed=%s queries=%d rows=%d", target.name, elapsed, stats.Queries(), stats.Rows())
		if response.Code != 200 || !strings.Contains(response.Body.String(), "Local Feature") {
			t.Fatalf("%s: %d %s", target.name, response.Code, response.Body)
		}
		if immediate && elapsed >= time.Second {
			t.Fatalf("%s waited on provider: %s", target.name, elapsed)
		}
	}
}
