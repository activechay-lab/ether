package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// The series-sum total of a whole-library episode listing matches a direct
// count of the episodes as series and episodes are added, changed and
// removed, for every viewer and with episodes not yet aired.
func TestStoredEpisodeTotalBySeriesMatchesDirectCount(t *testing.T) {
	ctx := context.Background()
	c := newChunkTestCatalog(t)
	now := c.now()
	put := func(ti core.Title, opts upsertOpts) {
		t.Helper()
		if err := c.upsert(ctx, ti, opts); err != nil {
			t.Fatal(err)
		}
	}
	for id := int64(1); id <= 6; id++ {
		rating := []string{"TV-PG", "TV-14", "TV-MA"}[id%3]
		show := core.Title{Key: core.ShowKey(id), Type: core.TypeShow, Title: "Show", ContentRating: rating, Released: now.AddDate(-1, 0, 0)}
		put(show, upsertOpts{browsable: true})
		for ep := 1; ep <= 5; ep++ {
			aired := now.AddDate(0, 0, -30+ep*7) // the last ones air in the future
			put(core.Title{Key: core.EpisodeKey(id, 1, ep), Type: core.TypeEpisode, ShowKey: show.Key, Season: 1, Episode: ep, Title: "Ep", Released: aired, ContentRating: rating}, upsertOpts{})
		}
	}
	direct := func(f core.Filter) int {
		t.Helper()
		where, args, err := c.storedEpisodesWhere(f)
		if err != nil {
			t.Fatal(err)
		}
		var n int
		if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles e JOIN catalog_titles t ON t.key=e.show_key WHERE e.key>? AND e.key<? AND `+where,
			append([]any{episodeKeyStart, episodeKeyEnd}, args...)...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	check := func(when string) {
		t.Helper()
		for _, f := range []core.Filter{
			{Type: core.TypeShow},
			{Type: core.TypeShow, MaxContentRating: "TV-14"},
			{Type: core.TypeShow, ExcludeContentRatings: []string{"TV-PG"}},
		} {
			got, ok, err := c.countStoredEpisodesBySeries(ctx, f)
			if err != nil || !ok {
				t.Fatalf("%s: by series %v %v", when, ok, err)
			}
			if want := direct(f); got != want || want == 0 {
				t.Fatalf("%s: filter %+v: by series %d, direct %d", when, f, got, want)
			}
		}
	}
	check("stored")
	// An episode turned adult, one moved to an unknown release, one removed.
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET adult=1 WHERE key=?`, int64(core.EpisodeKey(1, 1, 1))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, `UPDATE catalog_titles SET released_ts=0 WHERE key=?`, int64(core.EpisodeKey(2, 1, 2))); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.ExecContext(ctx, `DELETE FROM catalog_titles WHERE key=?`, int64(core.EpisodeKey(3, 1, 1))); err != nil {
		t.Fatal(err)
	}
	check("changed")
	// Time passes: an episode that had not aired now counts.
	c.clock.Store(func() time.Time { return now.AddDate(0, 0, 30) })
	check("later")
	// A bounded episode exclusion is subtracted from the series sum.
	f := core.Filter{Type: core.TypeShow, ExcludeKeys: []core.RatingKey{core.EpisodeKey(4, 1, 1)}}
	if got, ok, err := c.countStoredEpisodesBySeries(ctx, f); err != nil || !ok || got != direct(f) {
		t.Fatalf("episode exclusion: by series %d, direct %d, usable %v, error %v", got, direct(f), ok, err)
	}
	// A published episode hidden set is subtracted too, not a reason to join
	// every episode: aired and unaired members, a kept one, and one the
	// filter already excludes are each counted once.
	if _, err := c.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS availability_hidden(kind TEXT NOT NULL,generation TEXT NOT NULL,rating_key BIGINT NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(kind,generation,rating_key))`); err != nil {
		t.Fatal(err)
	}
	for _, k := range []core.RatingKey{core.EpisodeKey(5, 1, 1), core.EpisodeKey(5, 1, 2), core.EpisodeKey(6, 1, 3), core.EpisodeKey(4, 1, 1), core.EpisodeKey(2, 1, 2)} {
		if _, err := c.db.ExecContext(ctx, `INSERT INTO availability_hidden VALUES('episode','gen',?,0)`, int64(k)); err != nil {
			t.Fatal(err)
		}
	}
	// A retained older generation is inert.
	if _, err := c.db.ExecContext(ctx, `INSERT INTO availability_hidden VALUES('episode','old',?,0)`, int64(core.EpisodeKey(1, 1, 3))); err != nil {
		t.Fatal(err)
	}
	c.clock.Store(func() time.Time { return now })
	for _, f := range []core.Filter{
		{Type: core.TypeShow, HiddenEpisodeGeneration: "gen"},
		{Type: core.TypeShow, HiddenEpisodeGeneration: "gen", MaxContentRating: "TV-14", AvailabilityKeepKeys: []core.RatingKey{core.EpisodeKey(5, 1, 2)}, ExcludeKeys: []core.RatingKey{core.EpisodeKey(4, 1, 1)}},
	} {
		got, ok, err := c.countStoredEpisodesBySeries(ctx, f)
		if err != nil || !ok {
			t.Fatalf("hidden set: by series usable %v, error %v", ok, err)
		}
		if want := direct(f); got != want || want == 0 {
			t.Fatalf("hidden set %+v: by series %d, direct %d", f, got, want)
		}
	}
}
