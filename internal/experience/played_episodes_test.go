package experience

import (
	"context"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// TestPlayedEpisodePageNeverWalksTheLibrary answers the Moonfin played-episode
// query from the viewer's watch rows: no series is ever expanded, the order
// follows last-played, a restricted series hides its episodes, and the count
// comes from the played set.
func TestPlayedEpisodePageNeverWalksTheLibrary(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 7}
	shows := map[core.RatingKey]core.Title{
		core.ShowKey(10): {Key: core.ShowKey(10), Type: core.TypeShow},
		core.ShowKey(20): {Key: core.ShowKey(20), Type: core.TypeShow},
	}
	eps := map[core.RatingKey]core.Title{
		core.EpisodeKey(10, 1, 1): {Key: core.EpisodeKey(10, 1, 1), ShowKey: core.ShowKey(10), Type: core.TypeEpisode, Season: 1, Episode: 1},
		core.EpisodeKey(10, 1, 2): {Key: core.EpisodeKey(10, 1, 2), ShowKey: core.ShowKey(10), Type: core.TypeEpisode, Season: 1, Episode: 2},
		core.EpisodeKey(20, 1, 1): {Key: core.EpisodeKey(20, 1, 1), ShowKey: core.ShowKey(20), Type: core.TypeEpisode, Season: 1, Episode: 1, Adult: true},
	}
	expanded := 0
	var counted int
	svc := &Service{d: ServiceDeps{
		Active:     func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries:  []Library{{ID: 2, Type: core.TypeShow}},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }},
		Browse: func(context.Context, core.BrowseQuery) (core.Page, error) {
			t.Fatal("a played listing browsed the library")
			return core.Page{}, nil
		},
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			expanded++
			return nil, nil
		},
		Children: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			expanded++
			return nil, nil
		},
		ParentTitles: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			out := map[core.RatingKey]core.Title{}
			for _, k := range keys {
				out[k] = shows[k]
			}
			return out, nil
		},
		PlayedEpisodes: PlayedEpisodeDeps{
			// Most recent first: show20e1 (adult, must be dropped), show10e2, show10e1.
			Page: func(_ context.Context, _ core.Filter, userID int64, showKey core.RatingKey, desc bool, offset, limit int) ([]core.Title, error) {
				if userID != 7 {
					t.Fatalf("page for user %d", userID)
				}
				all := []core.Title{eps[core.EpisodeKey(20, 1, 1)], eps[core.EpisodeKey(10, 1, 2)], eps[core.EpisodeKey(10, 1, 1)]}
				if showKey != 0 {
					var one []core.Title
					for _, e := range all {
						if e.ShowKey == showKey {
							one = append(one, e)
						}
					}
					all = one
				}
				if !desc {
					all = []core.Title{all[len(all)-1], all[1], all[0]}
				}
				if offset > len(all) {
					return nil, nil
				}
				all = all[offset:]
				if len(all) > limit {
					all = all[:limit]
				}
				return all, nil
			},
			Count: func(_ context.Context, _ core.Filter, userID int64, showKey core.RatingKey) (int, error) {
				counted++
				if showKey != 0 {
					return 2, nil
				}
				return 3, nil
			},
		},
	}}
	q := frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortPlayed, Desc: true, Limit: 100, Watched: boolPtr(true)}
	page, err := svc.LibraryItems(ctx, viewer, core.Device{}, "2", q)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != 0 {
		t.Fatalf("the played listing expanded %d series", expanded)
	}
	if counted == 0 {
		t.Fatal("the played count was not read from the played set")
	}
	want := []core.RatingKey{core.EpisodeKey(10, 1, 2), core.EpisodeKey(10, 1, 1)}
	if len(page.Items) != len(want) {
		t.Fatalf("items %v", keysOf(page.Items))
	}
	for i := range want {
		if page.Items[i].Key != want[i] {
			t.Fatalf("order %v: %v want %v", i, keysOf(page.Items), want)
		}
	}
	if page.Total != 3 {
		t.Fatalf("total %d want 3", page.Total)
	}
	// Ascending follows the client's DatePlayed direction.
	q.Desc = false
	page, err = svc.LibraryItems(ctx, viewer, core.Device{}, "2", q)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Key != core.EpisodeKey(10, 1, 1) || page.Items[1].Key != core.EpisodeKey(10, 1, 2) {
		t.Fatalf("ascending: %v", keysOf(page.Items))
	}
	if expanded != 0 {
		t.Fatalf("the ascending listing expanded %d series", expanded)
	}
	// The Moonfin request is root-level (no ParentId): the cross-library
	// window routes the episode types to the show library and still answers
	// from the played set, ordered by last-played.
	q.Desc = true
	root, err := svc.LibraryItems(ctx, viewer, core.Device{}, "", q)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != 0 {
		t.Fatalf("the root listing expanded %d series", expanded)
	}
	if len(root.Items) != 2 || root.Items[0].Key != core.EpisodeKey(10, 1, 2) || root.Items[1].Key != core.EpisodeKey(10, 1, 1) {
		t.Fatalf("root: %v", keysOf(root.Items))
	}
}

