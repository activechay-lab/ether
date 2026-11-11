package availability

import (
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type gateCountingCatalog struct {
	*fakeCatalog
	changed int
}

func (c *gateCountingCatalog) ConsoleGateChanged() { c.changed++ }

// openGate notifies the catalogue and bumps gateRev only when a new
// (policy, rating_key) row is inserted; duplicate observations are no-ops.
func TestOpenGateNotifiesCatalogOnlyOnNewRow(t *testing.T) {
	x := setup(t)
	cat := &gateCountingCatalog{fakeCatalog: x.cat}
	x.s.d.Catalog = cat
	gp := x.s.currentGatePolicy()
	before := x.s.gateRev.Load()
	for range 3 {
		if err := x.s.openGate(t.Context(), gp, core.MovieKey(1), "f", x.now.Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if cat.changed != 1 || x.s.gateRev.Load() != before+1 {
		t.Fatalf("duplicate openGate changed=%d rev=%d->%d", cat.changed, before, x.s.gateRev.Load())
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
