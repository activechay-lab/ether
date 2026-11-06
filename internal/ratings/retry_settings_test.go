package ratings

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Wait for the worker's bookkeeping, not merely the HTTP response.
func waitRatingsIdle(t *testing.T, s *Service, key core.RatingKey) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		s.qmu.Lock()
		busy := s.queued[key]
		s.qmu.Unlock()
		if !busy {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("ratings worker did not finish")
		case <-tick.C:
		}
	}
}

func TestRetrySettingsSave(t *testing.T) {
	for _, change := range []string{"provider", "key", "backup", "slots", "enable"} {
		t.Run(change, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			db, err := store.OpenMemory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
			}))
			defer srv.Close()
			var clock atomic.Int64
			clock.Store(time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC).Unix())
			s := New(db, Options{OMDbBaseURL: srv.URL, MDBListBaseURL: srv.URL, Now: func() time.Time { return time.Unix(clock.Load(), 0) }})
			cfg := Settings{Enabled: true, Provider: "omdb", APIKey: "synthetic-a", CacheHours: 24}
			save := func(v Settings) {
				t.Helper()
				if err := s.SaveSettings(ctx, v); err != nil {
					t.Fatal(err)
				}
			}
			save(cfg)
			done := make(chan struct{})
			go func() { defer close(done); s.Run(ctx) }()
			defer func() { cancel(); <-done }()
			title := core.Title{Key: core.MovieKey(42), Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt0000042", TMDB: 42}}
			s.Queue(title)
			waitRatingsIdle(t, s, title.Key)
			s.Queue(title)
			waitRatingsIdle(t, s, title.Key)
			if calls.Load() != 1 {
				t.Fatal("unchanged settings bypassed retry delay")
			}
			invalid := cfg
			invalid.CacheHours = 0
			if s.SaveSettings(ctx, invalid) == nil {
				t.Fatal("invalid save succeeded")
			}
			s.Queue(title)
			waitRatingsIdle(t, s, title.Key)
			if calls.Load() != 1 {
				t.Fatal("failed save cleared retry delay")
			}
			switch change {
			case "provider":
				cfg.Provider = "mdblist"
				cfg.APIKey = "synthetic-b"
			case "key":
				cfg.APIKey = "synthetic-b"
			case "backup":
				cfg.BackupProvider = "mdblist"
				cfg.BackupAPIKey = "synthetic-b"
			case "slots":
				cfg.Critic = "imdb"
			case "enable":
				cfg.Enabled = false
				save(cfg)
				cfg.Enabled = true
			}
			save(cfg)
			s.Queue(title)
			waitRatingsIdle(t, s, title.Key)
			if calls.Load() != 2 {
				t.Fatalf("new settings suppressed before retry expiry: calls=%d", calls.Load())
			}
			clock.Add(int64((15 * time.Minute) / time.Second))
			s.Queue(title)
			waitRatingsIdle(t, s, title.Key)
			if calls.Load() != 3 {
				t.Fatal("retry deadline did not expire")
			}
		})
	}
}

func TestRetryOldFailureAfterSettingsSave(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	s := New(db, Options{OMDbBaseURL: srv.URL})
	cfg := Settings{Enabled: true, Provider: "omdb", APIKey: "synthetic-a", CacheHours: 24}
	if err = s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	title := core.Title{Key: core.MovieKey(42), IDs: core.IDs{IMDB: "tt0000042"}}
	s.Queue(title)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider not called")
	}
	cfg.APIKey = "synthetic-b"
	if err = s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitRatingsIdle(t, s, title.Key)
	s.Queue(title)
	waitRatingsIdle(t, s, title.Key)
	if calls.Load() != 2 {
		t.Fatal("old in-flight failure restored retry delay after save")
	}
}

func TestRetryWorkingProviderAfterSave(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Query().Get("apikey") == "synthetic-a" {
			http.Error(w, "unavailable", 503)
			return
		}
		_, _ = w.Write([]byte(`{"ratings":[{"source":"imdb","value":8.2}]}`))
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	s := New(db, Options{OMDbBaseURL: srv.URL, MDBListBaseURL: srv.URL, Now: func() time.Time { return now }})
	cfg := Settings{Enabled: true, Provider: "omdb", APIKey: "synthetic-a", CacheHours: 24}
	if err = s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done }()
	title := core.Title{Key: core.MovieKey(42), Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt0000042", TMDB: 42}}
	s.Queue(title)
	waitRatingsIdle(t, s, title.Key)
	if _, err = db.ExecContext(ctx, `CREATE TRIGGER reject_ratings_save BEFORE UPDATE ON ratings_settings BEGIN SELECT RAISE(ABORT, 'test write failure'); END`); err != nil {
		t.Fatal(err)
	}
	cfg.Provider = "mdblist"
	cfg.APIKey = "synthetic-b"
	if s.SaveSettings(ctx, cfg) == nil {
		t.Fatal("database write unexpectedly succeeded")
	}
	s.Queue(title)
	waitRatingsIdle(t, s, title.Key)
	if calls.Load() != 1 {
		t.Fatal("failed database write cleared backoff")
	}
	if _, err = db.ExecContext(ctx, `DROP TRIGGER reject_ratings_save`); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	s.Queue(title)
	waitRatingsIdle(t, s, title.Key)
	badges, err := s.Cached(ctx, &title)
	if err != nil || len(badges) != 1 || badges[0].Value != 8.2 || calls.Load() != 2 {
		t.Fatalf("working provider not fetched/cached immediately: %v %v calls=%d", badges, err, calls.Load())
	}
}

// A save of unchanged settings must not let a new worker inherit an old
// foreground flight's error and turn it into a new retry deadline.
func TestRetrySaveSeparatesIdenticalSettingsFlight(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	s := refreshTestService(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			close(started)
			<-release
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(omdbOK))
	})
	defer close(release)
	title := core.Title{Key: core.MovieKey(42), IDs: core.IDs{IMDB: "tt0000042"}}
	old := callRefresh(s, ctx, &title)
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("old lookup not started")
	}
	cfg, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() { cancel(); <-done; awaitResult(t, old, "old lookup cancellation") }()
	s.Queue(title)
	waitRatingsIdle(t, s, title.Key)
	badges, err := s.Cached(ctx, &title)
	if err != nil || len(badges) != 1 || badges[0].Value != 8 || calls.Load() != 2 {
		t.Fatalf("new generation reused old flight: badges=%v err=%v calls=%d", badges, err, calls.Load())
	}
	if err = s.SaveSettings(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	s.Queue(title)
	waitRatingsIdle(t, s, title.Key)
	badges, err = s.Cached(ctx, &title)
	if err != nil || len(badges) != 1 || badges[0].Value != 8 || calls.Load() != 2 {
		t.Fatalf("save discarded successful cache: badges=%v err=%v calls=%d", badges, err, calls.Load())
	}
}
