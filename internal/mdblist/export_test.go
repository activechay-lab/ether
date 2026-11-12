package mdblist

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := store.OpenPath(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestOutboxDeduplicatesAndIgnoresImports(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	s := TokenStore{DB: db, Sealer: testSealer{}}
	if err := s.Save(ctx, 1, "key-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	ev := state.Event{UserID: 1, Kind: state.EventCompleted, Key: core.MovieKey(123), At: time.Now()}
	if err := Enqueue(ctx, db, ev); err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(ctx, db, ev); err != nil {
		t.Fatal(err)
	}
	ev.Imported = true
	ev.Key = core.MovieKey(124)
	if err := Enqueue(ctx, db, ev); err != nil {
		t.Fatal(err)
	}
	ev.Imported = false
	ev.UserID = 2
	if err := Enqueue(ctx, db, ev); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mdblist_outbox`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("count %d err %v", n, err)
	}
	if err := s.Delete(ctx, 1); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mdblist_outbox`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("disconnect count %d err %v", n, err)
	}
}

// TestExportOrderingAndIdempotence is the lasting invariant test: queued
// completions are delivered oldest-first, a re-sent completion is a no-op at
// the remote (so a retry after an uncertain response cannot duplicate), and a
// delivered row is removed from the outbox exactly once.
func TestExportOrderingAndIdempotence(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	tokens := TokenStore{DB: db, Sealer: testSealer{}}
	if err := tokens.Save(ctx, 1, "key-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// Enqueue out of order; delivery must sort by watched_at.
	for _, off := range []time.Duration{2 * time.Hour, 0, time.Hour} {
		if err := Enqueue(ctx, db, state.Event{UserID: 1, Kind: state.EventCompleted, Key: core.MovieKey(100 + int64(off/time.Hour)), At: base.Add(off)}); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	var order []string
	seen := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Movies []struct {
				IDs       map[string]any `json:"ids"`
				WatchedAt string         `json:"watched_at"`
			} `json:"movies"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		for _, m := range body.Movies {
			order = append(order, m.WatchedAt)
			seen[m.WatchedAt]++
		}
		mu.Unlock()
		_, _ = w.Write([]byte(`{"updated":{"movies":1},"not_found":null}`))
	}))
	defer srv.Close()
	svc := Service{Client: Client{BaseURL: srv.URL}, Tokens: tokens}
	for i := 0; i < 3; i++ {
		if err := svc.FlushOne(ctx, 1); err != nil {
			t.Fatal(err)
		}
		// Clear the shared and per-viewer pacing so the next flush is not throttled.
		if _, err := db.Exec(`UPDATE mdblist_post_pacing SET next_post=0 WHERE id=1`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE mdblist_accounts SET next_post=0 WHERE user_id=1`); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		"2026-01-01T00:00:00Z",
		"2026-01-01T01:00:00Z",
		"2026-01-01T02:00:00Z",
	}
	if len(order) != len(want) {
		t.Fatalf("delivered %v", order)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order %v, want %v", order, want)
		}
	}
	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mdblist_outbox WHERE user_id=1`).Scan(&remaining); err != nil || remaining != 0 {
		t.Fatalf("outbox not drained: %d %v", remaining, err)
	}
	// A re-sent completion (same item, same time) is deduplicated locally, so
	// it never reaches the remote twice.
	if err := Enqueue(ctx, db, state.Event{UserID: 1, Kind: state.EventCompleted, Key: core.MovieKey(100), At: base}); err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(ctx, db, state.Event{UserID: 1, Kind: state.EventCompleted, Key: core.MovieKey(100), At: base}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM mdblist_outbox WHERE user_id=1`).Scan(&remaining); err != nil || remaining != 1 {
		t.Fatalf("dedupe failed: %d %v", remaining, err)
	}
}

func TestFlushDropsUnsupportedIdentity(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	tokens := TokenStore{DB: db, Sealer: testSealer{}}
	if err := tokens.Save(ctx, 1, "key-1", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := Enqueue(ctx, db, state.Event{UserID: 1, Kind: state.EventCompleted, Key: core.MovieKey(999), At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"not_found":{"movies":[{"ids":{"tmdb":999}}]}}`))
	}))
	defer srv.Close()
	svc := Service{Client: Client{BaseURL: srv.URL}, Tokens: tokens}
	if err := svc.FlushOne(ctx, 1); err != nil {
		t.Fatal(err)
	}
	var n int
	var warning string
	if err := db.QueryRow(`SELECT (SELECT COUNT(*) FROM mdblist_outbox),last_warning FROM mdblist_accounts WHERE user_id=1`).Scan(&n, &warning); err != nil {
		t.Fatal(err)
	}
	if n != 0 || warning != "not_found" {
		t.Fatalf("queue=%d warning=%q", n, warning)
	}
}
