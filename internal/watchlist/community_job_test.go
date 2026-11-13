package watchlist

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestCommunityJobIsolationAndRecovery(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	s := New(db, Options{Now: func() time.Time { return now }})
	u := &core.User{ID: 1}
	first, fresh, err := s.startCommunityJob(ctx, u)
	if err != nil || !fresh {
		t.Fatalf("start: fresh=%v err=%v", fresh, err)
	}
	same, fresh, err := s.startCommunityJob(ctx, u)
	if err != nil || fresh || same.ID != first.ID {
		t.Fatalf("duplicate: %+v fresh=%v err=%v", same, fresh, err)
	}
	if _, err := s.communityJobForUser(ctx, first.ID, 2); err == nil {
		t.Fatal("other viewer can read job")
	}
	// A new service/node must not interrupt another node's live job.
	other := New(db, Options{Now: func() time.Time { return now }})
	job, err := other.communityJobForUser(ctx, first.ID, 1)
	if err != nil || job.State != CommunityJobQueued {
		t.Fatalf("live job interrupted: %+v %v", job, err)
	}
	now = now.Add(7 * time.Minute)
	job, err = other.communityJobForUser(ctx, first.ID, 1)
	if err != nil || job.State != CommunityJobInterrupted {
		t.Fatalf("stale job not recovered: %+v %v", job, err)
	}
	next, fresh, err := s.startCommunityJob(ctx, u)
	if err != nil || !fresh || next.ID == first.ID {
		t.Fatalf("restart: %+v fresh=%v err=%v", next, fresh, err)
	}
	// A held source lease prevents another node from committing the same source.
	err = s.withCommunityWriterLock(ctx, "friend:1", func() error {
		short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		called := false
		err := other.withCommunityWriterLock(short, "friend:1", func() error { called = true; return nil })
		if err == nil || called {
			t.Fatal("concurrent writer admitted")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := other.withCommunityWriterLock(ctx, "friend:1", func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
