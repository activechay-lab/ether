package watchlist

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/notify"
	"github.com/braedonsaunders/ether/internal/store"
)

var _ onceForgetter = (*notify.Service)(nil)

// Prune drops an episode alert (and its once key) only when it can never be
// raised again: finished, past its lease, and its show and season off every
// watchlist. Removing a show takes its handled episodes in the same write.
func TestPruneKeepsAlertsAWatchlistCanRaise(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	n := notify.New(db, notify.Options{Now: clock})
	s := New(db, Options{Notifier: n, Now: clock})
	old, recent := now.Add(-48*time.Hour).Unix(), now.Add(-time.Hour).Unix()
	gone, kept, bySeason, pending, fresh := core.EpisodeKey(11, 1, 2), core.EpisodeKey(22, 1, 2), core.EpisodeKey(33, 2, 5), core.EpisodeKey(11, 1, 3), core.EpisodeKey(11, 1, 4)
	for _, a := range []struct {
		key           core.RatingKey
		at            int64
		sent, attempt int
	}{{gone, old, 1, 0}, {kept, old, 1, 0}, {bySeason, old, 0, alertAttempts}, {pending, old, 0, 1}, {fresh, recent, 1, 0}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO watchlist_episodes_alerts(episode_key,label,at,sent,attempts) VALUES(?,?,?,?,?)`, a.key, "x", a.at, a.sent, a.attempt); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO notify_once(key,at) VALUES(?,?)`, "title.available:"+episodeOnceKey(a.key), old); err != nil {
			t.Fatal(err)
		}
	}
	for _, it := range []struct {
		user int64
		key  core.RatingKey
	}{{1, core.ShowKey(11)}, {2, core.ShowKey(22)}, {2, core.SeasonKey(33, 2)}} {
		if _, err := db.ExecContext(ctx, `INSERT INTO watchlist_items(user_id,item_key,source,title,added) VALUES(?,?,'manual','{}',?)`, it.user, it.key, old); err != nil {
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO watchlist_episodes(user_id,item_key,episode_key,at) VALUES(?,?,?,?)`, it.user, it.key, core.EpisodeKey(it.key.TMDB(), 1, 1), old); err != nil {
			t.Fatal(err)
		}
	}
	// An episode row left by a check that raced an earlier removal.
	if _, err := db.ExecContext(ctx, `INSERT INTO watchlist_episodes(user_id,item_key,episode_key,at) VALUES(3,?,?,?)`, core.ShowKey(22), kept, old); err != nil {
		t.Fatal(err)
	}
	if err := s.Remove(ctx, 1, core.ShowKey(11)); err != nil {
		t.Fatal(err)
	}
	count := func(q string, args ...any) int {
		t.Helper()
		var c int
		if err := db.QueryRowContext(ctx, q, args...).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := count(`SELECT COUNT(*) FROM watchlist_episodes WHERE user_id=1`); c != 0 {
		t.Fatalf("removed show kept %d episode rows", c)
	}
	removed, err := s.Prune(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 3 { // the orphan episode row, gone's alert and its once key
		t.Fatalf("pruned %d rows, want 3", removed)
	}
	for _, k := range []core.RatingKey{kept, bySeason, pending, fresh} {
		if count(`SELECT COUNT(*) FROM watchlist_episodes_alerts WHERE episode_key=?`, k) != 1 || count(`SELECT COUNT(*) FROM notify_once WHERE key=?`, "title.available:"+episodeOnceKey(k)) != 1 {
			t.Fatalf("alert %d could still be raised but was pruned", k)
		}
	}
	if count(`SELECT COUNT(*) FROM watchlist_episodes_alerts WHERE episode_key=?`, gone)+count(`SELECT COUNT(*) FROM notify_once WHERE key=?`, "title.available:"+episodeOnceKey(gone)) != 0 {
		t.Fatal("dead alert or its once key survived")
	}
	if count(`SELECT COUNT(*) FROM watchlist_episodes`) != 2 {
		t.Fatal("prune touched episode rows of watchlisted shows")
	}
}
