package notify

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Pruning favourite-episode alerts and seen rows never lets a continuing
// scan announce an episode a second time.
func TestPruneViewerAlertsNeverRealerts(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	prefs := &ViewerAlerts{DB: db, Now: clock, Active: func(_ context.Context, id int64) (*core.User, error) { return &core.User{ID: id}, nil }}
	if err := prefs.SetEnabled(ctx, &core.User{ID: 5}, true); err != nil {
		t.Fatal(err)
	}
	aired := now.Add(time.Minute)
	d := FavoriteEpisodeDetector{Preferences: prefs, Now: clock, Episodes: func(context.Context, *core.User) ([]core.Title, error) {
		return []core.Title{{Key: core.EpisodeKey(9, 1, 1), Type: core.TypeEpisode, Released: aired, Title: "E", ShowTitle: "S"}}, nil
	}}
	s := New(db, Options{Now: clock})
	count := func(table string) int {
		t.Helper()
		var c int
		if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	// scan runs a pass that continues the schedule (no rebaseline).
	scan := func() {
		t.Helper()
		if _, err := db.ExecContext(ctx, `UPDATE viewer_episode_scan SET scanned_at=?`, now.Add(-10*time.Minute).Unix()); err != nil {
			t.Fatal(err)
		}
		if err := d.Scan(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if err := d.Scan(ctx); err != nil { // baseline
		t.Fatal(err)
	}
	now = now.Add(2 * time.Minute)
	scan()
	if count("viewer_episode_alerts") != 1 {
		t.Fatal("episode was not alerted")
	}
	if n, err := s.Prune(ctx); err != nil || n != 0 {
		t.Fatalf("prune of a deliverable alert: %d %v", n, err)
	}
	now = now.Add(3 * 24 * time.Hour)
	if n, err := s.Prune(ctx); err != nil || n != 1 || count("viewer_episode_seen") != 1 {
		t.Fatalf("expired alert prune: %d %v", n, err)
	}
	scan()
	if count("viewer_episode_alerts") != 0 {
		t.Fatal("pruned alert was raised again")
	}
	now = now.Add(5 * 24 * time.Hour)
	if n, err := s.Prune(ctx); err != nil || n != 1 {
		t.Fatalf("seen prune: %d %v", n, err)
	}
	scan()
	if count("viewer_episode_alerts")+count("viewer_episode_seen") != 0 {
		t.Fatal("an episode outside the alert window was seen or alerted again")
	}
}
