package catalog

import (
	"context"
	"strings"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/favorites"

	"github.com/braedonsaunders/ether/internal/core"
)

// TestFavoriteEpisodesByKey answers a viewer's favorite episodes in Jellyfin
// SortName order (series sort title, then season, then episode), pages them,
// counts them, and restricts to one series — all from the favorite rows, not
// the library.
func TestFavoriteEpisodesByKey(t *testing.T) {
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
	// Two shows with names that sort "Alpha" before "Beta".
	exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult) VALUES(?,'show','Alpha','Alpha',?,1,0)`, int64(core.ShowKey(10)), past)
	exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult) VALUES(?,'show','Beta','Beta',?,1,0)`, int64(core.ShowKey(20)), past)
	for e := 1; e <= 3; e++ {
		exec(`INSERT INTO catalog_titles(key,type,show_key,season,episode,title,sort_title,released_ts,adult,browsable,detail) VALUES(?,'episode',?,1,?,'e','e',?,0,0,1)`, int64(core.EpisodeKey(10, 1, e)), int64(core.ShowKey(10)), e, past)
		exec(`INSERT INTO catalog_titles(key,type,show_key,season,episode,title,sort_title,released_ts,adult,browsable,detail) VALUES(?,'episode',?,1,?,'e','e',?,0,0,1)`, int64(core.EpisodeKey(20, 1, e)), int64(core.ShowKey(20)), e, past)
	}
	// Viewer 7 favorited four episodes of the two shows.
	favs := []core.RatingKey{core.EpisodeKey(20, 1, 2), core.EpisodeKey(10, 1, 3), core.EpisodeKey(10, 1, 1), core.EpisodeKey(20, 1, 1)}
	for _, k := range favs {
		exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at,sort_order) VALUES(7,'title',?,0,0)`, k.String())
	}
	// A favorited movie must never appear.
	exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at,sort_order) VALUES(7,'title','1000001',0,0)`)
	// Another viewer's favorites must not leak.
	exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at,sort_order) VALUES(8,'title',?,0,0)`, core.EpisodeKey(10, 1, 2).String())

	filter := core.Filter{Type: core.TypeShow}
	got, err := c.FavoriteEpisodesByKey(ctx, filter, 7, 0, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	// SortName: Alpha (10) e1..e3, then Beta (20) e1..e2.
	want := []core.RatingKey{core.EpisodeKey(10, 1, 1), core.EpisodeKey(10, 1, 3), core.EpisodeKey(20, 1, 1), core.EpisodeKey(20, 1, 2)}
	wantSet := map[core.RatingKey]bool{}
	for _, k := range want {
		wantSet[k] = true
	}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", keysOfTitles(got), want)
	}
	for _, title := range got {
		if !wantSet[title.Key] {
			t.Fatalf("unexpected title %v", title.Key)
		}
	}
	if got[0].Key.TMDB() != 10 || got[len(got)-1].Key.TMDB() != 20 {
		t.Fatalf("sort order: %v", keysOfTitles(got))
	}
	// Paging continues.
	page2, err := c.FavoriteEpisodesByKey(ctx, filter, 7, 0, 2, 10)
	if err != nil || len(page2) != 2 {
		t.Fatalf("page 2: %v %v", keysOfTitles(page2), err)
	}
	// One series only.
	one, err := c.FavoriteEpisodesByKey(ctx, filter, 7, core.ShowKey(10), 0, 10)
	if err != nil || len(one) != 2 {
		t.Fatalf("one series: %v %v", keysOfTitles(one), err)
	}
	for _, title := range one {
		if title.ShowKey != core.ShowKey(10) {
			t.Fatalf("series restriction leaked %v", title.Key)
		}
	}
	// Counts follow the same predicate.
	if n, err := c.CountFavoriteEpisodes(ctx, filter, 7, 0); err != nil || n != 4 {
		t.Fatalf("count %d %v", n, err)
	}
	if n, err := c.CountFavoriteEpisodes(ctx, filter, 7, core.ShowKey(10)); err != nil || n != 2 {
		t.Fatalf("series count %d %v", n, err)
	}
	if n, err := c.CountFavoriteEpisodes(ctx, filter, 8, 0); err != nil || n != 1 {
		t.Fatalf("other viewer count %d %v", n, err)
	}
}

// TestFavoriteEpisodesQueryDrivenByFavorites proves the favorite-listing page
// and count are driven by the viewer's favorite rows rather than a scan of the
// catalogue's episodes.
func TestFavoriteEpisodesQueryDrivenByFavorites(t *testing.T) {
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
		exec(`INSERT INTO favorites_items(viewer_id,kind,subject_id,added_at,sort_order) VALUES(7,'title',?,0,0)`, core.EpisodeKey(10, 1, e).String())
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
	where, args, err := c.favoriteEpisodesWhere(ctx, show, 0)
	if err != nil {
		t.Fatal(err)
	}
	q := `SELECT ` + prefixedCols("e") + favoritesTitleJoin + ` WHERE f.viewer_id=? AND f.kind='title' AND ` + where + ` ORDER BY t.sort_title ASC, e.season ASC, e.episode ASC, e.key ASC LIMIT ? OFFSET ?`
	p := plan(q, append([]any{int64(7)}, append(args, 10, 0)...)...)
	if !strings.Contains(p, "favorites_items") {
		t.Fatalf("the page was not driven by the favorite rows:\n%s", p)
	}
	cq := `SELECT COUNT(*)` + favoritesTitleJoin + ` WHERE f.viewer_id=? AND f.kind='title' AND ` + where
	pc := plan(cq, append([]any{int64(7)}, args...)...)
	if !strings.Contains(pc, "favorites_items") {
		t.Fatalf("the count was not driven by the favorite rows:\n%s", pc)
	}
}
