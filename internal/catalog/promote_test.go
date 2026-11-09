package catalog_test

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestMigrateTiersPromotesCollectionMembersToLibraryTier(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	cat, err := catalog.New(db, nil, config.Catalog{MinPopularity: 50, MinVotes: 500}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	collMember := core.Title{
		Key:        core.MovieKey(101),
		Type:       core.TypeMovie,
		Title:      "Obscure Collection Pick",
		Year:       2021,
		Released:   now.AddDate(-5, 0, 0),
		Popularity: 0.5,
		VoteCount:  3,
	}
	untouchedTail := core.Title{
		Key:        core.MovieKey(102),
		Type:       core.TypeMovie,
		Title:      "Untouched Long Tail",
		Year:       2021,
		Released:   now.AddDate(-5, 0, 0),
		Popularity: 0.4,
		VoteCount:  2,
	}
	// Stored in search-only tier (browsable=false).
	if err := cat.UpsertTitle(ctx, collMember, false); err != nil {
		t.Fatal(err)
	}
	if err := cat.UpsertTitle(ctx, untouchedTail, false); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS collections_members (collection_id INTEGER NOT NULL, ord INTEGER NOT NULL, key INTEGER NOT NULL, PRIMARY KEY(collection_id, ord))`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO collections_members(collection_id, ord, key) VALUES (1, 0, ?)`, int64(collMember.Key)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `INSERT INTO catalog_meta(k, v) VALUES ('tier_migrate_armed', '1700000000') ON CONFLICT(k) DO UPDATE SET v=excluded.v`); err != nil {
		t.Fatal(err)
	}
	cat.SetTierBackup(func(context.Context, time.Time) (string, error) { return "test-backup.db", nil })

	if err := cat.MigrateTiers(ctx); err != nil {
		t.Fatal(err)
	}

	page, err := cat.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].Key != collMember.Key {
		t.Fatalf("expected collection member promoted to browsable library tier; got total=%d items=%+v", page.Total, page.Items)
	}
}

func TestMirrorValidKeysMatchesTitles(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	released := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Title: "Released", Released: now.Add(-24 * time.Hour)}
	adult := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Adult", Released: now.Add(-24 * time.Hour), Adult: true}
	future := core.Title{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "Future", Released: now.Add(48 * time.Hour)}
	for _, item := range []core.Title{released, adult, future} {
		if err := cat.UpsertTitle(ctx, item, true); err != nil {
			t.Fatal(err)
		}
	}

	m := catalog.NewMirror(db, func() time.Time { return now })
	input := []core.RatingKey{core.MovieKey(999), future.Key, adult.Key, released.Key}

	gotNow, err := m.ValidKeys(ctx, input, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotNow) != 1 || gotNow[0] != released.Key {
		t.Fatalf("ValidKeys(includeFuture=false) = %v, want [%v]", gotNow, released.Key)
	}

	gotFuture, err := m.ValidKeys(ctx, input, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotFuture) != 2 || gotFuture[0] != future.Key || gotFuture[1] != released.Key {
		t.Fatalf("ValidKeys(includeFuture=true) = %v, want [%v %v]", gotFuture, future.Key, released.Key)
	}
}
