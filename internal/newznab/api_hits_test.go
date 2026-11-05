package newznab

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestSearchCacheTTLScalesWithTitleAge(t *testing.T) {
	now := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	base := 6 * time.Hour
	day := 24 * time.Hour
	for _, tt := range []struct {
		name     string
		released time.Time
		empty    bool
		want     time.Duration
	}{
		{"unknown date keeps the base", time.Time{}, false, base},
		{"unknown date empty keeps the short miss", time.Time{}, true, emptySearchCacheTTL},
		{"new title", now.Add(-10 * day), false, base},
		{"year-old title", now.Add(-400 * day), false, 7 * day},
		{"unreleased title", now.Add(10 * day), false, base},
		{"new episode not up yet", now.Add(-2 * day), true, emptySearchCacheTTL},
		{"old title with nothing", now.Add(-60 * day), true, 30 * time.Minute},
	} {
		if got := searchCacheTTL(base, tt.released, now, tt.empty); got != tt.want {
			t.Errorf("%s: %v, want %v", tt.name, got, tt.want)
		}
	}
	mid := searchCacheTTL(base, now.Add(-200*day), now, false)
	if mid <= base || mid >= 7*day {
		t.Errorf("half-year-old title: %v, want between %v and a week", mid, base)
	}
	if got := searchCacheTTL(0, now.Add(-400*day), now, false); got != 0 {
		t.Errorf("disabled cache: %v", got)
	}
}

// Speculative work (warm-ups, detail discovery, availability) asks only the
// primary query and remembers its miss; it never fans out into fallbacks or
// a rescue. Play on the same title still runs the fallback burst.
func TestSpeculativeSearchAsksPrimaryQueryOnly(t *testing.T) {
	req := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt0133093", TMDB: 603}, Title: "The Signal", OriginalTitle: "La Señal", Year: 1999}
	for _, ctx := range []context.Context{
		core.WithIndexerPurpose(core.WithUnattendedSearch(context.Background()), core.IndexerPurposeWarmup),
		core.WithIndexerPurpose(context.Background(), core.IndexerPurposeDetail),
		core.WithIndexerPurpose(context.Background(), core.IndexerPurposeAvailability),
	} {
		f := newFakeIndexer(t, "emptyimdb")
		m := NewManager(nil, []config.Indexer{testConfig(f.server.URL)}, Options{})
		sent := func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.hits["movie"] + f.hits["search"] }
		for range 3 {
			if res, err := m.Search(ctx, req); err != nil || len(res) != 0 {
				t.Fatalf("%s: speculative search %v %v", core.IndexerPurpose(ctx), res, err)
			}
		}
		if _, err := m.SearchRescue(ctx, req); err != nil {
			t.Fatal(err)
		}
		if n := sent(); n != 1 {
			t.Fatalf("%s: speculative work sent %d queries, want the primary query once", core.IndexerPurpose(ctx), n)
		}
		play := core.WithIndexerPurpose(context.Background(), core.IndexerPurposePlayback)
		if res, err := m.Search(play, req); err != nil || len(res) == 0 {
			t.Fatalf("play search: %v %v", res, err)
		}
		if n := sent(); n <= 2 {
			t.Fatalf("Play sent %d queries in all, want its fallback burst after the primary", n)
		}
	}
}
