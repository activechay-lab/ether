package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// TestFavoriteEpisodePageNeverWalksTheLibrary proves the Moonfin Favorites
// query for Episode (#405) answers from the viewer's favorites: no series is
// expanded, the listings are the viewer's favorite episodes of the show
// library, and the total is bounded by the favorite set, not the library.
func TestFavoriteEpisodePageNeverWalksTheLibrary(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 7}
	eps := map[core.RatingKey]core.Title{
		core.EpisodeKey(10, 1, 1): {Key: core.EpisodeKey(10, 1, 1), ShowKey: core.ShowKey(10), Type: core.TypeEpisode, Season: 1, Episode: 1, SortTitle: "Show A S01E01"},
		core.EpisodeKey(20, 2, 3): {Key: core.EpisodeKey(20, 2, 3), ShowKey: core.ShowKey(20), Type: core.TypeEpisode, Season: 2, Episode: 3, SortTitle: "Show B S02E03"},
	}
	expanded := 0
	var indexed bool
	svc := &Service{d: ServiceDeps{
		Active:    func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries: []Library{{ID: 2, Type: core.TypeShow}},
		Visibility: Visibility{
			SectionFor: func(core.MediaType) (int, bool) { return 2, true },
			GetTitle: func(_ context.Context, key core.RatingKey) (*core.Title, error) {
				return &core.Title{Key: key, Type: core.TypeShow}, nil
			},
		},
		Browse: func(context.Context, core.BrowseQuery) (core.Page, error) {
			t.Fatal("a favorite listing browsed the library")
			return core.Page{}, nil
		},
		Children:      func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { expanded++; return nil, nil },
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { expanded++; return nil, nil },
		ParentTitles: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			out := map[core.RatingKey]core.Title{}
			for _, k := range keys {
				out[k] = core.Title{Key: k, Type: core.TypeShow}
			}
			return out, nil
		},
		FavoriteEpisodes: FavoriteEpisodeDeps{
			Page: func(_ context.Context, _ core.Filter, userID int64, showKey core.RatingKey, offset, limit int) ([]core.Title, error) {
				indexed = true
				if userID != 7 {
					t.Fatalf("page for user %d", userID)
				}
				if showKey != 0 {
					t.Fatalf("series restriction %v", showKey)
				}
				all := []core.Title{eps[core.EpisodeKey(10, 1, 1)], eps[core.EpisodeKey(20, 2, 3)]}
				if offset >= len(all) {
					return nil, nil
				}
				all = all[offset:]
				if len(all) > limit {
					all = all[:limit]
				}
				return all, nil
			},
			Count: func(context.Context, core.Filter, int64, core.RatingKey) (int, error) { return 2, nil },
		},
	}}
	yes := true
	q := frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortTitle, Limit: 30, Favorite: &yes}
	page, err := svc.LibraryItems(ctx, viewer, core.Device{}, "2", q)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != 0 {
		t.Fatalf("the favorite listing expanded %d series", expanded)
	}
	if !indexed {
		t.Fatal("the favorite listing did not use the indexed favorite path")
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("items %v total %d", keysOf(page.Items), page.Total)
	}
	// SortName orders by the catalogue sort title: Show A before Show B.
	if page.Items[0].Key != core.EpisodeKey(10, 1, 1) || page.Items[1].Key != core.EpisodeKey(20, 2, 3) {
		t.Fatalf("order %v", keysOf(page.Items))
	}
}

