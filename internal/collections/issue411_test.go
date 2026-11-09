package collections

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestMixedCollectionRowKeepsMoviesBehindShows(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	var keys []string
	for i := 1; i <= 15; i++ {
		sk := core.ShowKey(int64(100 + i))
		if err := cat.UpsertTitle(ctx, core.Title{
			Key: sk, Type: core.TypeShow, Title: fmt.Sprintf("Show %d", i),
			Year: 2022, Released: now.AddDate(-2, 0, 0), Popularity: float64(200 - i),
		}, true); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, sk.String())
	}
	for i := 1; i <= 32; i++ {
		mk := core.MovieKey(int64(500 + i))
		if err := cat.UpsertTitle(ctx, core.Title{
			Key: mk, Type: core.TypeMovie, Title: fmt.Sprintf("Movie %d", i),
			Year: 2023, Released: now.AddDate(-1, 0, 0), Popularity: float64(100 - i),
		}, true); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, mk.String())
	}

	s, err := New(db, cat, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	sp, err := s.CreateSpec(ctx, core.CollectionSpec{
		Name:      "Mixed MDBList",
		MediaType: MediaMixed,
		Sources:   []core.CollectionSource{{Kind: "titles", Keys: keys}},
		Sort:      "source",
		Refresh:   "daily",
		Enabled:   true,
		Targets:   core.CollectionTargets{HomeRow: true, SectionRow: true, CollectionsTab: true, Users: core.AllUsers()},
	})
	if err != nil {
		t.Fatal(err)
	}

	owner := &core.User{ID: 1, IsOwner: true}
	rows, err := s.HomeRowsFor(ctx, owner, core.HubContext{})
	if err != nil {
		t.Fatal(err)
	}
	var found *core.CollectionRow
	for i := range rows {
		if rows[i].CollectionID == sp.ID {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		t.Fatal("mixed collection row missing from HomeRowsFor")
	}
	movies := 0
	for _, it := range found.Hub.Items {
		if it.Type == core.TypeMovie {
			movies++
		}
	}
	if movies != rowItems || !found.Hub.More {
		t.Fatalf("mixed row movies=%d (want %d) more=%v (want true)", movies, rowItems, found.Hub.More)
	}

	layoutRow, err := s.LayoutRow(ctx, owner, core.HubContext{}, sp.ID)
	if err != nil || layoutRow == nil {
		t.Fatalf("LayoutRow: row=%v err=%v", layoutRow, err)
	}
	layoutMovies := 0
	for _, it := range layoutRow.Hub.Items {
		if it.Type == core.TypeMovie {
			layoutMovies++
		}
	}
	if layoutMovies != rowItems || !layoutRow.Hub.More {
		t.Fatalf("layout mixed row movies=%d (want %d) more=%v (want true)", layoutMovies, rowItems, layoutRow.Hub.More)
	}
}

func TestItemsPagingFiltersInvalidMembersBeforeSlicing(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}

	var validKeys []core.RatingKey
	for i := 1; i <= 10; i++ {
		mk := core.MovieKey(int64(800 + i))
		if err := cat.UpsertTitle(ctx, core.Title{
			Key: mk, Type: core.TypeMovie, Title: fmt.Sprintf("Valid %d", i),
			Year: 2022, Released: now.AddDate(-2, 0, 0), Popularity: 20,
		}, true); err != nil {
			t.Fatal(err)
		}
		validKeys = append(validKeys, mk)
	}
	s, err := New(db, cat, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	sp, err := s.CreateSpec(ctx, core.CollectionSpec{
		Name:      "Filtered Paging",
		MediaType: MediaMovie,
		Sources:   []core.CollectionSource{{Kind: "titles", Keys: keyStrings(validKeys)}},
		Sort:      "source",
		Refresh:   "daily",
		Enabled:   true,
		Targets:   core.CollectionTargets{CollectionsTab: true, Users: core.AllUsers()},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Remove two first-page members from catalog_titles after build (simulating
	// evicted/adult/unreleased rows still listed in collections_members).
	if _, err := db.ExecContext(ctx, `DELETE FROM catalog_titles WHERE key IN (?, ?)`, int64(validKeys[1]), int64(validKeys[3])); err != nil {
		t.Fatal(err)
	}

	owner := &core.User{ID: 1, IsOwner: true}
	var smartID int64
	fmt.Sscan(sp.ID, &smartID)
	ck := core.SmartKey(smartID)

	p1, err := s.Items(ctx, owner, ck, 0, 4)
	if err != nil {
		t.Fatal(err)
	}
	p2, err := s.Items(ctx, owner, ck, 4, 4)
	if err != nil {
		t.Fatal(err)
	}
	if p1.Total != 8 || len(p1.Items) != 4 || len(p2.Items) != 4 {
		t.Fatalf("expected 2 full pages of 4 (total 8); got p1=%d/%d p2=%d/%d", len(p1.Items), p1.Total, len(p2.Items), p2.Total)
	}
	want := []core.RatingKey{validKeys[0], validKeys[2], validKeys[4], validKeys[5], validKeys[6], validKeys[7], validKeys[8], validKeys[9]}
	for i, it := range append(p1.Items, p2.Items...) {
		if it.Key != want[i] {
			t.Fatalf("pos %d: got %v, want %v", i, it.Key, want[i])
		}
	}
}

func TestBuiltCollectionServesWhileIndexIsBuilding(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	mk := core.MovieKey(901)
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: mk, Type: core.TypeMovie, Title: "Ready Immediately",
		Year: 2023, Released: now.AddDate(-1, 0, 0), ContentRating: "PG-13", PosterPath: "/p.jpg", Popularity: 50,
	}, true); err != nil {
		t.Fatal(err)
	}
	s, err := New(db, cat, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	sp, err := s.CreateSpec(ctx, core.CollectionSpec{
		Name:      "Pinned Built Collection",
		MediaType: MediaMovie,
		Sources:   []core.CollectionSource{{Kind: "titles", Keys: []string{mk.String()}}},
		Sort:      "source",
		Refresh:   "daily",
		Enabled:   true,
		Targets:   core.CollectionTargets{HomeRow: true, CollectionsTab: true, Users: core.AllUsers()},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate cold start / post-tier-migration rebuild blocking full-catalogue index load.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	s.idx = newIndex(db, s.now, time.Minute)
	s.idx.beforeLoad = func() { <-release }
	s.mu.Lock()
	s.members = map[int64]*memberSet{}
	s.mu.Unlock()

	owner := &core.User{ID: 1, IsOwner: true}
	lr, err := s.LayoutRow(ctx, owner, core.HubContext{}, sp.ID)
	if err != nil || lr == nil || len(lr.Hub.Items) != 1 || lr.Hub.Items[0].Key != mk {
		t.Fatalf("LayoutRow while index building: row=%+v err=%v", lr, err)
	}
	var smartID int64
	fmt.Sscan(sp.ID, &smartID)
	ck := core.SmartKey(smartID)
	col, err := s.Get(ctx, owner, ck)
	if err != nil || col == nil || col.ChildCount != 1 || col.PosterPath != "/p.jpg" {
		t.Fatalf("Get while index building: col=%+v err=%v", col, err)
	}
}

func TestRepairMemberTiersPromotesSearchTierMembers(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	cat, err := catalog.New(db, nil, config.Catalog{MinPopularity: 100, MinVotes: 1000}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	mk := core.MovieKey(950)
	if err := cat.UpsertTitle(ctx, core.Title{
		Key: mk, Type: core.TypeMovie, Title: "Search Tier Member",
		Year: 2021, Released: now.AddDate(-3, 0, 0), Popularity: 0.2, VoteCount: 1,
	}, false); err != nil {
		t.Fatal(err)
	}
	s, err := New(db, cat, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	sp, err := s.CreateSpec(ctx, core.CollectionSpec{
		Name:      "Promotes Tail Members",
		MediaType: MediaMovie,
		Sources:   []core.CollectionSource{{Kind: "titles", Keys: []string{mk.String()}}},
		Sort:      "source",
		Refresh:   "daily",
		Enabled:   true,
		Targets:   core.CollectionTargets{CollectionsTab: true, Users: core.AllUsers()},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = sp

	// Verify CreateSpec/buildNow promoted mk to browsable=1.
	page, err := cat.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil || page.Total != 1 || page.Items[0].Key != mk {
		t.Fatalf("buildNow should promote member to browsable=1: page=%+v err=%v", page, err)
	}

	// Simulate a database upgraded from 0.3.6 where browsable was 0 on an already-built member.
	if _, err := db.ExecContext(ctx, `UPDATE catalog_titles SET browsable = 0 WHERE key = ?`, int64(mk)); err != nil {
		t.Fatal(err)
	}
	s.repairedTiers.Store(false)
	s.RunDue(ctx)

	var browsable int
	if err := db.QueryRowContext(ctx, `SELECT browsable FROM catalog_titles WHERE key = ?`, int64(mk)).Scan(&browsable); err != nil {
		t.Fatal(err)
	}
	if browsable != 1 {
		t.Fatalf("repairMemberTiers left browsable=%d, want 1", browsable)
	}
}

func keyStrings(ks []core.RatingKey) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = k.String()
	}
	return out
}
