package state

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestLegacyProgressDoesNotCompleteOrClamp(t *testing.T) {
	s, _ := newTestState(t)
	key := core.MovieKey(3)
	if err := s.Progress(ctx, 1, key, 10*time.Minute, time.Hour, "paused"); err != nil {
		t.Fatal(err)
	}
	before, _ := s.History(ctx, 1, time.Time{}, 100)
	if err := s.LegacyProgress(ctx, 1, key, 100*time.Hour); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ctx, 1, key)
	if err != nil {
		t.Fatal(err)
	}
	if got.ViewOffset != 100*time.Hour || got.ViewCount != 0 || got.Duration != time.Hour {
		t.Fatalf("legacy progress: %+v", got)
	}
	after, _ := s.History(ctx, 1, time.Time{}, 100)
	if len(after) != len(before) {
		t.Fatalf("legacy progress added history: %d -> %d", len(before), len(after))
	}
}

func TestPendingLegacyProgressPreservesRawResume(t *testing.T) {
	at := time.Now()
	ws := core.WatchStatus{Key: core.MovieKey(3), Duration: time.Hour}
	w := pendingWrite{kind: "progress", progress: progressWrite{state: "progress", offset: 100 * time.Hour, at: at}}
	got := overlayPending(ws, w, false, time.Time{})
	if got.ViewCount != 0 || got.ViewOffset != 100*time.Hour {
		t.Fatalf("pending legacy: %+v", got)
	}
}
