package admin

import (
	"github.com/braedonsaunders/ether/internal/core"
	"testing"
)

func TestCatalogCursorBoundToFiltersAndBothSortDirections(t *testing.T) {
	q := core.BrowseQuery{Filter: core.Filter{YearFrom: 1990}, Sort: core.SortYear, Desc: true, SecondarySorts: []core.SortSpec{{Sort: core.SortTitle}}}
	cursor := encodeCatalogCursor(q, core.MovieKey(123))
	if key, err := decodeCatalogCursor(cursor, q); err != nil || key != core.MovieKey(123) {
		t.Fatalf("%v %v", key, err)
	}
	for _, change := range []func(*core.BrowseQuery){func(v *core.BrowseQuery) { v.Filter.YearFrom++ }, func(v *core.BrowseQuery) { v.Desc = false }, func(v *core.BrowseQuery) { v.SecondarySorts = []core.SortSpec{{Sort: core.SortTitle, Desc: true}} }} {
		changed := q
		change(&changed)
		if _, err := decodeCatalogCursor(cursor, changed); err == nil {
			t.Fatal("mismatched cursor accepted")
		}
	}
	for _, raw := range []string{"!", "e30", ""} {
		if _, err := decodeCatalogCursor(raw, q); err == nil {
			t.Fatal("invalid cursor accepted")
		}
	}
}
