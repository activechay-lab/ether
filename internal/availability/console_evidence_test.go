package availability

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

type projectionCatalog struct {
	*fakeCatalog
	until       int64
	cached      int
	invalidated []string
	writes      int
}

func (c *projectionCatalog) RecordConsoleReleaseEvidence(_ context.Context, _ core.RatingKey, _ core.Release, _, _ string, until, _ int64, cached int) error {
	c.until = until
	c.cached = cached
	c.writes++
	return nil
}
func (c *projectionCatalog) InvalidateConsoleReleaseEvidence(_ context.Context, id string) {
	c.invalidated = append(c.invalidated, id)
}
func TestConsoleProjectionUsesCheckedEvidenceNotAddonHints(t *testing.T) {
	x := setup(t)
	ctx := t.Context()
	c := &projectionCatalog{fakeCatalog: x.cat}
	x.s.d.Catalog = c
	r := x.release("r")
	r.DebridCached = []string{"rd"}
	r.Info = core.ReleaseInfo{Height: 2160, HDR10: true}
	x.f.pages = [][]core.Release{{r}}
	if err := x.s.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if c.writes == 0 || c.until != x.now.Unix()+21600 || c.cached != -1 {
		t.Fatalf("projection %+v", c)
	}
	c.writes = 0
	x.s.backfillConsoleEvidence(ctx)
	if c.writes != 1 || c.until != x.now.Unix()+21600 {
		t.Fatalf("backfill %+v", c)
	}
	x.s.Invalidate(ctx, "r")
	if len(c.invalidated) != 1 || c.invalidated[0] != "r" {
		t.Fatalf("invalidation %v", c.invalidated)
	}
	x.s.publishConsoleEvidence(ctx, core.MovieKey(1), r, "f", x.s.policy(), -1)
	if c.until != 0 {
		t.Fatal("invalidated source resurrected as playable")
	}
}
