package resolver

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestChannelAvailabilityUsesOneVerdict(t *testing.T) {
	var checks atomic.Int64
	idx := &fakeIndexer{releases: []core.Release{signalRelease("one", "PRIMARY", "english"), signalRelease("two", "PRIMARY", "english")}}
	cfg := testCfg()
	cfg.Resolver.OptimisticStart = true
	cfg.Resolver.HealthRace = 4
	s := New(idx, fakeHealth{checks: &checks}, nil, cfg, Options{})
	defer s.Close()
	ctx := core.WithBackgroundBudget(context.Background(), func(context.Context, string, string, int64) error { return nil })
	known, ready := s.CheckChannelAvailability(ctx, matrixTitle())
	if !known || !ready {
		t.Fatal("background validation did not establish availability")
	}
	if checks.Load() != 1 {
		t.Fatalf("checked %d releases after first success", checks.Load())
	}
	searches := idx.searches.Load()
	known, ready = s.CheckChannelAvailability(ctx, matrixTitle())
	if !known || !ready || checks.Load() != 1 || idx.searches.Load() != searches {
		t.Fatal("cached verdict repeated discovery or validation")
	}
}
