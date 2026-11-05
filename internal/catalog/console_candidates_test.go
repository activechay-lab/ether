package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"os"
	"testing"
	"time"
)

func TestConsoleEvidenceCandidatePagesPreservePositiveAndUnknownMembership(t *testing.T) {
	c := largeCatalogue(t, 30)
	ctx := context.Background()
	c.SetConsoleAvailabilityPolicy(func() string { return "candidate-test" })
	until := time.Now().Add(time.Hour).Unix()
	for _, v := range []struct {
		id             int64
		height, cached int
	}{{2, 2160, 1}, {9, 2160, 0}, {17, 1080, 1}} {
		release := core.Release{ID: "source", Info: core.ReleaseInfo{}}
		release.Info.Height = v.height
		if err := c.RecordConsoleReleaseEvidence(ctx, core.MovieKey(v.id), release, "fixture", "candidate-test", until, until, v.cached); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		f    core.Filter
		want []int64
	}{
		{core.Filter{Playable: "yes", Qualities: []string{"4k"}}, []int64{2, 9}},
		{core.Filter{DebridCached: "yes", Qualities: []string{"4k"}}, []int64{2}},
	} {
		tc.f.LocalOnly = true
		tc.f.Type = core.TypeMovie
		if os.Getenv("ETHER_CATALOG_PLAN") == "1" {
			consoleEvidencePlan(t, c, core.BrowseQuery{Filter: tc.f})
		}
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: tc.f, Sort: core.SortPopularity, Desc: true, Limit: 49, NoTotal: true, CursorPage: true})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != len(tc.want) {
			t.Fatalf("items %d expected %d", len(page.Items), len(tc.want))
		}
		for i, id := range tc.want {
			if page.Items[i].Key != core.MovieKey(id) {
				t.Fatalf("item %d=%v expected %d", i, page.Items[i].Key, id)
			}
		}
	}
	page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie, LocalOnly: true, Playable: "unknown"}, Sort: core.SortPopularity, Desc: true, Limit: 49, NoTotal: true, CursorPage: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 27 {
		t.Fatalf("unknown complement size %d", len(page.Items))
	}
	for _, item := range page.Items {
		if item.Key == core.MovieKey(2) || item.Key == core.MovieKey(9) || item.Key == core.MovieKey(17) {
			t.Fatal("verified title in unknown complement")
		}
	}
}
