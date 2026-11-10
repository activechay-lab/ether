package availability

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type countingRecent struct {
	*fakeCatalog
	calls int
}

func (c *countingRecent) RecentShows(ctx context.Context, since time.Time) ([]core.Title, error) {
	c.calls++
	return c.fakeCatalog.RecentShows(ctx, since)
}

// Every library page excludes the hidden evidence-window titles, so the
// answer is memoized: a second page reads no catalogue, and a new
// observation recomputes it at once.
func TestHiddenTitlesAreMemoizedUntilAnObservation(t *testing.T) {
	x := setup(t)
	recent := &countingRecent{fakeCatalog: x.cat}
	x.s.d.Catalog = recent
	show := core.Title{Key: core.ShowKey(7), Type: core.TypeShow, Title: "Series", Language: "en", Released: x.now.Add(-time.Hour)}
	x.cat.titles[show.Key] = show
	for range 3 {
		if hidden := x.s.HiddenShows(t.Context()); len(hidden) != 1 || hidden[0] != show.Key {
			t.Fatalf("hidden: %v", hidden)
		}
	}
	if recent.calls != 1 {
		t.Fatalf("read the recent catalogue %d times for three pages", recent.calls)
	}
	if err := x.s.retainFeed(t.Context(), x.f.sources[0], []core.Release{{ID: "x", Name: "Other.S01E01.1080p.WEB-DL", Indexer: "Fake"}}); err != nil {
		t.Fatal(err)
	}
	x.s.HiddenShows(t.Context())
	if recent.calls != 2 {
		t.Fatalf("an observation did not recompute the hidden titles (%d reads)", recent.calls)
	}
}

type gateCountingCatalog struct {
	*fakeCatalog
	changed int
}

func (c *gateCountingCatalog) ConsoleGateChanged() { c.changed++ }

// The hidden answer stays fresh for most of the warm cycle: with a
// shorter life every browse after a few seconds queued another full
// evidence pass behind the last, back to back for as long as anyone
// browsed.
func TestHiddenSetStaysFreshBetweenWarmCycles(t *testing.T) {
	at := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	h := &hiddenSet{key: "k", revision: 3, at: at}
	if !h.fresh("k", 3, at.Add(30*time.Second)) {
		t.Fatal("hidden set went stale inside the warm cycle")
	}
	if h.fresh("k", 3, at.Add(time.Minute)) {
		t.Fatal("hidden set outlived the warm cycle")
	}
	if h.fresh("k", 4, at.Add(time.Second)) || h.fresh("other", 3, at.Add(time.Second)) {
		t.Fatal("hidden set answered for other inputs")
	}
}

// Republishing an unchanged set keeps its published rows answering: the
// seq stays current with every member present and no new rows written.
func TestPublishHiddenUnchangedKeepsPublishedRows(t *testing.T) {
	x := setup(t)
	keys := []core.RatingKey{core.ShowKey(1), core.ShowKey(2), core.ShowKey(3)}
	for range 3 {
		if err := x.s.publishHidden(t.Context(), hiddenShows, "p", keys); err != nil {
			t.Fatal(err)
		}
	}
	var seq int64
	if err := x.s.db.QueryRowContext(t.Context(), `SELECT seq FROM availability_hidden_current WHERE kind=?`, hiddenShows).Scan(&seq); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := x.s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM availability_hidden WHERE kind=? AND from_seq<=? AND to_seq>?`, hiddenShows, seq, seq).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != len(keys) {
		t.Fatalf("published snapshot holds %d of %d keys", n, len(keys))
	}
	var total int
	if err := x.s.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM availability_hidden WHERE kind=?`, hiddenShows).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != len(keys) {
		t.Fatalf("unchanged republishes wrote %d rows for %d keys", total, len(keys))
	}
}

// Republishing the same hidden set must keep the catalogue's page cache:
// a refresh on a large library otherwise made every browse a cache miss.
// A new set or policy still invalidates it.
func TestPublishHiddenInvalidatesOnlyOnChange(t *testing.T) {
	x := setup(t)
	cat := &gateCountingCatalog{fakeCatalog: x.cat}
	x.s.d.Catalog = cat
	publish := func(policy string, keys ...core.RatingKey) {
		t.Helper()
		if err := x.s.publishHidden(t.Context(), hiddenShows, policy, keys); err != nil {
			t.Fatal(err)
		}
	}
	publish("p", core.ShowKey(1))
	publish("p", core.ShowKey(1))
	if cat.changed != 1 {
		t.Fatalf("an unchanged set invalidated the catalogue %d times", cat.changed)
	}
	publish("p", core.ShowKey(1), core.ShowKey(2))
	publish("q", core.ShowKey(1), core.ShowKey(2))
	if cat.changed != 3 {
		t.Fatalf("a changed set or policy invalidated %d times, want 3", cat.changed)
	}
}
