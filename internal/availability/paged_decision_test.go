package availability

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type pagedEpisodeCatalog struct{ *fakeCatalog }

func (c *pagedEpisodeCatalog) RecentEvidenceEpisodes(_ context.Context, since time.Time) ([]core.Title, error) {
	var out []core.Title
	for _, t := range c.titles {
		if t.Type == core.TypeEpisode && t.Released.After(since) {
			out = append(out, t)
		}
	}
	return out, nil
}

// A page must not use an episode decision invalidated by a release change.
func TestPagedEpisodeDecisionWaitsForInvalidation(t *testing.T) {
	x := setup(t)
	ep := core.Title{Key: core.EpisodeKey(99, 1, 1), Type: core.TypeEpisode, Title: "Episode", Released: x.now.Add(-time.Hour)}
	x.cat.titles[ep.Key] = ep
	x.s.d.Catalog = &pagedEpisodeCatalog{x.cat}
	x.s.d.Async = true
	defer x.s.Close()
	input := x.s.discoverySnapshot()
	x.s.visibilityMu.Lock()
	for _, kind := range []string{hiddenMovies, hiddenShows, hiddenEpisodes} {
		x.s.hiddenSets[kind] = &hiddenSet{key: hiddenKey(kind, input), at: x.now, covered: map[core.RatingKey]bool{ep.Key: true}, hidden: map[core.RatingKey]bool{}}
	}
	x.s.visibilityMu.Unlock()
	x.s.clearVisibility()
	f, err := x.s.PagingFilter(t.Context(), core.Filter{Type: core.TypeEpisode})
	if err != nil {
		t.Fatal(err)
	}
	var count int
	if err := x.s.db.QueryRow(`SELECT COUNT(*) FROM availability_hidden WHERE kind='episode' AND from_seq<=? AND to_seq>? AND rating_key=?`, f.HiddenEpisodeSeq, f.HiddenEpisodeSeq, ep.Key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("invalidated unavailable episode was not excluded")
	}
}

// Delta publication: publishing A+x-y inserts only x and closes only y, a
// reader pinned to the older seq still sees precisely A, and republishing
// the unchanged set writes no membership rows. Covers the empty set too.
func TestPublishHiddenDeltaKeepsPinnedSnapshots(t *testing.T) {
	x := setup(t)
	ctx := t.Context()
	a, b, c := core.ShowKey(101), core.ShowKey(102), core.ShowKey(103)
	members := func(seq int64) map[core.RatingKey]bool {
		t.Helper()
		rows, err := x.s.db.QueryContext(ctx, `SELECT rating_key FROM availability_hidden WHERE kind=? AND from_seq<=? AND to_seq>?`, hiddenShows, seq, seq)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		out := map[core.RatingKey]bool{}
		for rows.Next() {
			var k int64
			if err := rows.Scan(&k); err != nil {
				t.Fatal(err)
			}
			out[core.RatingKey(k)] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	curSeq := func() int64 {
		t.Helper()
		var seq int64
		if err := x.s.db.QueryRowContext(ctx, `SELECT seq FROM availability_hidden_current WHERE kind=?`, hiddenShows).Scan(&seq); err != nil {
			t.Fatal(err)
		}
		return seq
	}
	rowCount := func() int {
		t.Helper()
		var n int
		if err := x.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM availability_hidden WHERE kind=?`, hiddenShows).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if err := x.s.publishHidden(ctx, hiddenShows, "test", []core.RatingKey{a, b}); err != nil {
		t.Fatal(err)
	}
	before := curSeq()
	if got := members(before); len(got) != 2 || !got[a] || !got[b] {
		t.Fatalf("first snapshot = %v", got)
	}
	if err := x.s.publishHidden(ctx, hiddenShows, "test", []core.RatingKey{a, b}); err != nil {
		t.Fatal(err)
	}
	if curSeq() != before || rowCount() != 2 {
		t.Fatalf("unchanged publish wrote rows: seq %d!=%d rows=%d", curSeq(), before, rowCount())
	}
	if err := x.s.publishHidden(ctx, hiddenShows, "test", []core.RatingKey{b, c}); err != nil {
		t.Fatal(err)
	}
	after := curSeq()
	if after != before+1 {
		t.Fatalf("seq did not advance by one: %d -> %d", before, after)
	}
	// One insert (c) and one close (a): 3 interval rows, not a fresh copy.
	if rowCount() != 3 {
		t.Fatalf("delta publish left %d rows, want 3", rowCount())
	}
	if got := members(before); len(got) != 2 || !got[a] || !got[b] {
		t.Fatalf("pinned old snapshot changed: %v", got)
	}
	if got := members(after); len(got) != 2 || !got[b] || !got[c] {
		t.Fatalf("new snapshot = %v", got)
	}
	if err := x.s.publishHidden(ctx, hiddenShows, "test", nil); err != nil {
		t.Fatal(err)
	}
	if empty := curSeq(); empty == before || empty == after || len(members(empty)) != 0 {
		t.Fatalf("empty set not published at a new seq: %d", empty)
	}
}

// SQLite's 64-bit rating keys must remain 64-bit on PostgreSQL.
func TestHiddenMigrationTranslatesToPostgres(t *testing.T) {
	ddl := store.TranslateDDL(hiddenMigration)
	for _, want := range []string{"rating_key BIGINT NOT NULL", "created_at BIGINT NOT NULL", "PRIMARY KEY(kind,generation,rating_key)"} {
		if !strings.Contains(ddl, want) {
			t.Fatalf("translated migration missing %q", want)
		}
	}
}
