package catalog

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type continueTMDB struct {
	TMDB
	release chan struct{}
	calls   atomic.Int64
	season  int
	air     string
}

func (f *continueTMDB) SeasonDetails(ctx context.Context, id int64, season int) (*tmdb.SeasonDetails, error) {
	f.calls.Add(1)
	select {
	case <-f.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if season != f.season {
		return &tmdb.SeasonDetails{SeasonNumber: season}, nil
	}
	episode := 2
	if season == 2 {
		episode = 1
	}
	return &tmdb.SeasonDetails{SeasonNumber: season, Episodes: []tmdb.Episode{{SeasonNumber: season, EpisodeNumber: episode, AirDate: f.air, Name: "Next"}}}, nil
}

// Local Home reads must recover missing metadata without waiting for the provider,
// sharing refresh work and never admitting future episodes.
func TestContinueMetadataRecoveryWithoutProviderWait(t *testing.T) {
	for _, tc := range []struct {
		name   string
		season int
		air    string
		want   bool
	}{
		{"same season", 1, "2025-01-01", true},
		{"season boundary", 2, "2025-01-01", true},
		{"unaired", 1, "2027-01-01", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			db := store.OpenTest(t)
			api := &continueTMDB{release: make(chan struct{}), season: tc.season, air: tc.air}
			c, err := New(db, api, config.Catalog{}, nil, WithNow(func() time.Time { return now }))
			if err != nil {
				t.Fatal(err)
			}
			defer c.WaitRefreshes()
			var once sync.Once
			release := func() { once.Do(func() { close(api.release) }) }
			defer release()
			show := core.Title{Key: core.ShowKey(100), Type: core.TypeShow, Title: "Show", Status: "Returning Series", Released: now.AddDate(-1, 0, 0)}
			if err := c.upsert(ctx, show, upsertOpts{detail: true}); err != nil {
				t.Fatal(err)
			}
			for season := 1; season <= tc.season; season++ {
				title := core.Title{Key: core.SeasonKey(100, season), Type: core.TypeSeason, ShowKey: show.Key, Season: season, Released: show.Released}
				if err := c.upsert(ctx, title, upsertOpts{detail: season == 1}); err != nil {
					t.Fatal(err)
				}
			}
			prior := core.EpisodeKey(100, 1, 1)
			if err := c.upsert(ctx, core.Title{Key: prior, Type: core.TypeEpisode, ShowKey: show.Key, Season: 1, Episode: 1, Released: show.Released}, upsertOpts{}); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE catalog_titles SET detail_at=? WHERE type='season' AND season=1`, now.Add(-20*time.Minute).Unix()); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				start := time.Now()
				got, err := c.ContinueEpisodes(WithReadWaitBudget(ctx, 0), []core.RatingKey{prior})
				if err != nil || len(got) != 0 {
					t.Fatalf("cold read: %v %v", got, err)
				}
				if time.Since(start) > time.Second {
					t.Fatal("Home waited for metadata")
				}
			}
			deadline := time.Now().Add(time.Second)
			for api.calls.Load() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if api.calls.Load() == 0 {
				t.Fatal("missing next episode never schedules metadata recovery")
			}
			release()
			c.WaitRefreshes()
			if got := api.calls.Load(); got != int64(tc.season) {
				t.Fatalf("refreshes not shared: got %d, want %d", got, tc.season)
			}
			got, err := c.ContinueEpisodes(ctx, []core.RatingKey{prior})
			if err != nil {
				t.Fatal(err)
			}
			if (got[prior] != nil) != tc.want {
				t.Fatalf("after hydration: %v, want episode=%v", got, tc.want)
			}
		})
	}
}
