package catalog

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// A sync skips small rating, vote and popularity wobbles but never leaves a
// stored value on the wrong side of a threshold anything decides by (the
// browse bar, a catalogue rule's bound), and a title's reconcile day writes
// it exactly.
func TestRatingDriftKeepsThresholdsExact(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	key := core.MovieKey(4242)
	day := int64(key) % ratingReconcileDays
	// A day that is not the title's reconcile day, and one that is.
	off := time.Unix(((day+1)%ratingReconcileDays+ratingReconcileDays*3000)*86400+3600, 0).UTC()
	on := time.Unix((day+ratingReconcileDays*3001)*86400+3600, 0).UTC()
	now := off
	rule := core.LibraryExclusion{MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "rating", Op: "gte", Value: []byte(`7.5`)}}}
	c, err := New(db, nil, config.Catalog{MinVotes: 100, Exclusions: []core.LibraryExclusion{rule}}, nil, WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	title := core.Title{Key: key, Type: core.TypeMovie, IDs: core.IDs{TMDB: 4242}, Title: "Drift", Released: off.AddDate(-2, 0, 0), Popularity: 20, VoteCount: 98, Rating: 7.4}
	stored := func() (pop float64, votes int, rating float64) {
		t.Helper()
		if err := db.QueryRowContext(ctx, `SELECT popularity, vote_count, rating FROM catalog_titles WHERE key=?`, int64(key)).Scan(&pop, &votes, &rating); err != nil {
			t.Fatal(err)
		}
		return
	}
	sync := func(pop float64, votes int, rating float64) {
		t.Helper()
		title.Popularity, title.VoteCount, title.Rating = pop, votes, rating
		if err := c.upsertMany(ctx, []core.Title{title}, upsertOpts{browsable: true}); err != nil {
			t.Fatal(err)
		}
	}
	sync(20, 98, 7.4) // new title: exact
	sync(20.5, 99, 7.45)
	if p, v, r := stored(); p != 20 || v != 98 || r != 7.4 {
		t.Fatalf("a small wobble was written: %v %v %v", p, v, r)
	}
	sync(20.5, 100, 7.45) // crosses the browse bar (100 votes)
	if _, v, _ := stored(); v != 100 {
		t.Fatalf("crossing the vote bar not written: %v", v)
	}
	sync(20.5, 100, 7.5) // crosses the rule's 7.5 bound by 0.05
	if _, _, r := stored(); r != 7.5 {
		t.Fatalf("crossing a rule bound not written: %v", r)
	}
	sync(23, 100, 7.5) // popularity moved 15%
	if p, _, _ := stored(); p != 23 {
		t.Fatalf("a material popularity change not written: %v", p)
	}
	sync(23.5, 101, 7.52)
	now = on // the title's reconcile day writes the wobble exactly
	sync(23.5, 101, 7.52)
	if p, v, r := stored(); p != 23.5 || v != 101 || r != 7.52 {
		t.Fatalf("reconcile did not write exactly: %v %v %v", p, v, r)
	}
}
