package plex

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
	"time"
)

// Explicit additions have their own block after the playing item; original
// queue entries have newer IDs too, so ID comparisons cannot identify that block.
func TestQueueUpNextBlockSurvivesPersistence(t *testing.T) {
	s := newPlayQueueStore()
	keys := []core.RatingKey{core.MovieKey(1), core.MovieKey(2), core.MovieKey(3)}
	q, err := s.createWith(1, "client", keys, time.Now(), nil)
	if err != nil {
		t.Fatal(err)
	}
	add := func(key int64, next bool) { s.addItems(&q, []core.RatingKey{core.MovieKey(key)}, nil, next) }
	add(4, false)
	last := q.lastAdded
	add(5, true)
	if q.lastAdded != last {
		t.Fatal("Play Next moved last-added marker")
	}
	add(6, false)
	want := []core.RatingKey{core.MovieKey(1), core.MovieKey(5), core.MovieKey(4), core.MovieKey(6), core.MovieKey(2), core.MovieKey(3)}
	for i, key := range want {
		if q.items[i].key != key {
			t.Fatalf("item %d = %v, want %v", i, q.items[i].key, key)
		}
	}
	encoded, err := encodeQueue(&q)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := decodeQueue(q.id, q.userID, 1, time.Now().UnixMilli(), encoded)
	if err != nil {
		t.Fatal(err)
	}
	q = *restored
	q.selected = 1
	add(7, false)
	if q.items[4].key != core.MovieKey(7) || q.lastAdded != q.items[4].id {
		t.Fatal("persisted Up Next block lost")
	}
	version := q.version
	s.addItems(&q, nil, nil, false)
	if q.version != version {
		t.Fatal("empty add changed version")
	}
}
