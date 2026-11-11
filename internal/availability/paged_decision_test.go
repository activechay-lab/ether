package availability

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestPagedEpisodeStickyGateSQL(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	c, err := catalog.New(x.s.db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return x.now }))
	if err != nil {
		t.Fatal(err)
	}
	x.s.d.Catalog = c
	show := core.Title{Key: core.ShowKey(99), Type: core.TypeShow, Title: "Show", Released: x.now.Add(-time.Hour)}
	ep := core.Title{Key: core.EpisodeKey(99, 1, 1), ShowKey: show.Key, Type: core.TypeEpisode, Title: "Episode", ShowTitle: "Show", Season: 1, Episode: 1, Released: x.now.Add(-time.Hour)}
	if err := c.UpsertTitle(ctx, show, true); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertTitle(ctx, ep, true); err != nil {
		t.Fatal(err)
	}
	f, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeEpisode})
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("ungated episode visible: %+v %v", page, err)
	}
	if err := x.s.remember(ctx, x.f.sources[0], ep, x.release("ep1"), x.s.policy()); err != nil {
		t.Fatal(err)
	}
	f, err = x.s.PagingFilter(ctx, core.Filter{Type: core.TypeEpisode})
	if err != nil {
		t.Fatal(err)
	}
	page, err = c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
	if err != nil || page.Total != 1 || page.Items[0].Key != ep.Key {
		t.Fatalf("opened episode missing: %+v %v", page, err)
	}
	x.s.Invalidate(ctx, "ep1")
	f, err = x.s.PagingFilter(ctx, core.Filter{Type: core.TypeEpisode})
	if err != nil {
		t.Fatal(err)
	}
	page, err = c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
	if err != nil || page.Total != 1 {
		t.Fatalf("Invalidate closed sticky episode gate: %+v %v", page, err)
	}
}

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

func TestHiddenMigrationTranslatesToPostgres(t *testing.T) {
	ddl := store.TranslateDDL(hiddenMigration)
	for _, want := range []string{"rating_key BIGINT NOT NULL", "created_at BIGINT NOT NULL", "PRIMARY KEY(kind,generation,rating_key)"} {
		if !strings.Contains(ddl, want) {
			t.Fatalf("translated migration missing %q", want)
		}
	}
}

func TestGateMigrationTranslatesToPostgres(t *testing.T) {
	ddl := store.TranslateDDL(gateMigration)
	for _, want := range []string{"rating_key BIGINT NOT NULL", "PRIMARY KEY(policy, rating_key)", "availability_title_checks_due"} {
		if !strings.Contains(ddl, want) {
			t.Fatalf("translated gate migration missing %q: %s", want, ddl)
		}
	}
}
