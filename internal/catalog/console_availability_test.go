package catalog

import (
	"context"
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

func TestConsoleAvailabilityEvidence(t *testing.T) {
	c := largeCatalogue(t, 3)
	ctx := context.Background()
	policy := "one"
	c.SetConsoleAvailabilityPolicy(func() string { return policy })
	now := c.now().Unix()
	r := core.Release{ID: "good", Info: core.ReleaseInfo{Height: 2160, HDR10: true, Atmos: true}}
	if err := c.RecordConsoleReleaseEvidence(ctx, core.MovieKey(2), r, "feed", policy, now+3600, now+3600, 1); err != nil {
		t.Fatal(err)
	}
	run := func(f core.Filter, want int) {
		t.Helper()
		f.Type = core.TypeMovie
		p, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Limit: 10})
		if err != nil || p.Total != want {
			t.Fatalf("%+v total %d want%d err%v", f, p.Total, want, err)
		}
	}
	run(core.Filter{Playable: "yes", DebridCached: "yes", Qualities: []string{"4k", "hdr", "atmos"}}, 1)
	run(core.Filter{Playable: "unknown"}, 2)
	run(core.Filter{Qualities: []string{"dv"}}, 0)
	// Flags must describe the same source, rather than OR-ing incompatible releases.
	r2 := core.Release{ID: "dv", Info: core.ReleaseInfo{Height: 1080, DolbyVision: true}}
	if err := c.RecordConsoleReleaseEvidence(ctx, core.MovieKey(2), r2, "feed", policy, now+3600, 0, -1); err != nil {
		t.Fatal(err)
	}
	run(core.Filter{Qualities: []string{"4k", "dv"}}, 0)
	policy = "two"
	run(core.Filter{Playable: "yes"}, 0)
	run(core.Filter{Playable: "unknown"}, 3)
	policy = "one"
	c.InvalidateConsoleReleaseEvidence(ctx, "good")
	run(core.Filter{Qualities: []string{"4k"}}, 0)
	c.InvalidateConsoleReleaseEvidence(ctx, "dv")
	run(core.Filter{Playable: "unknown"}, 3)
	// Stale checks never turn into negative assertions.
	if err := c.RecordConsoleReleaseEvidence(ctx, core.MovieKey(1), r, "feed", policy, now-int64(time.Hour.Seconds()), now-1, 1); err != nil {
		t.Fatal(err)
	}
	run(core.Filter{Playable: "unknown", DebridCached: "unknown"}, 3)
	if err := c.PruneConsoleReleaseEvidence(ctx, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := c.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM catalog_release_evidence").Scan(&count); err != nil || count != 0 {
		t.Fatalf("prune count%d err%v", count, err)
	}
	r.InfoHash = "hash-one"
	if err := c.RecordConsoleReleaseEvidence(ctx, core.MovieKey(3), r, "feed", policy, now+3600, now+3600, 1); err != nil {
		t.Fatal(err)
	}
	r.InfoHash = "hash-two"
	if err := c.RecordConsoleReleaseEvidence(ctx, core.MovieKey(3), r, "feed", policy, now+3600, 0, -1); err != nil {
		t.Fatal(err)
	}
	run(core.Filter{DebridCached: "yes"}, 0)
	run(core.Filter{DebridCached: "unknown"}, 3)
}
