package experience

import (
	"context"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// TestCollectionLibraryPagesInSQL is the slice acceptance for issue #436:
// a collection-source library pages the collection's titles through one
// indexed browse whose filter carries the SQL semi-join id, applies viewer
// exclusions, and an empty (deleted) collection yields an empty page.
func TestCollectionLibraryPagesInSQL(t *testing.T) {
	viewer := &core.User{ID: 1, IsOwner: true}
	const n = 3000
	calls := 0
	var first core.BrowseQuery
	firstSeen := false
	s := &Service{d: ServiceDeps{
		Active:          func(context.Context, int64) (*core.User, error) { return viewer, nil },
		PluginLibraries: fakeCollectionLibraries{{ID: 9, Name: "MDBList picks", Source: core.LibraryCollection, MediaType: core.TypeMovie, CollectionID: 42}},
		Visibility: Visibility{
			SectionFor:      func(core.MediaType) (int, bool) { return 1, true },
			FilterAvailable: func(_ context.Context, titles []core.Title) []core.Title { return titles },
		},
		PageExclusions: func(context.Context, *core.User, core.MediaType) LibraryPageExclusions {
			return LibraryPageExclusions{Keys: []core.RatingKey{core.MovieKey(2)}, AvailabilityInSQL: true}
		},
		Browse: func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
			if !firstSeen {
				first, firstSeen = q, true
			}
			calls++
			if q.Filter.CollectionMembers != 42 {
				return core.Page{Items: []core.Title{}, Total: 0}, nil
			}
			hidden := map[core.RatingKey]bool{}
			for _, key := range q.Filter.ExcludeKeys {
				hidden[key] = true
			}
			var items []core.Title
			for i := 1; i <= n; i++ {
				key := core.MovieKey(int64(i))
				if hidden[key] {
					continue
				}
				items = append(items, core.Title{Key: key, Type: core.TypeMovie, Title: "Movie", Year: 2001, Released: time.Unix(1, 0), HomeReleased: time.Unix(2, 0)})
			}
			total := len(items)
			start := min(max(q.Offset, 0), total)
			items = items[start:]
			if q.Limit > 0 && len(items) > q.Limit {
				items = items[:q.Limit]
			}
			return core.Page{Items: items, Total: total}, nil
		},
	}}
	page, err := s.LibraryItems(context.Background(), viewer, core.Device{}, "9", frontend.ItemQuery{Offset: 0, Limit: 5, Sort: core.SortUpdated})
	if err != nil {
		t.Fatal(err)
	}
	if first.Offset != 0 || first.Limit != 5 || first.Sort != core.SortUpdated || first.Filter.Type != core.TypeMovie || first.Filter.CollectionMembers != 42 || len(first.Filter.ExcludeKeys) != 1 || first.Filter.ExcludeKeys[0] != core.MovieKey(2) {
		t.Fatalf("indexed browse query=%+v", first)
	}
	if calls != 1 {
		t.Fatalf("indexed page must read one browse, got %d", calls)
	}
	if page.Total != n-1 || len(page.Items) != 5 || page.Items[0].Key != core.MovieKey(1) {
		t.Fatalf("page total=%d first=%v len=%d", page.Total, page.Items, len(page.Items))
	}
	// An empty (deleted) collection yields an empty page, never an error: the
	// semi-join matches no members.
	s.d.PluginLibraries = fakeCollectionLibraries{{ID: 9, Name: "Gone", Source: core.LibraryCollection, MediaType: core.TypeMovie, CollectionID: 77}}
	page, err = s.LibraryItems(context.Background(), viewer, core.Device{}, "9", frontend.ItemQuery{Limit: 5})
	if err != nil || len(page.Items) != 0 || page.Total != 0 {
		t.Fatalf("deleted collection page=%+v err=%v", page, err)
	}
	// A collection id that names nothing at all is also empty, never a 500.
	s.d.PluginLibraries = fakeCollectionLibraries{{ID: 9, Name: "Gone", Source: core.LibraryCollection, MediaType: core.TypeMovie, CollectionID: 0}}
	page, err = s.LibraryItems(context.Background(), viewer, core.Device{}, "9", frontend.ItemQuery{Limit: 5})
	if err != nil || len(page.Items) != 0 || page.Total != 0 {
		t.Fatalf("deleted collection page=%+v err=%v", page, err)
	}
}

// fakeCollectionLibraries is a PluginLibraries whose only library is a
// stored collection library, so LibraryItems takes the stored path.
type fakeCollectionLibraries []core.Library

func (f fakeCollectionLibraries) Libraries(context.Context) ([]core.Library, error) {
	return f, nil
}

func (f fakeCollectionLibraries) Library(_ context.Context, id core.LibraryID) (core.Library, error) {
	for _, lib := range f {
		if lib.ID == id {
			return lib, nil
		}
	}
	return core.Library{}, core.ErrNotFound
}

func (f fakeCollectionLibraries) IsPluginLibraryActive(context.Context, core.Library) bool {
	return false
}
func (f fakeCollectionLibraries) RootMediaType(context.Context, core.Library) core.MediaType {
	return core.TypeMovie
}
func (f fakeCollectionLibraries) Count(context.Context, core.Library) (int, error) { return 0, nil }
func (f fakeCollectionLibraries) Browse(context.Context, core.Library, frontend.ItemQuery, string, int) (PluginBrowsePage, error) {
	return PluginBrowsePage{}, core.ErrNotFound
}
func (f fakeCollectionLibraries) Item(context.Context, core.RatingKey) (PluginItemView, error) {
	return PluginItemView{}, core.ErrNotFound
}
func (f fakeCollectionLibraries) ItemsMany(context.Context, []core.RatingKey) (map[core.RatingKey]PluginItemView, error) {
	return nil, nil
}
func (f fakeCollectionLibraries) Children(context.Context, core.RatingKey, bool, string, int) (PluginBrowsePage, error) {
	return PluginBrowsePage{}, core.ErrNotFound
}
func (f fakeCollectionLibraries) Ancestors(context.Context, core.RatingKey) ([]core.Title, core.Library, error) {
	return nil, core.Library{}, core.ErrNotFound
}
func (f fakeCollectionLibraries) Facets(context.Context, core.Library, string) ([]core.FacetValue, error) {
	return nil, nil
}
func (f fakeCollectionLibraries) Search(context.Context, core.Library, string, frontend.ItemQuery, int) ([]core.Title, error) {
	return nil, nil
}
