package watchlist

import (
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestAvailabilityOrderNeverCheckedFirst(t *testing.T) {
	items := []Item{
		{Title: core.Title{Key: 1}, CheckedAt: time.Unix(200, 0)},
		{Title: core.Title{Key: 2}},
		{Title: core.Title{Key: 3}, CheckedAt: time.Unix(100, 0)},
		{Title: core.Title{Key: 4}},
	}
	orderChecks(items)
	for i, want := range []core.RatingKey{2, 4, 3, 1} {
		if items[i].Title.Key != want {
			t.Fatalf("position %d: got %v, want %v", i, items[i].Title.Key, want)
		}
	}
}
