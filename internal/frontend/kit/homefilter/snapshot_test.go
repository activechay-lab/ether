package homefilter

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestAllowedMemoizesWithinSnapshot(t *testing.T) {
	calls := 0
	filter := func(_ context.Context, titles []core.Title) []core.Title {
		calls++
		var out []core.Title
		for _, ti := range titles {
			if ti.Key == 10 {
				out = append(out, ti)
			}
		}
		return out
	}
	ctx := With(context.Background(), &Snapshot{})
	items := []core.Title{{Key: 10}, {Key: 20}, {Key: 10}}
	got := Allowed(ctx, items, filter)
	if !got[10] || got[20] || calls != 1 {
		t.Fatalf("first Allowed = %v, calls = %d", got, calls)
	}
	got2 := Allowed(ctx, items, filter)
	if !got2[10] || got2[20] || calls != 1 {
		t.Fatalf("second Allowed = %v, calls = %d", got2, calls)
	}
}