// TestRootSortedWindowNeverExpandsShows proves the root CommunityRating /
// PremiereDate rows (no ParentId, Recursive, Movie+Series) answer from the
// indexed catalogue page: the show library is never expanded into seasons.
func TestRootSortedWindowNeverExpandsShows(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 7}
	shows := []core.Title{{Key: core.ShowKey(10), Type: core.TypeShow}, {Key: core.ShowKey(20), Type: core.TypeShow}}
	expanded := 0
	var browsed []core.BrowseQuery
	svc := &Service{d: ServiceDeps{
		Active:     func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries:  []Library{{ID: 1, Type: core.TypeMovie}, {ID: 2, Type: core.TypeShow}},
		State:      &countingBrowseState{},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }},
		Browse: func(_ context.Context, bq core.BrowseQuery) (core.Page, error) {
			browsed = append(browsed, bq)
			if bq.Filter.Type == core.TypeShow {
				return core.Page{Items: shows, Total: len(shows)}, nil
			}
			return core.Page{Total: 0}, nil
		},
		Children:      func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { expanded++; return nil, nil },
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { expanded++; return nil, nil },
		ParentTitles: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			return map[core.RatingKey]core.Title{}, nil
		},
	}}
	for _, sort := range []core.SortOrder{core.SortRating, core.SortReleased} {
		browsed = nil
		q := frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeMovie, core.TypeShow}, Sort: sort, Desc: true, Limit: 60}
		page, err := svc.LibraryItems(ctx, viewer, core.Device{}, "", q)
		if err != nil {
			t.Fatal(err)
		}
		if expanded != 0 {
			t.Fatalf("%s: the root window expanded %d shows", sort, expanded)
		}
		if len(page.Items) != 2 {
			t.Fatalf("%s: items %v", sort, keysOf(page.Items))
		}
		var typed bool
		for _, bq := range browsed {
			if bq.Filter.Type == core.TypeShow {
				typed = true
			}
		}
		if !typed {
			t.Fatalf("%s: the show library was never browsed in SQL", sort)
		}
	}
}

// TestPlayedEpisodePageWithinASeries answers a played listing under one show
// (ParentId) from the played set too, without expanding the series.
func TestPlayedEpisodePageWithinASeries(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 7}
	show := core.Title{Key: core.ShowKey(10), Type: core.TypeShow}
	ep := core.Title{Key: core.EpisodeKey(10, 1, 2), ShowKey: show.Key, Type: core.TypeEpisode, Season: 1, Episode: 2}
	expanded := 0
	svc := &Service{d: ServiceDeps{
		Active:     func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries:  []Library{{ID: 2, Type: core.TypeShow}},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }},
		Get:        func(context.Context, *core.User, core.RatingKey) (*core.Title, error) { return &show, nil },
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			expanded++
			return []core.Title{ep}, nil
		},
		ParentTitles: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			return map[core.RatingKey]core.Title{show.Key: show}, nil
		},
		PlayedEpisodes: PlayedEpisodeDeps{
			Page: func(_ context.Context, _ core.Filter, _ int64, showKey core.RatingKey, _ bool, _, _ int) ([]core.Title, error) {
				if showKey != show.Key {
					t.Fatalf("series restriction %v want %v", showKey, show.Key)
				}
				return []core.Title{ep}, nil
			},
			Count: func(context.Context, core.Filter, int64, core.RatingKey) (int, error) { return 1, nil },
		},
	}}
	q := frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortPlayed, Desc: true, Limit: 100, Watched: boolPtr(true)}
	page, err := svc.Children(ctx, viewer, core.Device{}, show.Key, q)
	if err != nil {
		t.Fatal(err)
	}
	if expanded != 0 {
		t.Fatalf("the series played listing expanded %d times", expanded)
	}
	if len(page.Items) != 1 || page.Items[0].Key != ep.Key {
		t.Fatalf("items %v", keysOf(page.Items))
	}
}

// TestPlayedEpisodePageNotFoundForOtherSorts keeps the fast path only where it
// is correct: an explicit metadata sort keeps today's path.
func TestPlayedEpisodePageNotFoundForOtherSorts(t *testing.T) {
	ctx := context.Background()
	viewer := &core.User{ID: 7}
	show := core.Title{Key: core.ShowKey(10), Type: core.TypeShow}
	ep := core.Title{Key: core.EpisodeKey(10, 1, 1), ShowKey: show.Key, Type: core.TypeEpisode, Season: 1, Episode: 1}
	expanded := 0
	svc := &Service{d: ServiceDeps{
		Active:     func(context.Context, int64) (*core.User, error) { return viewer, nil },
		Libraries:  []Library{{ID: 2, Type: core.TypeShow}},
		State:      &countingBrowseState{},
		Visibility: Visibility{SectionFor: func(core.MediaType) (int, bool) { return 2, true }, GetTitle: func(context.Context, core.RatingKey) (*core.Title, error) { return &show, nil }},
		Browse: func(context.Context, core.BrowseQuery) (core.Page, error) {
			return core.Page{Items: []core.Title{show}, Total: 1}, nil
		},
		Grandchildren: func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) {
			expanded++
			return []core.Title{ep}, nil
		},
		ParentTitles: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			return map[core.RatingKey]core.Title{show.Key: show}, nil
		},
		PlayedEpisodes: PlayedEpisodeDeps{
			Page: func(context.Context, core.Filter, int64, core.RatingKey, bool, int, int) ([]core.Title, error) {
				return []core.Title{ep}, nil
			},
			Count: func(context.Context, core.Filter, int64, core.RatingKey) (int, error) { return 1, nil },
		},
	}}
	q := frontend.ItemQuery{Recursive: true, Types: []core.MediaType{core.TypeEpisode}, Sort: core.SortTitle, Limit: 100, Watched: boolPtr(true)}
	if _, err := svc.LibraryItems(ctx, viewer, core.Device{}, "2", q); err != nil {
		t.Fatal(err)
	}
	if expanded == 0 {
		t.Fatal("a metadata sort should have kept the walk")
	}
}
