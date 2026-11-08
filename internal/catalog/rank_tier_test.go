package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Exercise both legal orderings, including a restart after the copy has
// captured its index definitions. Only the touched title should survive.
func TestRankTierLifecycle(t *testing.T) {
	for _, before := range []bool{true, false} {
		name := "replace_during_copy"
		if before {
			name = "replace_before_copy"
		}
		t.Run(name, func(t *testing.T) {
			c := newChunkTestCatalog(t)
			if store.DialectOf(c.db) != store.SQLite {
				t.Skip("SQLite copy/swap lifecycle")
			}
			ctx := context.Background()
			for _, id := range []int64{1, 2, 3} {
				if err := c.upsertMany(ctx, []core.Title{{Key: core.MovieKey(id), Type: core.TypeMovie, Title: "movie", Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Popularity: 0.01}}, upsertOpts{browsable: true}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := c.RebuildRank(ctx); err != nil {
				t.Fatal(err)
			}
			if before {
				if err := c.swapTitleIndexes(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if !before {
				// A crash can land between saving the schema and preparing the
				// copy. Replacement must also defer in this durable state.
				if _, err := c.tierIndexDefs(ctx); err != nil {
					t.Fatal(err)
				}
				resumed, err := New(c.db, nil, c.config(), nil)
				if err != nil {
					t.Fatal(err)
				}
				c = resumed
				if err := c.swapTitleIndexes(ctx); err != nil {
					t.Fatal(err)
				}
				if c.titleIndexesSwapped.Load() {
					t.Fatal("replacement ignored saved copy schema")
				}
			}
			touched := newTierTouched()
			touched.add(core.MovieKey(1))
			p := TierMigration{}
			if err := c.tierCopy(ctx, touched, &p); err != nil {
				t.Fatal(err)
			}
			var err error
			c, err = New(c.db, nil, c.config(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.RebuildRank(ctx); err == nil {
				t.Fatal("rank rebuild should defer during a resumable copy")
			}
			if !before {
				if err := c.swapTitleIndexes(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.tierSwap(ctx, &p); err != nil {
				t.Fatal(err)
			}
			// A rename does not fire the rank's DELETE trigger. Never publish a
			// ready rank containing the removed titles, even across restart.
			c, err = New(c.db, nil, c.config(), nil)
			if err != nil {
				t.Fatal(err)
			}
			var extras int
			if err := c.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_rank r WHERE NOT EXISTS (SELECT 1 FROM catalog_titles t WHERE t.key=r.key)`).Scan(&extras); err != nil {
				t.Fatal(err)
			}
			if c.rankReady.Load() && extras != 0 {
				t.Errorf("ready rank contains %d demoted titles after swap/restart", extras)
			}
			if !before {
				// Fail the rank publication transaction after the title swap.
				// A restart must retain invalidation and retry against keepers.
				if _, err := c.db.ExecContext(ctx, `CREATE TRIGGER fail_rank_publish BEFORE INSERT ON catalog_meta WHEN NEW.k='rank_built' BEGIN SELECT RAISE(ABORT,'injected rank publication failure'); END`); err != nil {
					t.Fatal(err)
				}
				if err := c.swapTiers(ctx, touched, &p); err == nil {
					t.Fatal("expected rank publication rollback")
				}
				c, err = New(c.db, nil, c.config(), nil)
				if err != nil {
					t.Fatal(err)
				}
				if c.rankReady.Load() {
					t.Fatal("failed rank publication restored readiness")
				}
				if _, err := c.db.ExecContext(ctx, `DROP TRIGGER fail_rank_publish`); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.swapTiers(ctx, touched, &p); err != nil {
				t.Fatal(err)
			}
			if err := c.swapTitleIndexes(ctx); err != nil {
				t.Fatal(err)
			}
			if !c.rankReady.Load() {
				t.Error("rank not rebuilt after tier completion")
			}
			var ranks, titles int
			if err := c.db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM catalog_rank),(SELECT COUNT(*) FROM catalog_titles)`).Scan(&ranks, &titles); err != nil {
				t.Fatal(err)
			}
			if ranks != 1 || titles != 1 {
				t.Errorf("final membership: ranks=%d titles=%d want touched title only", ranks, titles)
			}
			var key int64
			if err := c.db.QueryRowContext(ctx, `SELECT key FROM catalog_rank`).Scan(&key); err != nil || key != int64(core.MovieKey(1)) {
				t.Fatalf("touched rank key=%d err=%v", key, err)
			}
			for _, s := range titleIndexSwaps {
				// Execute the actual hint: existence under the old renamed table
				// is not sufficient, and an unpinned query must remain valid too.
				rows, err := c.db.QueryContext(ctx, `SELECT key FROM catalog_titles`+c.indexHint(s.old)+` WHERE type IN ('season','episode') AND detail=0 AND browsable=1 AND adult=0 LIMIT 1`)
				if err != nil {
					t.Errorf("hint %s: %v", s.old, err)
				} else {
					rows.Close()
				}
			}
		})
	}
}
