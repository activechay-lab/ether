package plexwatch

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type heldIdentityCatalog struct{ fakeCatalog }

func (c *heldIdentityCatalog) StoredTitle(ctx context.Context, key core.RatingKey) (*core.Title, error) {
	return c.Get(ctx, key)
}

func TestHeldNamesakeYearIdentity(t *testing.T) {
	ctx := context.Background()
	modern, old := core.MovieKey(770), core.MovieKey(550)
	s := &Servers{catalog: &heldIdentityCatalog{fakeCatalog{titles: map[core.RatingKey]core.Title{
		modern: {Key: modern, Type: core.TypeMovie, Title: "Runner", Year: 2026},
		old:    {Key: old, Type: core.TypeMovie, Title: "Runner", Year: 1962},
	}}}}
	for _, tc := range []struct {
		year int
		key  core.RatingKey
		want bool
	}{
		{2026, modern, true}, {2025, modern, true}, {1962, modern, false}, {2026, old, false}, {1962, old, true}, {0, modern, false},
	} {
		md := pmsMetadata{Type: "movie", Title: "Runner", Year: flexInt(tc.year)}
		if got := s.heldYearAgrees(ctx, md, tc.key); got != tc.want {
			t.Fatalf("year %d key %v: %v, want %v", tc.year, tc.key, got, tc.want)
		}
	}
}
