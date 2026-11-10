package plex

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// Prune keeps every id a live session holds, a play queue renders, or a
// listing used within 90 days; it removes the rest from the table and from
// memory; and a title re-listed afterwards gets an id that names it alone.
func TestVariantPruneKeepsHeldIDsAndNeverReusesOne(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	clock := time.Now()
	r := newVariantIDRegistryAt(testStores(), db, func() time.Time { return clock })
	day := 24 * time.Hour

	live := variantRef{key: fightClub.Key, releaseID: "rel-live"}
	queued := variantRef{key: alien.Key, releaseID: "rel-queued"}
	recent := variantRef{key: breakingBad.Key, releaseID: "rel-recent"}
	old := variantRef{key: bbE1.Key, releaseID: "rel-old"}
	// squatter holds old's hash slot, so old was probed past it.
	squatter := variantRef{key: bbE2.Key, releaseID: "rel-squatter"}
	slot := variantHash(old.key, old.releaseID)
	if _, err := db.ExecContext(ctx, `INSERT INTO variants_ids(media_id, rating_key, release_id, last_seen) VALUES (?,?,?,?)`, slot, int64(squatter.key), squatter.releaseID, clock.Unix()); err != nil {
		t.Fatal(err)
	}
	ids := map[variantRef]int64{}
	for _, ref := range []variantRef{live, queued, recent, old} {
		ids[ref] = r.idFor(ref.key, ref.releaseID)
	}
	if ids[old] == slot {
		t.Fatal("old was not probed past the squatter")
	}

	// 95 days on, a listing shows recent and the squatter; at 100 days a
	// play queue renders queued; the live session has not re-listed since.
	clock = clock.Add(95 * day)
	r.idFor(recent.key, recent.releaseID)
	r.lookup(slot)
	clock = clock.Add(5 * day)
	r.idFor(queued.key, queued.releaseID)
	if err := r.flush(ctx); err != nil {
		t.Fatal(err)
	}
	srv := &Server{variantIDs: r, playIDs: newPlayIdentities()}
	srv.playIDs.m[playIdentityKey{userID: 1, clientID: "tv", key: live.key}] = &playIdentity{
		frozen: true, selected: ids[live],
		variants: []core.ReleaseVariant{{Auto: true, Release: core.Release{ID: "rel-auto"}}, {Release: core.Release{ID: live.releaseID}}},
	}
	if n, err := srv.Prune(ctx); err != nil || n != 1 {
		t.Fatalf("pruned %d: %v", n, err)
	}
	for _, ref := range []variantRef{live, queued, recent} {
		if got, ok := r.lookup(ids[ref]); !ok || got != ref {
			t.Fatalf("held id %d for %v lost: %v %v", ids[ref], ref, got, ok)
		}
	}
	if _, ok := r.lookup(ids[old]); ok {
		t.Fatal("pruned id still in memory")
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM variants_ids`).Scan(&rows); err != nil || rows != 4 {
		t.Fatalf("rows = %d (%v), want 4", rows, err)
	}

	// Re-listed after the prune: never a live id, and only old's.
	again := r.idFor(old.key, old.releaseID)
	for ref, id := range ids {
		if ref != old && id == again {
			t.Fatalf("re-listed id %d collides with %v", again, ref)
		}
	}
	if again == slot {
		t.Fatal("re-listed id took the squatter's slot")
	}
	restarted := newVariantIDRegistry(testStores(), db)
	if got, ok := restarted.lookup(again); !ok || got != old {
		t.Fatalf("re-listed id %d after restart = %v %v", again, got, ok)
	}
	if got, ok := restarted.lookup(slot); !ok || got != squatter {
		t.Fatalf("squatter after restart = %v %v", got, ok)
	}
}
