package newznab

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
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

// Events emitted for Statistics must match real HTTP API requests:
//   - multi-attempt fallback searches emit one event per HTTP query sent,
//     marking 0-result HTTP queries Empty so Search success reflects found/sent;
//   - cache hits (search cache, primary-miss cache, NZB disk cache) emit
//     Cached events without counting as live searches or grabs;
//   - local budget refusals (both the 10% speculative share and DailyLimit)
//     emit BudgetExhausted events with zero HTTP requests;
//   - torrent-only searches skip Usenet indexers completely.
func TestEventsSeparateHTTPRequestsFromCacheAndBudgetRefusals(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	f := newFakeIndexer(t, "emptyimdb")
	cfg := testConfig(f.server.URL)
	cfg.DailyLimit = 10
	cfg.DailyGrabs = 2
	m := NewManager(db, []config.Indexer{cfg}, Options{NZBDir: t.TempDir()})

	var mu sync.Mutex
	var events []Event
	m.Subscribe(func(e Event) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	})
	takeEvents := func() []Event {
		mu.Lock()
		defer mu.Unlock()
		out := append([]Event(nil), events...)
		events = nil
		return out
	}
	searchHTTP := func() int {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.hits["movie"] + f.hits["search"] + f.hits["tvsearch"]
	}

	// 1. Torrent-only season-pack query skips Usenet indexers with 0 HTTP hits and 0 events.
	torrentOnly := core.WithTorrentOnlySearch(core.WithIndexerPurpose(ctx, core.IndexerPurposePlayback))
	if res, err := m.Search(torrentOnly, core.SearchRequest{Type: core.TypeEpisode, Title: "Show", Season: 1}); err != nil || len(res) != 0 {
		t.Fatalf("torrent-only on Usenet indexer: %v %v", res, err)
	}
	if searchHTTP() != 0 || len(takeEvents()) != 0 {
		t.Fatalf("torrent-only search hit Usenet indexer")
	}

	// 2. Speculative search on a 10-hit DailyLimit gets 1 speculative hit (10% of 10 = 1).
	// First call misses on primary IMDB query (1 HTTP search, Empty=true).
	sctx, _ := core.WithSpeculative(ctx)
	warm := core.WithIndexerPurpose(sctx, core.IndexerPurposeWarmup)
	missReq := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt0133093"}, Title: "The Signal", Year: 1999}
	if res, err := m.Search(warm, missReq); err != nil || len(res) != 0 {
		t.Fatalf("first warm search: %v %v", res, err)
	}
	evs := takeEvents()
	if len(evs) != 1 || evs[0].Kind != "search" || !evs[0].Success || !evs[0].Empty || evs[0].Cached || evs[0].BudgetExhausted || evs[0].Purpose != core.IndexerPurposeWarmup {
		t.Fatalf("first warm events: %+v", evs)
	}
	if got := searchHTTP(); got != 1 {
		t.Fatalf("search HTTP = %d, want 1", got)
	}

	// Second speculative call for the SAME title is answered from primaryMissKey cache (0 HTTP hits).
	if res, err := m.Search(warm, missReq); err != nil || len(res) != 0 {
		t.Fatalf("cached warm search: %v %v", res, err)
	}
	evs = takeEvents()
	if len(evs) != 1 || !evs[0].Cached || evs[0].Kind != "cache" || searchHTTP() != 1 {
		t.Fatalf("cached primary-miss events=%+v http=%d", evs, searchHTTP())
	}

	// Third speculative call for a DIFFERENT title exceeds the 10% speculative share:
	// refused locally (BudgetExhausted=true, SpeculativeHeld=true) with 0 HTTP hits.
	otherWarmReq := core.SearchRequest{Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt0000002"}, Title: "Other", Year: 2001}
	if _, err := m.Search(warm, otherWarmReq); !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("expected speculative budget refusal, got %v", err)
	}
	evs = takeEvents()
	if len(evs) != 1 || !evs[0].BudgetExhausted || !evs[0].SpeculativeHeld || searchHTTP() != 1 {
		t.Fatalf("speculative refusal events=%+v http=%d", evs, searchHTTP())
	}

	// 3. Foreground Play on missReq runs multi-attempt fallbacks (DailyLimit>0 steps serially:
	// primary IMDB misses -> Empty=true; keyword fallback hits -> Empty=false).
	play := core.WithIndexerPurpose(ctx, core.IndexerPurposePlayback)
	beforeHTTP := searchHTTP()
	rels, err := m.Search(play, missReq)
	if err != nil || len(rels) == 0 {
		t.Fatalf("play multi-attempt search: %v %v", rels, err)
	}
	deltaHTTP := searchHTTP() - beforeHTTP
	evs = takeEvents()
	if deltaHTTP < 2 || len(evs) != deltaHTTP {
		t.Fatalf("multi-attempt emitted %d events for %d HTTP queries: %+v", len(evs), deltaHTTP, evs)
	}
	emptyCount, foundCount := 0, 0
	for _, e := range evs {
		if e.Kind != "search" || !e.Success || e.Cached || e.BudgetExhausted || e.Purpose != core.IndexerPurposePlayback {
			t.Fatalf("unexpected search event: %+v", e)
		}
		if e.Empty {
			emptyCount++
		} else {
			foundCount++
		}
	}
	if emptyCount == 0 || foundCount != 1 {
		t.Fatalf("want >=1 empty attempt and 1 found attempt, got empty=%d found=%d", emptyCount, foundCount)
	}

	// Repeating Play on missReq is served from the result cache: 1 Cached event, 0 HTTP queries.
	beforeHTTP = searchHTTP()
	if _, err := m.Search(play, missReq); err != nil {
		t.Fatal(err)
	}
	evs = takeEvents()
	if len(evs) != 1 || !evs[0].Cached || evs[0].Kind != "cache" || searchHTTP() != beforeHTTP {
		t.Fatalf("result cache events=%+v httpDelta=%d", evs, searchHTTP()-beforeHTTP)
	}

	// 4. NZB grab: first FetchNZB hits network (1 grab event), second hits disk cache (1 cache event).
	if _, err := m.FetchNZB(play, rels[0]); err != nil {
		t.Fatal(err)
	}
	evs = takeEvents()
	if len(evs) != 1 || evs[0].Kind != "grab" || !evs[0].Success || evs[0].Cached {
		t.Fatalf("first grab events=%+v", evs)
	}
	if _, err := m.FetchNZB(play, rels[0]); err != nil {
		t.Fatal(err)
	}
	evs = takeEvents()
	if len(evs) != 1 || !evs[0].Cached || evs[0].Kind != "cache" || f.nzbHits != 1 {
		t.Fatalf("disk-cached grab events=%+v nzbHits=%d", evs, f.nzbHits)
	}
}
