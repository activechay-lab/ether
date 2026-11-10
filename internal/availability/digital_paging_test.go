package availability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

// Exercise live SQL and the tokenless paging contract together. Date discovery
// ahead of an offset repeats a title; date removal ahead of it skips a title.
func TestDigitalPagingCutSurvivesDateAndGenerationChanges(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	c, err := catalog.New(x.s.db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return x.now }))
	if err != nil {
		t.Fatal(err)
	}
	x.s.d.Catalog = c
	x.c.Availability.Mode = config.ModeDigital
	x.c.Availability.ApplyMode()
	for i := int64(1); i <= 30; i++ {
		title := core.Title{Key: core.MovieKey(i), Type: core.TypeMovie, Title: fmt.Sprintf("%03d", i), Released: x.now.Add(-24 * time.Hour)}
		if err := c.UpsertTitle(ctx, title, true); err != nil {
			t.Fatal(err)
		}
		if i > 1 {
			if _, err := x.s.db.Exec(`INSERT INTO catalog_home_release(key,checked,earliest_ts) VALUES(?,1,?)`, title.Key, x.now.Add(-time.Hour).Unix()); err != nil {
				t.Fatal(err)
			}
		}
	}
	live := core.Filter{Type: core.TypeMovie, DigitalDiscovery: true, DigitalDateEvidence: true}
	first, err := c.Browse(ctx, core.BrowseQuery{Filter: live, Sort: core.SortTitle, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeMovie})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.db.Exec(`INSERT INTO catalog_home_release(key,checked,earliest_ts) VALUES(?,1,?)`, core.MovieKey(1), x.now.Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	second, err := c.Browse(ctx, core.BrowseQuery{Filter: live, Sort: core.SortTitle, Offset: 10, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if first.Items[9].Key != second.Items[0].Key {
		t.Fatalf("live predicate did not reproduce duplicate: %v %v", first.Items[9].Key, second.Items[0].Key)
	}
	t.Logf("live SQL reproduction: page 1 ends %v, page 2 starts %v (duplicate)", first.Items[9].Key, second.Items[0].Key)
	x.s.computeHidden(ctx, hiddenMovies)
	next, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeMovie})
	if err != nil {
		t.Fatal(err)
	}
	if next.HiddenMovieSeq != pinned.HiddenMovieSeq {
		t.Fatal("publication moved an active cut")
	}
	stable, err := c.Browse(ctx, core.BrowseQuery{Filter: next, Sort: core.SortTitle, Offset: 10, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if stable.Items[0].Key != core.MovieKey(12) || stable.Total != 29 {
		t.Fatalf("unstable cut: %+v", stable)
	}
	// An independent sort shares the same exclusions, a stronger contract than
	// requiring a client identifier or a continuation token.
	if _, err := x.s.db.Exec(`DELETE FROM catalog_home_release WHERE key=?`, core.MovieKey(2)); err != nil {
		t.Fatal(err)
	}
	gap, err := c.Browse(ctx, core.BrowseQuery{Filter: live, Sort: core.SortTitle, Offset: 10, Limit: 11})
	if err != nil {
		t.Fatal(err)
	}
	if gap.Items[0].Key != core.MovieKey(12) {
		t.Fatalf("live removal did not shift offset: %v", gap.Items[0].Key)
	}
	// Removing both leading keys makes the missing original boundary explicit.
	if _, err := x.s.db.Exec(`DELETE FROM catalog_home_release WHERE key=?`, core.MovieKey(1)); err != nil {
		t.Fatal(err)
	}
	gap, err = c.Browse(ctx, core.BrowseQuery{Filter: live, Sort: core.SortTitle, Offset: 10, Limit: 12})
	if err != nil {
		t.Fatal(err)
	}
	if gap.Items[0].Key != core.MovieKey(13) {
		t.Fatal("expected gap at key 12")
	}
	t.Logf("live removal reproduction: next page begins %v, skipping key %v", gap.Items[0].Key, core.MovieKey(12))
}

func TestDigitalWindowUsesHomeDate(t *testing.T) {
	now := time.Now()
	cfg := config.Availability{Mode: config.ModeDigital, MovieEvidenceDays: 90}
	title := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Released: now.AddDate(-5, 0, 0)}
	for _, tc := range []struct {
		date time.Time
		want bool
	}{{time.Time{}, true}, {now.Add(time.Hour), true}, {now.Add(-89 * 24 * time.Hour), true}, {now.Add(-90 * 24 * time.Hour), false}} {
		title.HomeReleased = tc.date
		if got := evidenceWindow(title, now, cfg); got != tc.want {
			t.Fatalf("home date %v: %v", tc.date, got)
		}
	}
}

// Stale published answers must be immediately usable while the asynchronous
// refresh is held in the catalogue. No refresh worker can delay a page.
func TestHiddenServesPublishedAnswerDuringRefresh(t *testing.T) {
	x := setup(t)
	x.s.d.Async = true
	defer x.s.Close()
	input := x.s.discoverySnapshot()
	h := &hiddenSet{key: hiddenKey(hiddenMovies, input), at: x.now.Add(-time.Minute), keys: []core.RatingKey{core.MovieKey(1)}}
	x.s.hiddenSets[hiddenMovies] = h
	x.s.refreshing = map[string]int{hiddenMovies: 1} // hold refresh without a goroutine
	got := x.s.hidden(t.Context(), hiddenMovies, input)
	if len(got) != 1 || got[0] != core.MovieKey(1) {
		t.Fatal("stale answer was not served")
	}
}

func TestShowPagingCutAndFilterIdentity(t *testing.T) {
	x := setup(t)
	input := x.s.discoverySnapshot()
	x.s.hiddenSets[hiddenShows] = &hiddenSet{key: hiddenKey(hiddenShows, input), at: x.now}
	policy := hiddenPolicy(hiddenShows, input)
	if err := x.s.publishHidden(t.Context(), hiddenShows, policy, []core.RatingKey{core.ShowKey(1)}); err != nil {
		t.Fatal(err)
	}
	filter := func() core.Filter {
		return core.Filter{Type: core.TypeShow, Group: &core.FilterGroup{Operator: "and", Children: []core.FilterGroup{{Filter: &core.Filter{Type: core.TypeShow}}}}}
	}
	before, err := x.s.PagingFilter(t.Context(), filter())
	if err != nil {
		t.Fatal(err)
	}
	if err := x.s.publishHidden(t.Context(), hiddenShows, policy, []core.RatingKey{core.ShowKey(2)}); err != nil {
		t.Fatal(err)
	}
	after, err := x.s.PagingFilter(t.Context(), filter())
	if err != nil {
		t.Fatal(err)
	}
	if before.HiddenShowSeq != after.HiddenShowSeq {
		t.Fatal("new pointer/group or generation shifted cut")
	}
	other, err := x.s.PagingFilter(core.WithBrowseScope(t.Context(), "another-client"), filter())
	if err != nil {
		t.Fatal(err)
	}
	if other.HiddenShowSeq == before.HiddenShowSeq {
		t.Fatal("new client inherited an older sequence")
	}
	x.now = x.now.Add(6 * time.Minute)
	// Serve the last publication while the next refresh is held.
	x.s.d.Async = true
	x.s.refreshing = map[string]int{hiddenShows: 1}
	defer x.s.Close()
	expired, err := x.s.PagingFilter(t.Context(), filter())
	if err != nil {
		t.Fatal(err)
	}
	if expired.HiddenShowSeq == before.HiddenShowSeq {
		t.Fatal("expired cut did not advance")
	}
}
