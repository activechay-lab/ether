package experience

import (
	"context"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// fakePluginLibraries is a minimal PluginLibraries for the served-kinds tests.
type fakePluginLibraries struct {
	libs []core.Library
}

func (f *fakePluginLibraries) Libraries(context.Context) ([]core.Library, error) { return f.libs, nil }
func (f *fakePluginLibraries) Library(_ context.Context, id core.LibraryID) (core.Library, error) {
	for _, l := range f.libs {
		if l.ID == id {
			return l, nil
		}
	}
	return core.Library{}, core.ErrNotFound
}
func (f *fakePluginLibraries) IsPluginLibraryActive(context.Context, core.Library) bool { return true }
func (f *fakePluginLibraries) RootMediaType(context.Context, core.Library) core.MediaType {
	return core.TypeShow
}
func (f *fakePluginLibraries) Count(context.Context, core.Library) (int, error) { return 0, nil }
func (f *fakePluginLibraries) Browse(context.Context, core.Library, frontend.ItemQuery, string, int) (PluginBrowsePage, error) {
	return PluginBrowsePage{}, nil
}
func (f *fakePluginLibraries) Item(context.Context, core.RatingKey) (PluginItemView, error) {
	return PluginItemView{}, core.ErrNotFound
}
func (f *fakePluginLibraries) ItemsMany(context.Context, []core.RatingKey) (map[core.RatingKey]PluginItemView, error) {
	return nil, nil
}
func (f *fakePluginLibraries) Children(context.Context, core.RatingKey, bool, string, int) (PluginBrowsePage, error) {
	return PluginBrowsePage{}, nil
}
func (f *fakePluginLibraries) Ancestors(context.Context, core.RatingKey) ([]core.Title, core.Library, error) {
	return nil, core.Library{}, core.ErrNotFound
}
func (f *fakePluginLibraries) Facets(context.Context, core.Library, string) ([]core.FacetValue, error) {
	return nil, nil
}
func (f *fakePluginLibraries) Search(context.Context, core.Library, string, frontend.ItemQuery, int) ([]core.Title, error) {
	return nil, nil
}

// mediaBrowserPresentation builds a Presentations with the given MediaBrowser
// collection type and level->Jellyfin item-kind map.
func mediaBrowserPresentation(collection string, itemTypes map[string]string) core.Presentations {
	p := core.Presentations{}
	p.MediaBrowser = &struct {
		CollectionType string            `json:"collectionType"`
		ItemTypes      map[string]string `json:"itemTypes,omitempty"`
	}{CollectionType: collection, ItemTypes: itemTypes}
	return p
}

// MemberOf reports no collection membership: these tests use no
// collection-source libraries.
func (f *fakePluginLibraries) MemberOf(context.Context, core.Library, core.RatingKey) (bool, error) {
	return false, nil
}
