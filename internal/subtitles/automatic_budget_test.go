package subtitles

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestAutomaticBudgetConcurrentRestartAndEpisodeIsolation(t *testing.T) {
	db := store.OpenTest(t)
	ctx := context.Background()
	now := time.Now()
	key := core.EpisodeKey(1, 1, 1)
	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Independent objects represent workers sharing the same cluster database.
			b := NewAutomaticBudget(db)
			a, ok, err := b.Begin(ctx, 1, key, now)
			if err != nil {
				t.Error(err)
				return
			}
			if !ok {
				return
			}
			ok, err = b.Spend(ctx, a, now)
			if err != nil || !ok {
				t.Errorf("spend: %v %v", ok, err)
				return
			}
			winners.Add(1)
			if ok, err := b.Spend(ctx, a, now); err != nil || ok {
				t.Errorf("duplicate permission: %v %v", ok, err)
			}
			// Simulate lost HTTP response/crash: never finish or release this attempt.
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("downloads=%d", winners.Load())
	}
	b := NewAutomaticBudget(db)
	if _, ok, err := b.Begin(ctx, 1, key, now.Add(365*24*time.Hour)); err != nil || ok {
		t.Fatalf("restart/failover refunded: %v %v", ok, err)
	}
	for _, pair := range []struct {
		viewer int64
		key    core.RatingKey
	}{{2, key}, {1, core.EpisodeKey(1, 1, 2)}} {
		if _, ok, err := b.Begin(ctx, pair.viewer, pair.key, now); err != nil || !ok {
			t.Fatalf("scope: %v %v", ok, err)
		}
	}
}

func TestAutomaticBudgetExpiryPauseAndFencing(t *testing.T) {
	db := store.OpenTest(t)
	b := NewAutomaticBudget(db)
	ctx := context.Background()
	now := time.Now()
	key := core.MovieKey(10)
	old, ok, err := b.Begin(ctx, 1, key, now)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err = b.Finish(ctx, old, "no_match"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = b.Begin(ctx, 1, key, now.Add(time.Hour)); err != nil || ok {
		t.Fatal("negative retry", ok, err)
	}
	next, ok, err := b.Begin(ctx, 1, key, now.Add(6*time.Hour))
	if err != nil || !ok {
		t.Fatal("expiry", ok, err)
	}
	if ok, err = b.Spend(ctx, old, now.Add(6*time.Hour)); err != nil || ok {
		t.Fatal("stale spend", ok, err)
	}
	if err = b.Finish(ctx, old, "error"); err != nil {
		t.Fatal(err)
	}
	status, err := b.Status(ctx, 1, key)
	if err != nil || status.Status != "searching" {
		t.Fatal(status, err)
	}
	if err = b.Pause(ctx, now.Add(8*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = b.Pause(ctx, now.Add(7*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, err = b.Spend(ctx, next, now.Add(7*time.Hour)); err != nil || ok {
		t.Fatal("pause shortened", ok, err)
	}
	if _, ok, err = b.Begin(ctx, 2, key, now.Add(7*time.Hour)); err != nil || ok {
		t.Fatal("other viewer pause", ok, err)
	}
	if ok, err = b.Spend(ctx, next, now.Add(8*time.Hour)); err != nil || !ok {
		t.Fatal("pause expiry", ok, err)
	}
	if err = b.Finish(ctx, next, "error"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err = b.Begin(ctx, 1, key, now.Add(24*time.Hour)); err != nil || ok {
		t.Fatal("error refunded", ok, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, ok, err = b.Begin(cancelled, 3, key, now); err == nil || ok {
		t.Fatal("cancelled admission", ok, err)
	}
	if _, ok, err = NewAutomaticBudget(nil).Begin(ctx, 1, key, now); err == nil || ok {
		t.Fatal("missing store", ok, err)
	}
	if _, ok, err = b.Begin(ctx, 1, core.ShowKey(10), now); err == nil || ok {
		t.Fatal("series admitted", ok, err)
	}
}

func TestAutomaticBudgetMergePreservesSpentAndFencesSearch(t *testing.T) {
	db := store.OpenTest(t)
	b := NewAutomaticBudget(db)
	ctx := context.Background()
	now := time.Now()
	key := core.MovieKey(1)
	a, _, _ := b.Begin(ctx, 1, key, now)
	if ok, err := b.Spend(ctx, a, now); err != nil || !ok {
		t.Fatal(ok, err)
	}
	other, _, _ := b.Begin(ctx, 2, key, now)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = mergeAutomaticBudget(ctx, tx, 1, 2); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Spend(ctx, other, now); err != nil || ok {
		t.Fatal("merge lost spent bit", ok, err)
	}
	if _, ok, err := b.Begin(ctx, 2, key, now.Add(24*time.Hour)); err != nil || ok {
		t.Fatal("merge refunded", ok, err)
	}
}
