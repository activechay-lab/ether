package stats

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSourceHistoryDebridDeltasAndWindow(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s := New(db, Options{Now: func() time.Time { return now }})
	for _, n := range []int64{10, 25, 5} { // 5 is a restart: counts from zero again
		if err := s.SnapshotDebrid(ctx, []DebridSample{{ID: "rd", Requests: n, Cached: n / 5, Bytes: n * 1000}}); err != nil {
			t.Fatal(err)
		}
		if err := s.SnapshotStream(ctx, StreamSample{MemoryHits: n, BlocksFetched: 2 * n}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO stats_provider_days(provider,day,bytes,articles,misses,errors) VALUES('old','2026-09-01',1,1,0,0),('p','2026-11-07',100,4,1,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated,failovers,origin) VALUES('play',1,1,'u','t','p','d','directplay',?,?,1,'Debrid')`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	h, err := s.SourceHistoryFor(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if h.Since != "2026-10-10" || h.Until != "2026-11-08" {
		t.Fatalf("window %s..%s", h.Since, h.Until)
	}
	if len(h.DebridDays) != 1 || h.DebridDays[0].Requests != 30 || h.DebridDays[0].Cached != 6 || h.DebridDays[0].Bytes != 30000 {
		t.Fatalf("debrid days %+v", h.DebridDays)
	}
	if len(h.StreamDays) != 1 || h.StreamDays[0].MemoryHits != 30 || h.StreamDays[0].BlocksFetched != 60 {
		t.Fatalf("stream days %+v", h.StreamDays)
	}
	if len(h.ProviderDays) != 1 || h.ProviderDays[0].ID != "p" {
		t.Fatalf("provider days outside the window leaked: %+v", h.ProviderDays)
	}
	if len(h.Plays) != 1 || h.Plays[0].Origin != "Debrid" || h.Plays[0].Day != "2026-11-08" || h.Plays[0].Failovers != 1 {
		t.Fatalf("plays %+v", h.Plays)
	}
	// Lifetime reads every retained day, including ones outside the window.
	if l := h.Lifetime; len(l.Providers) != 2 || l.Providers[0].ID != "old" || l.Providers[0].FirstDay != "2026-09-01" || len(l.Debrid) != 1 || l.Debrid[0].Bytes != 30000 {
		t.Fatalf("lifetime %+v", h.Lifetime)
	}
	if len(h.Hours) != 0 {
		t.Fatalf("hours outside a one-day window: %+v", h.Hours)
	}
	today, err := s.SourceHistoryFor(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if today.Since != "2026-11-08" || len(today.Hours) != 1 || today.Hours[0].Hour != 12 || today.Hours[0].Plays != 1 {
		t.Fatalf("today %s hours %+v", today.Since, today.Hours)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/sources?days=500", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"days":90`) {
		t.Fatalf("bounded window: %d %s", w.Code, w.Body.String())
	}
}

func TestIndexerStatsSeparateAPIRequestsCacheHitsAndBudgets(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 11, 9, 15, 0, 0, 0, time.UTC)
	s := New(db, Options{Now: func() time.Time { return now }})

	events := []newznab.Event{
		// Multi-attempt search: 2 empty queries + 1 query with results = 3 API requests, 1 search success.
		{Indexer: "DrunkenSlug", Kind: "search", Purpose: core.IndexerPurposePlayback, Success: true, Empty: true, Duration: 200 * time.Millisecond, At: now},
		{Indexer: "DrunkenSlug", Kind: "search", Purpose: core.IndexerPurposePlayback, Success: true, Empty: true, Duration: 250 * time.Millisecond, At: now},
		{Indexer: "DrunkenSlug", Kind: "search", Purpose: core.IndexerPurposePlayback, Success: true, Empty: false, Results: 8, Duration: 300 * time.Millisecond, AverageAge: 48 * time.Hour, At: now},
		// RSS feed poll: 1 API request, not a title search.
		{Indexer: "DrunkenSlug", Kind: "feed", Purpose: core.IndexerPurposeFeed, Success: true, Results: 50, Duration: 180 * time.Millisecond, At: now},
		// Real NZB grab: 1 API request, 1 grab.
		{Indexer: "DrunkenSlug", Kind: "grab", Purpose: core.IndexerPurposePlayback, Success: true, Duration: 150 * time.Millisecond, At: now},
		// Cache hits (search cache + disk NZB): 0 API requests, 2 cache hits.
		{Indexer: "DrunkenSlug", Kind: "cache", Purpose: core.IndexerPurposeWarmup, Success: true, Cached: true, Duration: 2 * time.Millisecond, At: now},
		{Indexer: "DrunkenSlug", Kind: "cache", Purpose: core.IndexerPurposePlayback, Success: true, Cached: true, At: now},
		// Budget refusals (speculative 10% cap + daily limit): 0 API requests, 3 budgets.
		{Indexer: "DrunkenSlug", Kind: "search", Purpose: core.IndexerPurposeWarmup, BudgetExhausted: true, SpeculativeHeld: true, At: now},
		{Indexer: "DrunkenSlug", Kind: "search", Purpose: core.IndexerPurposeDetail, BudgetExhausted: true, SpeculativeHeld: true, At: now},
		{Indexer: "DrunkenSlug", Kind: "grab", Purpose: core.IndexerPurposeWarmup, BudgetExhausted: true, At: now},
	}
	for _, e := range events {
		if err := s.RecordIndexer(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	// Pre-upgrade / imported row without requests populated falls back to searches + grabs.
	if _, err := db.Exec(`INSERT INTO stats_indexer_days(indexer,day,searches,successes,grabs,grab_success) VALUES('DrunkenSlug','2026-10-01',10,8,2,2)`); err != nil {
		t.Fatal(err)
	}

	h, err := s.SourceHistoryFor(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.IndexerDays) != 1 {
		t.Fatalf("indexerDays=%+v", h.IndexerDays)
	}
	got := h.IndexerDays[0]
	if got.Requests != 5 || got.CacheHits != 2 || got.Budgets != 3 {
		t.Fatalf("headline counts: requests=%d cacheHits=%d budgets=%d, want 5 / 2 / 3", got.Requests, got.CacheHits, got.Budgets)
	}
	if got.Searches != 3 || got.Successes != 1 || got.Grabs != 1 || got.GrabSuccess != 1 {
		t.Fatalf("search/grab counts: %+v, want searches=3 successes=1 grabs=1 grabSuccess=1", got)
	}
	if got.LatencyMS != 750 || got.AgeSamples != 1 {
		t.Fatalf("latency/age polluted by non-searches: %+v", got)
	}
	if len(h.Lifetime.Indexers) != 1 || h.Lifetime.Indexers[0].Requests != 17 || h.Lifetime.Indexers[0].CacheHits != 2 || h.Lifetime.Indexers[0].Budgets != 3 {
		t.Fatalf("lifetime=%+v, want requests=17 (5 today + 12 historical)", h.Lifetime.Indexers)
	}

	month, err := s.IndexerMonth(ctx)
	if err != nil || len(month) != 1 || month[0].Requests != 5 || month[0].CacheHits != 2 || month[0].Budgets != 3 {
		t.Fatalf("IndexerMonth=%+v %v", month, err)
	}
}
