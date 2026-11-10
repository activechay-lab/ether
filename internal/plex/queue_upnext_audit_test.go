package plex

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

// Explicit additions have their own block after the playing item; original
// queue entries have newer IDs too, so ID comparisons cannot identify that block.
func TestQueueUpNextBlockSurvivesPersistence(t *testing.T) {
	s := testStores().NewPlayQueues()
	keys := []core.RatingKey{core.MovieKey(1), core.MovieKey(2), core.MovieKey(3)}
	q, err := s.CreateWith(1, "client", keys, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	add := func(key int64, next bool) { s.AddItems(&q, []core.RatingKey{core.MovieKey(key)}, nil, next) }
	add(4, false)
	last := q.LastAdded
	add(5, true)
	if q.LastAdded != last {
		t.Fatal("Play Next moved last-added marker")
	}
	add(6, false)
	want := []core.RatingKey{core.MovieKey(1), core.MovieKey(5), core.MovieKey(4), core.MovieKey(6), core.MovieKey(2), core.MovieKey(3)}
	for i, key := range want {
		if q.Items[i].Key != key {
			t.Fatalf("item %d = %v, want %v", i, q.Items[i].Key, key)
		}
	}
	encoded, err := testStores().EncodeQueue(&q)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := testStores().DecodeQueue(q.ID, q.UserID, 1, time.Now().UnixMilli(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	q = *restored
	q.Selected = 1
	add(7, false)
	if q.Items[4].Key != core.MovieKey(7) || q.LastAdded != q.Items[4].ID {
		t.Fatal("persisted Up Next block lost")
	}
	version := q.Version
	s.AddItems(&q, nil, nil, false)
	if q.Version != version {
		t.Fatal("empty add changed version")
	}
}