// TestFavoriteEpisodePageWithinASeries answers a favorite-episode listing under
// one show (ParentId) from the favorites too, without expanding the series, and
// only lists that show's episodes.
func TestFavoriteEpisodePageWithinASeries(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 7}
	show := core.Title{Key: core.ShowKey(10), Type: core.TypeShow}
	mine := core.Title{Key: core.EpisodeKey(10, 1, 1), ShowKey: show.Key, Type: core.TypeEpisode, Season: 1, Episode: 1}
	other := core.Title{Key: core.EpisodeKey(20, 1, 1), ShowKey: core.ShowKey(20), Type: core.TypeEpisode, Season: 1, Episode: 1}
	expanded := 0
	svc := &Service{d: ServiceDeps{
		Active:    func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries: []Library{{ID: 2, Type: core.TypeShow}},
		Visibility: Visibility{
			SectionFor: func(core.MediaType) (int, bool) { return 2, true },
			GetTitle:   func(context.Context, core.RatingKey) (*core.Title, error) { return &show, nil },
		},
		Get: func(context.Context, *core.User, core.RatingKey) (*core.Title, error) { return &show, nil },
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			expanded++
			return nil, nil
		},
		FavoriteTitleKeys: func(_ context.Context, _ *core.User, _ int) ([]core.RatingKey, bool, error) {
			return []core.RatingKey{mine.Key, other.Key}, true, nil
		},
		CachedGetMany: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			out := map[core.RatingKey]core.Title{}
			for _, k := range keys {
				if k == mine.Key {
					out[k] = mine
				}
				if k == other.Key {
					out[k] = other
				}
			}
			return out, nil
		},
	}}
	yes := true
	q := frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortTitle, Limit: 30, Favorite: &yes}
	page, err := svc.Children(ctx, viewer, core.Device{}, show.Key, q)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != 0 {
		t.Fatalf("the series favorite listing expanded %d times", expanded)
	}
	if len(page.Items) != 1 || page.Items[0].Key != mine.Key {
		t.Fatalf("items %v want only %v", keysOf(page.Items), mine.Key)
	}
}

// TestUnsupportedTypesAnswerEmpty proves a listing whose IncludeItemTypes names
// only kinds no accessible library serves answers empty without touching the
// catalogue (#405: Person/MusicVideo/MusicArtist/MusicAlbum/Audio must not
// become a favorite walk of the whole TV library).
func TestUnsupportedTypesAnswerEmpty(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 7}
	calls := 0
	svc := &Service{d: ServiceDeps{
		Active:     func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries:  []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }},
		Browse:     func(context.Context, core.BrowseQuery) (core.Page, error) { calls++; return core.Page{}, nil },
		Children:   func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { calls++; return nil, nil },
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			calls++
			return nil, nil
		},
		FavoriteTitleKeys: func(context.Context, *core.User, int) ([]core.RatingKey, bool, error) { calls++; return nil, true, nil },
	}}
	yes := true
	for _, kind := range []string{"person", "musicvideo", "musicartist", "musicalbum", "audio"} {
		q := frontend.ItemQuery{Recursive: true, Sort: core.SortTitle, Limit: 30, Favorite: &yes, UnservedTypes: []string{kind}}
		page, err := svc.LibraryItems(ctx, viewer, core.Device{}, "", q)
		if err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if len(page.Items) != 0 || page.Total != 0 {
			t.Fatalf("%s: items %v total %d, want empty", kind, keysOf(page.Items), page.Total)
		}
	}
	if calls != 0 {
		t.Fatalf("an unsupported type touched the catalogue %d times", calls)
	}
}

// TestPluginProvidedKindStillLists proves the empty answer is not a blanket
// refusal: a plugin library that declares the kind keeps it servable, so the
// listing is routed there rather than emptied.
func TestPluginProvidedKindStillLists(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 7}
	pl := &fakePluginLibraries{libs: []core.Library{{
		ID: 9, Source: core.LibraryPlugin, PluginID: "music", KindKey: "album", MediaType: core.TypeShow,
		Present: mediaBrowserPresentation("music", map[string]string{"album": "MusicAlbum", "track": "Audio"}),
	}}}
	svc := &Service{d: ServiceDeps{
		Active:          func(context.Context, int64) (*core.User, error) { return viewer, nil },
		PluginLibraries: pl,
		Protocol:        "jellyfin",
		Visibility:      Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }},
	}}
	q := frontend.ItemQuery{UnservedTypes: []string{"musicalbum"}}
	if !svc.requestedTypesServable(ctx, viewer, q) {
		t.Fatal("a plugin-declared MusicAlbum kind must stay servable")
	}
	q = frontend.ItemQuery{UnservedTypes: []string{"person"}}
	if svc.requestedTypesServable(ctx, viewer, q) {
		t.Fatal("Person is not a kind any configured library serves")
	}
}
