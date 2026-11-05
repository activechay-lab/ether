package newznab

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// The usage report must agree with the indexer's own counter: every
// request the fake indexer saw is counted once, under the purpose that
// caused it, and cached answers cost nothing.
func TestUsageMatchesIndexerCounterByPurpose(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := newFakeIndexer(t, "")
	cfg := testConfig(f.server.URL)
	cfg.DailyLimit = 50
	m := NewManager(db, []config.Indexer{cfg}, Options{NZBDir: t.TempDir()})

	play := core.WithIndexerPurpose(ctx, core.IndexerPurposePlayback)
	req := core.SearchRequest{Type: core.TypeMovie, Title: "The Signal", Year: 1999, IDs: core.IDs{IMDB: "tt0000001"}}
	results, err := m.Search(play, req) // caps + one search
	if err != nil || len(results) == 0 {
		t.Fatalf("search: %v %v", results, err)
	}
	if _, err := m.Search(play, req); err != nil { // cached: no request
		t.Fatal(err)
	}
	if _, err := m.FetchNZB(play, results[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Recent(core.WithIndexerPurpose(ctx, core.IndexerPurposeAvailability), cfg.ID, 0, 50); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SearchRescue(play, core.SearchRequest{Type: core.TypeMovie, Title: "Another Film", Year: 2001}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Search(ctx, core.SearchRequest{Type: core.TypeMovie, Title: "Untagged", Year: 2002}); err != nil {
		t.Fatal(err)
	}
	if err := m.TestIndexer(ctx, cfg, config.ClientIdentity{}); err != nil {
		t.Fatal(err)
	}

	rep, err := m.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Indexers) != 1 {
		t.Fatalf("indexers=%+v", rep.Indexers)
	}
	u := rep.Indexers[0]
	f.mu.Lock()
	served := 0
	for _, n := range f.hits {
		served += n
	}
	grabs := f.nzbHits
	f.mu.Unlock()
	if u.Today.Hits != int64(served) || u.Today.Grabs != int64(grabs) {
		t.Fatalf("today=%+v, indexer served %d hits and %d grabs", u.Today, served, grabs)
	}
	if u.Week.Hits != u.Today.Hits || u.Month.Hits != u.Today.Hits {
		t.Fatalf("windows disagree: %+v", u)
	}
	got := map[string]PurposeCount{}
	for _, p := range u.Today.Purposes {
		got[p.Purpose] = p
	}
	if got[core.IndexerPurposeCaps].Hits != 1 || got[core.IndexerPurposePlayback].Hits < 1 || got[core.IndexerPurposePlayback].Grabs != 1 {
		t.Fatalf("caps/playback: %+v", got)
	}
	if got[core.IndexerPurposeFeed].Hits < 1 || got[core.IndexerPurposeRescue].Hits < 1 || got[core.IndexerPurposeOther].Hits < 1 {
		t.Fatalf("feed/rescue/other: %+v", got)
	}
	// Test fetches caps and runs one search; both stay tests.
	if got[core.IndexerPurposeTest].Hits != 2 {
		t.Fatalf("test: %+v", got)
	}
	if _, ok := got[PurposeUnattributed]; ok {
		t.Fatalf("every hit should be attributed: %+v", got)
	}
	// Manual tests never spend the daily budget.
	r, err := m.Clients()[0].Remaining(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if int64(r.HitsUsed) != u.Today.Hits-2 {
		t.Fatalf("budget used %d, report %d (2 test hits)", r.HitsUsed, u.Today.Hits)
	}
	if u.DailyLimit != 50 || len(u.Days) != usageDays || u.Days[len(u.Days)-1].Hits != u.Today.Hits {
		t.Fatalf("limit/days: %+v", u)
	}
}

// Budget rows from before purpose tracking (or from an older cluster
// node) still count, as unattributed, and a month reaches past the
// listed days.
func TestUsageReportsUnattributedHistory(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
	cfg := config.Indexer{ID: "idx1", Name: "Fake", URL: "http://indexer.invalid", Enabled: true, DailyLimit: 100}
	m := NewManager(db, []config.Indexer{cfg}, Options{Now: func() time.Time { return now }})
	for _, row := range []struct {
		day         string
		hits, grabs int
	}{{"2026-10-31", 10, 1}, {"2026-10-29", 7, 0}, {"2026-10-02", 5, 2}, {"2026-09-30", 99, 9}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO newznab_budget(indexer_id,day,hits,grabs) VALUES(?,?,?,?)`, "idx1", row.day, row.hits, row.grabs); err != nil {
			t.Fatal(err)
		}
	}
	// Today: 4 of 10 hits attributed to feed polls, plus 3 manual-test hits on top.
	if _, err := db.ExecContext(ctx, `INSERT INTO newznab_usage(indexer_id,day,purpose,hits,grabs) VALUES('idx1','2026-10-31','feed',4,0),('idx1','2026-10-31','test',3,0)`); err != nil {
		t.Fatal(err)
	}
	rep, err := m.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Today != "2026-10-31" || rep.WeekStart != "2026-10-25" || rep.MonthStart != "2026-10-01" {
		t.Fatalf("windows: %+v", rep)
	}
	u := rep.Indexers[0]
	if u.Today.Hits != 13 || u.Today.Grabs != 1 {
		t.Fatalf("today=%+v", u.Today)
	}
	if u.Today.Purposes[0] != (PurposeCount{Purpose: PurposeUnattributed, Hits: 6, Grabs: 1}) {
		t.Fatalf("today purposes=%+v", u.Today.Purposes)
	}
	if u.Week.Hits != 20 || u.Month.Hits != 25 || u.Month.Grabs != 3 {
		t.Fatalf("week=%+v month=%+v", u.Week, u.Month)
	}

	// The console reads the same report over HTTP.
	w := httptest.NewRecorder()
	m.UsageHandler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	var body UsageReport
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Indexers[0].Month.Hits != 25 {
		t.Fatalf("handler %d %s", w.Code, w.Body.String())
	}

	// Usage rows share the budget's retention.
	now = now.AddDate(0, 0, budgetRetentionDays+1)
	if _, err := m.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	var left int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM newznab_usage`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("usage rows left %d (%v)", left, err)
	}
}

// Accounting counts HTTP requests, not logical searches: every fallback
// query of a Play fan-out is one hit, and a grab that never reaches the
// indexer (a release without a download URL) costs nothing. The console's
// totals must equal what the indexer (or Prowlarr in front of it) counted.
func TestUsageCountsEveryHTTPRequestSent(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	f := newFakeIndexer(t, "emptyimdb") // the id query misses: Play fans out
	m := NewManager(db, []config.Indexer{testConfig(f.server.URL)}, Options{NZBDir: t.TempDir()})
	play := core.WithIndexerPurpose(ctx, core.IndexerPurposePlayback)
	req := core.SearchRequest{Type: core.TypeMovie, Title: "The Signal", OriginalTitle: "La Señal", Year: 1999, IDs: core.IDs{IMDB: "tt0000001", TMDB: 7}}
	results, err := m.Search(play, req)
	if err != nil || len(results) == 0 {
		t.Fatalf("search: %v %v", results, err)
	}
	if _, err := m.FetchNZB(play, results[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := m.FetchNZB(play, core.Release{ID: "no-url", Name: "x", Indexer: "Fake", Protocol: core.ProtocolUsenet}); err == nil {
		t.Fatal("a release without a URL cannot be fetched")
	}
	rep, err := m.Usage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	u := rep.Indexers[0]
	f.mu.Lock()
	served := 0
	for _, n := range f.hits {
		served += n
	}
	grabs := f.nzbHits
	f.mu.Unlock()
	if served < 4 {
		t.Fatalf("the fan-out should have sent several queries, sent %d", served)
	}
	if u.Today.Hits != int64(served) || u.Today.Grabs != int64(grabs) || grabs != 1 {
		t.Fatalf("usage %+v, indexer received %d API requests and %d NZB downloads", u.Today, served, grabs)
	}
	r, err := m.Clients()[0].Remaining(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if r.HitsUsed != served || r.GrabsUsed != grabs {
		t.Fatalf("budget %d hits / %d grabs, indexer received %d / %d", r.HitsUsed, r.GrabsUsed, served, grabs)
	}
}
