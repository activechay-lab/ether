package catalog

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestPlayedEpisodesByLastViewed orders a viewer's played episodes by their
// own last-played time (not key or library order), pages them, counts them,
// and restricts to one series — all from the viewer's watch rows.
func TestPlayedEpisodesByLastViewed(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	past := now.Add(-time.Hour).Unix()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := c.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Two shows; show 20 is hidden from paging for a restricted check later.
	for _, id := range []int64{10, 20} {
		exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult) VALUES(?,'show','s','s',?,1,0)`, int64(core.ShowKey(id)), past)
		for e := 1; e <= 3; e++ {
			exec(`INSERT INTO catalog_titles(key,type,show_key,season,episode,title,sort_title,released_ts,adult,browsable,detail) VALUES(?,'episode',?,1,?,'e','e',?,0,0,1)`, int64(core.EpisodeKey(id, 1, e)), int64(core.ShowKey(id)), e, past)
		}
	}
	// Viewer 7 played four episodes, in this order of recency (most recent
	// first): show20e2, show10e3, show10e1, show20e1.
	played := []struct {
		key  core.RatingKey
		when int64
	}{
		{core.EpisodeKey(20, 1, 2), 1000},
		{core.EpisodeKey(10, 1, 3), 900},
		{core.EpisodeKey(10, 1, 1), 800},
		{core.EpisodeKey(20, 1, 1), 700},
	}
	for _, p := range played {
		exec(`INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at,updated_at) VALUES(7,?,1,?,?)`, int64(p.key), p.when, p.when)
	}
	// Unplayed rows must never appear.
	exec(`INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at,updated_at) VALUES(7,?,0,?,?)`, int64(core.EpisodeKey(10, 1, 2)), 2000, 2000)
	// Another viewer's plays must not leak.
	exec(`INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at,updated_at) VALUES(8,?,1,?,?)`, int64(core.EpisodeKey(10, 1, 1)), 5000, 5000)

	filter := core.Filter{Type: core.TypeShow}
	got, err := c.PlayedEpisodesByLastViewed(ctx, filter, 7, 0, true, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.RatingKey{core.EpisodeKey(20, 1, 2), core.EpisodeKey(10, 1, 3), core.EpisodeKey(10, 1, 1), core.EpisodeKey(20, 1, 1)}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", keysOfTitles(got), want)
	}
	for i := range want {
		if got[i].Key != want[i] {
			t.Fatalf("order %v: got %v want %v", i, keysOfTitles(got), want)
		}
	}
	// Ascending reverses the order.
	asc, err := c.PlayedEpisodesByLastViewed(ctx, filter, 7, 0, false, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(asc) != 4 || asc[0].Key != core.EpisodeKey(20, 1, 1) {
		t.Fatalf("ascending: %v", keysOfTitles(asc))
	}
	// Paging continues in last-played order.
	page2, err := c.PlayedEpisodesByLastViewed(ctx, filter, 7, 0, true, 3, 10)
	if err != nil || len(page2) != 1 || page2[0].Key != core.EpisodeKey(20, 1, 1) {
		t.Fatalf("page 2: %v %v", keysOfTitles(page2), err)
	}
	// One series only.
	one, err := c.PlayedEpisodesByLastViewed(ctx, filter, 7, core.ShowKey(10), true, 0, 10)
	if err != nil || len(one) != 2 || one[0].Key != core.EpisodeKey(10, 1, 3) || one[1].Key != core.EpisodeKey(10, 1, 1) {
		t.Fatalf("one series: %v %v", keysOfTitles(one), err)
	}
	// Counts follow the same predicate.
	if n, err := c.CountPlayedEpisodes(ctx, filter, 7, 0); err != nil || n != 4 {
		t.Fatalf("count %d %v", n, err)
	}
	if n, err := c.CountPlayedEpisodes(ctx, filter, 7, core.ShowKey(10)); err != nil || n != 2 {
		t.Fatalf("series count %d %v", n, err)
	}
	if n, err := c.CountPlayedEpisodes(ctx, filter, 8, 0); err != nil || n != 1 {
		t.Fatalf("other viewer count %d %v", n, err)
	}
}

// TestPlayedEpisodesQueryUsesTheWatchIndex proves the played-listing page and
// count seek the viewer's own watch rows rather than scanning the catalogue.
func TestPlayedEpisodesQueryUsesTheWatchIndex(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	past := now.Add(-time.Hour).Unix()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := c.db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult) VALUES(?,'show','s','s',?,1,0)`, int64(core.ShowKey(10)), past)
	for e := 1; e <= 100; e++ {
		exec(`INSERT INTO catalog_titles(key,type,show_key,season,episode,title,sort_title,released_ts,adult,browsable,detail) VALUES(?,'episode',?,1,?,'e','e',?,0,0,1)`, int64(core.EpisodeKey(10, 1, e)), int64(core.ShowKey(10)), e, past)
	}
	for e := 1; e <= 3; e++ {
		exec(`INSERT INTO state_watch(user_id,rating_key,view_count,last_viewed_at,updated_at) VALUES(7,?,1,?,?)`, int64(core.EpisodeKey(10, 1, e)), int64(e), int64(e))
	}
	plan := func(q string, args ...any) string {
		t.Helper()
		rows, err := c.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				t.Fatal(err)
			}
			out += detail + "\n"
		}
		return out
	}
	show := core.Filter{Type: core.TypeShow}
	where, args, err := c.playedEpisodesWhere(ctx, show, 0)
	if err != nil {
		t.Fatal(err)
	}
	q := `SELECT ` + prefixedCols("e") + ` FROM state_watch w CROSS JOIN catalog_titles e JOIN catalog_titles t ON t.key=e.show_key WHERE w.user_id=? AND w.view_count>0 AND e.key=w.rating_key AND ` + where + ` ORDER BY w.last_viewed_at DESC, e.key ASC LIMIT ? OFFSET ?`
	p := plan(q, append([]any{int64(7)}, append(args, 10, 0)...)...)
	if !strings.Contains(p, "state_watch_played") {
		t.Fatalf("the page did not use the played watch index:\n%s", p)
	}
	cq := `SELECT COUNT(*) FROM state_watch w CROSS JOIN catalog_titles e JOIN catalog_titles t ON t.key=e.show_key WHERE w.user_id=? AND w.view_count>0 AND e.key=w.rating_key AND ` + where
	pc := plan(cq, append([]any{int64(7)}, args...)...)
	if !strings.Contains(pc, "state_watch_played") && !strings.Contains(pc, "state_watch_last_viewed") {
		t.Fatalf("the count did not use a played watch index:\n%s", pc)
	}
}
