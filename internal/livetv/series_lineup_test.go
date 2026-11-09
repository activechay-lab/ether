package livetv

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

// A lineup continues every series where it left off, across days and across
// rotations off air, and never repeats an episode the series already aired.
func TestLineupContinuesEachSeries(t *testing.T) {
	var pool []ProgramSource
	for show := int64(1); show <= 6; show++ {
		var eps []Item
		for e := 1; e <= 30; e++ {
			eps = append(eps, Item{Key: core.EpisodeKey(show, 1, e), ShowKey: core.ShowKey(show), Season: 1, Episode: e, Title: "s", DurationMS: int64(22 * time.Minute / time.Millisecond)})
		}
		pool = append(pool, ProgramSource{ID: "child", Kind: "show", ShowKey: core.ShowKey(show), Items: eps})
	}
	src := ProgramSource{ID: "l", Kind: "lineup", Shows: 3, RotateDays: 2, StartDate: "2026-11-02", Sources: pool}
	c := Channel{Epoch: time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC), Seed: 1}
	b := Block{Start: 17 * 60, End: 19 * 60}
	next := map[core.RatingKey]int{}
	for d := 0; d < 12; d++ {
		day := time.Date(2026, 11, 2+d, 0, 0, 0, 0, time.UTC)
		items := lineupDay(src, c, day, b, 1)
		// The block airs 120 minutes of 22-minute episodes: six per day.
		seen := map[core.RatingKey]bool{}
		for _, it := range items[:6] {
			seen[it.ShowKey] = true
			if want, ok := next[it.ShowKey]; ok && it.Episode != want {
				t.Fatalf("day %d: %v aired episode %d, want %d", d, it.ShowKey, it.Episode, want)
			}
			next[it.ShowKey] = it.Episode%30 + 1
		}
		if len(seen) != 3 {
			t.Fatalf("day %d: %d series on air, want 3", d, len(seen))
		}
	}
	if len(next) != 6 {
		t.Fatalf("rotation reached %d of 6 series", len(next))
	}
}
