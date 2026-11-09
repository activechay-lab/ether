package sessions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestSubtitleIntentFencesManualOffAndPublication(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	s := NewSubtitleIntents(db)
	key := core.MovieKey(1)
	before, err := s.Snapshot(ctx, 1, key)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Record(ctx, 1, key, true); err != nil {
		t.Fatal(err)
	}
	called := false
	if err = s.Publish(ctx, 1, key, before, func() error { called = true; return nil }); !errors.Is(err, core.ErrSourceChanged) || called {
		t.Fatal("manual off overwritten", called, err)
	}
	if err = s.Record(ctx, 1, key, false); err != nil {
		t.Fatal(err)
	}
	reset, err := s.Snapshot(ctx, 1, key)
	if err != nil || reset.Manual || reset.Revision <= before.Revision {
		t.Fatal(reset, err)
	}
	if err = s.Publish(ctx, 1, key, before, func() error { t.Fatal("reset revived stale work"); return nil }); !errors.Is(err, core.ErrSourceChanged) {
		t.Fatal(err)
	}
	// A separate node's manual writer cannot pass the final publication fence.
	done := make(chan error, 1)
	err = s.Publish(ctx, 1, key, reset, func() error {
		go func() { done <- NewSubtitleIntents(db).Record(ctx, 1, key, true) }()
		select {
		case err := <-done:
			t.Fatalf("writer passed fence: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manual writer not released")
	}
	after, err := s.Snapshot(ctx, 1, key)
	if err != nil || !after.Manual {
		t.Fatal(after, err)
	}
	if err = s.Publish(ctx, 1, key, after, func() error { t.Fatal("manual choice published"); return nil }); err == nil {
		t.Fatal("manual snapshot accepted")
	}
}
