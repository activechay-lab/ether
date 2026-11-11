package catalog_test

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestAiredShowEpisodes(t *testing.T) {
	// Season 2 premiered 2026-03-01 with S02E01 on 03-01, S02E02 on 03-10, and S02E03 undated.
	clock := &testClock{t: time.Date(2026, 3, 5, 12, 0, 0, 0, time.UTC)}
	fake := &returningSeries{}
	cat, _ := newAiredCatalog(t, fake.server(t), clock)
	ctx := context.Background()

	showKey := core.ShowKey(700)
	if _, err := cat.Grandchildren(ctx, showKey); err != nil {
		t.Fatal(err)
	}

	gotEarly, err := cat.AiredShowEpisodes(ctx, []core.RatingKey{showKey, core.ShowKey(999999)})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(gotEarly[showKey])
	wantEarly := []core.RatingKey{
		core.EpisodeKey(700, 0, 1),
		core.EpisodeKey(700, 1, 1),
		core.EpisodeKey(700, 1, 2),
		core.EpisodeKey(700, 2, 1),
	}
	if !reflect.DeepEqual(gotEarly[showKey], wantEarly) {
		t.Fatalf("2026-03-05 AiredShowEpisodes(%v) = %v, want %v", showKey, gotEarly[showKey], wantEarly)
	}
	if len(gotEarly[core.ShowKey(999999)]) != 0 {
		t.Fatalf("unexpected episodes for missing show: %v", gotEarly[core.ShowKey(999999)])
	}

	// Advancing past S02E02's 2026-03-10 air date includes S02E02 without network
	// calls, while undated S02E03 remains excluded.
	clock.set(time.Date(2026, 3, 15, 12, 0, 0, 0, time.UTC))
	gotLate, err := cat.AiredShowEpisodes(ctx, []core.RatingKey{showKey})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(gotLate[showKey])
	wantLate := []core.RatingKey{
		core.EpisodeKey(700, 0, 1),
		core.EpisodeKey(700, 1, 1),
		core.EpisodeKey(700, 1, 2),
		core.EpisodeKey(700, 2, 1),
		core.EpisodeKey(700, 2, 2),
	}
	if !reflect.DeepEqual(gotLate[showKey], wantLate) {
		t.Fatalf("2026-03-15 AiredShowEpisodes(%v) = %v, want %v", showKey, gotLate[showKey], wantLate)
	}
}
