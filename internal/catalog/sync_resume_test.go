package catalog

import (
	"context"
	"errors"
	"fmt"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/tmdb"
	"testing"
	"time"
)

type interruptedDiscovery struct {
	TMDB
	calls []int
	fail  bool
}

func (f *interruptedDiscovery) DiscoverMovies(ctx context.Context, p tmdb.DiscoverParams) (*tmdb.Paged[tmdb.Movie], error) {
	f.calls = append(f.calls, p.Page)
	if f.fail && p.Page == 3 {
		return nil, errors.New("injected late discovery failure")
	}
	return &tmdb.Paged[tmdb.Movie]{TotalPages: 3, Results: []tmdb.Movie{{ID: int64(p.Page), Title: fmt.Sprintf("Movie %d", p.Page), ReleaseDate: "2000-01-01", Popularity: 50}}}, nil
}
func TestSyncPageContinuationSurvivesNewCatalog(t *testing.T) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	f := &interruptedDiscovery{fail: true}
	c.SetTMDB(f)
	resumed, err := c.resumeSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.walkDiscover(resumed, c.popularityWalk(false), newSyncWriter(c)); err == nil {
		t.Fatal("expected late page failure")
	}
	f.fail = false
	f.calls = nil
	next, err := New(c.db, f, config.Catalog{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err = next.resumeSync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.walkDiscover(resumed, next.popularityWalk(false), newSyncWriter(next)); err != nil {
		t.Fatal(err)
	}
	// Pages 1 and 2 were stored before the failure; they must not recur.
	if fmt.Sprint(f.calls) != "[3]" {
		t.Fatalf("resumed calls %v", f.calls)
	}
	f.calls = nil
	if err := next.walkDiscover(resumed, next.popularityWalk(false), newSyncWriter(next)); err != nil || len(f.calls) != 0 {
		t.Fatalf("completed pass repeated: %v %v", f.calls, err)
	}
	for n, want := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 16 * time.Minute, 32 * time.Minute, time.Hour, time.Hour} {
		if got := syncRetryDelay(n + 1); got != want {
			t.Fatalf("retry %d: %v", n, got)
		}
	}
}
