package experience

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type stepAsideTestState struct {
	serviceState
	inProgress map[int64][]core.WatchStatus
	statuses   map[int64]map[core.RatingKey]core.WatchStatus
	history    map[int64][]core.HistoryEntry
}

func (s *stepAsideTestState) InProgress(_ context.Context, userID int64, _ int) ([]core.WatchStatus, error) {
	return append([]core.WatchStatus(nil), s.inProgress[userID]...), nil
}

func (s *stepAsideTestState) Get(_ context.Context, userID int64, key core.RatingKey) (core.WatchStatus, error) {
	if st, ok := s.statuses[userID][key]; ok {
		return st, nil
	}
	return core.WatchStatus{Key: key}, nil
}

func (s *stepAsideTestState) GetMany(_ context.Context, userID int64, keys []core.RatingKey) (map[core.RatingKey]core.WatchStatus, error) {
	out := make(map[core.RatingKey]core.WatchStatus, len(keys))
	for _, k := range keys {
		if st, ok := s.statuses[userID][k]; ok {
			out[k] = st
		} else {
			out[k] = core.WatchStatus{Key: k}
		}
	}
	return out, nil
}

func (s *stepAsideTestState) History(_ context.Context, userID int64, _ time.Time, _ int) ([]core.HistoryEntry, error) {
	return append([]core.HistoryEntry(nil), s.history[userID]...), nil
}

type stepAsideFixture struct {
	now          time.Time
	heldMovie    core.Title
	freeMovie    core.Title
	fullShow     core.Title
	fullS1       core.Title
	fullS1E1     core.Title
	fullS1E2     core.Title
	partialShow  core.Title
	partS1       core.Title
	partS1E1     core.Title
	partS1E2     core.Title
	partS2       core.Title
	partS2E1Held core.Title
	partS2E2Free core.Title

	byKey    map[core.RatingKey]core.Title
	movies   []core.Title
	shows    []core.Title
	children map[core.RatingKey][]core.Title
	aired    map[core.RatingKey][]core.RatingKey
	held     StepAsideHeld
	state    *stepAsideTestState
	sessions []*core.PlaySession
}

func newStepAsideFixture() *stepAsideFixture {
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)
	past := now.Add(-48 * time.Hour)

	heldMovie := core.Title{Key: core.MovieKey(10), Type: core.TypeMovie, Title: "Held Movie", Released: past, AddedAt: past}
	freeMovie := core.Title{Key: core.MovieKey(11), Type: core.TypeMovie, Title: "Ether Only Movie", Released: past, AddedAt: past}

	fullShow := core.Title{Key: core.ShowKey(20), Type: core.TypeShow, Title: "Complete Show", Released: past, AddedAt: past}
	fullS1 := core.Title{Key: core.SeasonKey(20, 1), ShowKey: fullShow.Key, Type: core.TypeSeason, Title: "Season 1", Season: 1, Released: past}
	fullS1E1 := core.Title{Key: core.EpisodeKey(20, 1, 1), ShowKey: fullShow.Key, Type: core.TypeEpisode, Title: "Full Ep 1", Season: 1, Episode: 1, Released: past}
	fullS1E2 := core.Title{Key: core.EpisodeKey(20, 1, 2), ShowKey: fullShow.Key, Type: core.TypeEpisode, Title: "Full Ep 2", Season: 1, Episode: 2, Released: past}

	partialShow := core.Title{Key: core.ShowKey(30), Type: core.TypeShow, Title: "Partial Show", Released: past, AddedAt: past}
	partS1 := core.Title{Key: core.SeasonKey(30, 1), ShowKey: partialShow.Key, Type: core.TypeSeason, Title: "Season 1", Season: 1, Released: past}
	partS1E1 := core.Title{Key: core.EpisodeKey(30, 1, 1), ShowKey: partialShow.Key, Type: core.TypeEpisode, Title: "Part S1E1", Season: 1, Episode: 1, Released: past}
	partS1E2 := core.Title{Key: core.EpisodeKey(30, 1, 2), ShowKey: partialShow.Key, Type: core.TypeEpisode, Title: "Part S1E2", Season: 1, Episode: 2, Released: past}
	partS2 := core.Title{Key: core.SeasonKey(30, 2), ShowKey: partialShow.Key, Type: core.TypeSeason, Title: "Season 2", Season: 2, Released: past}
	partS2E1Held := core.Title{Key: core.EpisodeKey(30, 2, 1), ShowKey: partialShow.Key, Type: core.TypeEpisode, Title: "Part S2E1 Held", Season: 2, Episode: 1, Released: past}
	partS2E2Free := core.Title{Key: core.EpisodeKey(30, 2, 2), ShowKey: partialShow.Key, Type: core.TypeEpisode, Title: "Part S2E2 Missing", Season: 2, Episode: 2, Released: past}

	all := []core.Title{
		heldMovie, freeMovie,
		fullShow, fullS1, fullS1E1, fullS1E2,
		partialShow, partS1, partS1E1, partS1E2, partS2, partS2E1Held, partS2E2Free,
	}
	byKey := make(map[core.RatingKey]core.Title, len(all))
	for _, t := range all {
		byKey[t.Key] = t
	}

	return &stepAsideFixture{
		now:          now,
		heldMovie:    heldMovie,
		freeMovie:    freeMovie,
		fullShow:     fullShow,
		fullS1:       fullS1,
		fullS1E1:     fullS1E1,
		fullS1E2:     fullS1E2,
		partialShow:  partialShow,
		partS1:       partS1,
		partS1E1:     partS1E1,
		partS1E2:     partS1E2,
		partS2:       partS2,
		partS2E1Held: partS2E1Held,
		partS2E2Free: partS2E2Free,
		byKey:        byKey,
		movies:       []core.Title{heldMovie, freeMovie},
		shows:        []core.Title{fullShow, partialShow},
		children: map[core.RatingKey][]core.Title{
			fullShow.Key:    {fullS1},
			fullS1.Key:      {fullS1E1, fullS1E2},
			partialShow.Key: {partS1, partS2},
			partS1.Key:      {partS1E1, partS1E2},
			partS2.Key:      {partS2E1Held, partS2E2Free},
		},
		aired: map[core.RatingKey][]core.RatingKey{
			fullShow.Key:    {fullS1E1.Key, fullS1E2.Key},
			partialShow.Key: {partS1E1.Key, partS1E2.Key, partS2E1Held.Key, partS2E2Free.Key},
		},
		held: StepAsideHeld{
			Rev: 1,
			Movies: map[core.RatingKey]struct{}{
				heldMovie.Key: {},
			},
			Episodes: map[core.RatingKey]struct{}{
				fullS1E1.Key:     {},
				fullS1E2.Key:     {},
				partS1E1.Key:     {},
				partS1E2.Key:     {},
				partS2E1Held.Key: {},
			},
			Shows: []core.RatingKey{fullShow.Key, partialShow.Key},
		},
		state: &stepAsideTestState{
			inProgress: map[int64][]core.WatchStatus{},
			statuses:   map[int64]map[core.RatingKey]core.WatchStatus{},
			history:    map[int64][]core.HistoryEntry{},
		},
	}
}

func (f *stepAsideFixture) buildStepAside() *StepAside {
	return NewStepAside(StepAsideOptions{
		Held: func() StepAsideHeld { return f.held },
		AiredEpisodes: func(_ context.Context, shows []core.RatingKey) (map[core.RatingKey][]core.RatingKey, error) {
			out := make(map[core.RatingKey][]core.RatingKey, len(shows))
			for _, k := range shows {
				out[k] = f.aired[k]
			}
			return out, nil
		},
		State:    f.state,
		Sessions: func() []*core.PlaySession { return f.sessions },
		Now:      func() time.Time { return f.now },
	})
}

func (f *stepAsideFixture) buildService(t *testing.T, protocol core.Protocol, stepAside *StepAside) (*Service, Visibility) {
	t.Helper()
	libs := []Library{
		{ID: 1, Name: "Movies", Type: core.TypeMovie, Virtual: true},
		{ID: 2, Name: "TV Shows", Type: core.TypeShow, Virtual: true},
	}
	cachedGet := func(_ context.Context, k core.RatingKey) (*core.Title, error) {
		if ti, ok := f.byKey[k]; ok {
			cp := ti
			return &cp, nil
		}
		return nil, core.ErrNotFound
	}
	var filter func(context.Context, []core.Title) []core.Title
	if stepAside != nil {
		filter = stepAside.WrapAvailable(nil)
	}
	vis := BindVisibility(nil, cachedGet, filter, func() []*core.PlaySession { return f.sessions }, libs)

	browse := func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
		var src []core.Title
		switch q.Filter.Type {
		case core.TypeMovie:
			src = f.movies
		case core.TypeShow:
			src = f.shows
		}
		var filtered []core.Title
		for _, item := range src {
			if slices.Contains(q.Filter.ExcludeKeys, item.Key) {
				continue
			}
			filtered = append(filtered, item)
		}
		total := len(filtered)
		start := min(max(q.Offset, 0), total)
		end := total
		if q.Limit > 0 {
			end = min(start+q.Limit, total)
		}
		return core.Page{Items: append([]core.Title(nil), filtered[start:end]...), Total: total}, nil
	}

	pageExclusions := func(ctx context.Context, viewer *core.User, typ core.MediaType) LibraryPageExclusions {
		if stepAside == nil {
			return LibraryPageExclusions{AvailabilityInSQL: true}
		}
		keys, active := stepAside.ExcludedKeys(ctx, viewer, typ)
		return LibraryPageExclusions{
			Keys:              keys,
			AvailabilityInSQL: !active,
		}
	}

	searchCatalog := func(ctx context.Context, _ string, _ []core.MediaType, _ int) ([]core.Title, error) {
		var all []core.Title
		all = append(all, f.movies...)
		all = append(all, f.shows...)
		if stepAside != nil {
			all = stepAside.FilterAvailable(ctx, all)
		}
		return all, nil
	}
	unrestricted := func(*core.User) bool {
		return stepAside == nil || !stepAside.Active()
	}

	d := ServiceDeps{
		Protocol: protocol,
		Active: func(_ context.Context, id int64) (*core.User, error) {
			return &core.User{ID: id, Username: "viewer"}, nil
		},
		Libraries:      libs,
		Visibility:     vis,
		State:          f.state,
		Browse:         browse,
		PageExclusions: pageExclusions,
		CachedGet:      cachedGet,
		CachedGetMany: func(_ context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			out := make(map[core.RatingKey]core.Title, len(keys))
			for _, k := range keys {
				if ti, ok := f.byKey[k]; ok {
					out[k] = ti
				}
			}
			return out, nil
		},
		Get: func(ctx context.Context, _ *core.User, k core.RatingKey) (*core.Title, error) {
			return cachedGet(ctx, k)
		},
		Children: func(_ context.Context, _ *core.User, k core.RatingKey) ([]core.Title, error) {
			return append([]core.Title(nil), f.children[k]...), nil
		},
		Grandchildren: func(_ context.Context, _ *core.User, k core.RatingKey) ([]core.Title, error) {
			var out []core.Title
			for _, season := range f.children[k] {
				out = append(out, f.children[season.Key]...)
			}
			return out, nil
		},
		SeasonEpisodes: func(ctx context.Context, u *core.User, show core.RatingKey, season, offset, limit int) (core.Page, bool, error) {
			if stepAside != nil && stepAside.HasShowEpisodes(core.WithCatalogViewer(ctx, u), show) {
				return core.Page{}, false, nil
			}
			sk := core.SeasonKey(show.TMDB(), season)
			items := append([]core.Title(nil), f.children[sk]...)
			total := len(items)
			start := min(max(offset, 0), total)
			end := total
			if limit > 0 {
				end = min(start+limit, total)
			}
			return core.Page{Items: items[start:end], Total: total}, true, nil
		},
		Search: SearchFor(SearchSources{Catalog: searchCatalog}, vis, unrestricted),
	}
	d.Search.Visibility = d.Visibility
	return &Service{d: d, cursors: &episodeCursors{}}, vis
}

func titleKeys(ts []core.Title) []core.RatingKey {
	out := make([]core.RatingKey, len(ts))
	for i := range ts {
		out[i] = ts[i].Key
	}
	return out
}

func TestStepAsideMoviesAndFullShowsHiddenOnPlex(t *testing.T) {
	f := newStepAsideFixture()
	sa := f.buildStepAside()
	plexSvc, plexVis := f.buildService(t, "plex", sa)
	ctx := context.Background()
	viewer := &core.User{ID: 1, Username: "alex"}

	moviesPage, err := plexSvc.LibraryItems(ctx, viewer, core.Device{}, "1", frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(moviesPage.Items), []core.RatingKey{f.freeMovie.Key}; !reflect.DeepEqual(got, want) || moviesPage.Total != 1 {
		t.Fatalf("plex movies = %v (total=%d), want %v", got, moviesPage.Total, want)
	}

	showsPage, err := plexSvc.LibraryItems(ctx, viewer, core.Device{}, "2", frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(showsPage.Items), []core.RatingKey{f.partialShow.Key}; !reflect.DeepEqual(got, want) || showsPage.Total != 1 {
		t.Fatalf("plex shows = %v (total=%d), want %v", got, showsPage.Total, want)
	}

	searchRes, err := plexSvc.Search(ctx, viewer, core.Device{}, "show", frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(searchRes.Page.Items), []core.RatingKey{f.freeMovie.Key, f.partialShow.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plex search = %v, want %v", got, want)
	}

	if _, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: viewer, Key: f.heldMovie.Key}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("held movie Item err = %v, want ErrNotFound", err)
	}
	if _, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: viewer, Key: f.fullShow.Key}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("fully held show Item err = %v, want ErrNotFound", err)
	}

	recentMovies, _, err := RecentlyAdded(ctx, viewer, core.TypeMovie, 10, plexVis, func(_ context.Context, q core.BrowseQuery) (core.Page, error) {
		return core.Page{Items: f.movies, Total: len(f.movies)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(recentMovies), []core.RatingKey{f.freeMovie.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plex recently added movies = %v, want %v", got, want)
	}
}

func TestStepAsidePartialShowServesOnlyMissingEpisodes(t *testing.T) {
	f := newStepAsideFixture()
	sa := f.buildStepAside()
	plexSvc, plexVis := f.buildService(t, "plex", sa)
	ctx := context.Background()
	viewer := &core.User{ID: 1, Username: "alex"}

	// Partial show remains openable on Plex.
	if detail, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: viewer, Key: f.partialShow.Key}); err != nil || detail.Title.Key != f.partialShow.Key {
		t.Fatalf("partial show Item = %+v, err=%v", detail, err)
	}

	// Season 1 is completely held on PMS and hidden; Season 2 is missing S02E02 and stays visible.
	seasons, err := plexSvc.Children(ctx, viewer, core.Device{}, f.partialShow.Key, frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(seasons.Items), []core.RatingKey{f.partS2.Key}; !reflect.DeepEqual(got, want) || seasons.Total != 1 {
		t.Fatalf("partial show seasons = %v (total=%d), want %v", got, seasons.Total, want)
	}
	if _, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: viewer, Key: f.partS1.Key}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("fully held season Item err = %v, want ErrNotFound", err)
	}

	// Season 2 serves ONLY the missing episode S02E02, hiding held S02E01.
	eps, err := plexSvc.Children(ctx, viewer, core.Device{}, f.partS2.Key, frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(eps.Items), []core.RatingKey{f.partS2E2Free.Key}; !reflect.DeepEqual(got, want) || eps.Total != 1 {
		t.Fatalf("season 2 episodes = %v (total=%d), want %v", got, eps.Total, want)
	}
	if _, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: viewer, Key: f.partS2E1Held.Key}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("held episode Item err = %v, want ErrNotFound", err)
	}
	if detail, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: viewer, Key: f.partS2E2Free.Key}); err != nil || detail.Title.Key != f.partS2E2Free.Key {
		t.Fatalf("missing episode Item = %+v, err=%v", detail, err)
	}

	// Unplayed badge counts subtract held unplayed episodes.
	counts, err := sa.AdjustUnplayedCounts(ctx, viewer.ID, []core.RatingKey{f.partialShow.Key, f.partS2.Key}, map[core.RatingKey]int{
		f.partialShow.Key: 4,
		f.partS2.Key:      2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if counts[f.partialShow.Key] != 1 || counts[f.partS2.Key] != 1 {
		t.Fatalf("adjusted unplayed counts = %v, want 1 and 1", counts)
	}

	// Next Up / On Deck: if viewer finished S02E01 (synced from PMS), Ether offers missing S02E02;
	// if viewer finished S01E01 where next episode S01E02 is held on PMS, Ether steps aside.
	f.state.statuses[viewer.ID] = map[core.RatingKey]core.WatchStatus{
		f.partS2E1Held.Key: {Key: f.partS2E1Held.Key, ViewCount: 1, LastViewedAt: f.now.Add(-time.Hour)},
		f.fullS1E1.Key:     {Key: f.fullS1E1.Key, ViewCount: 1, LastViewedAt: f.now.Add(-30 * time.Minute)},
	}
	f.state.history[viewer.ID] = []core.HistoryEntry{
		{Key: f.fullS1E1.Key, Completed: true, ViewedAt: f.now.Add(-30 * time.Minute)},
		{Key: f.partS2E1Held.Key, Completed: true, ViewedAt: f.now.Add(-time.Hour)},
	}
	cont := Continue{
		State:   f.state,
		Now:     func() time.Time { return f.now },
		Visible: plexVis.Visible,
		GetTitles: func(_ context.Context, keys []core.RatingKey) []core.Title {
			var out []core.Title
			for _, k := range keys {
				if ti, ok := f.byKey[k]; ok {
					out = append(out, ti)
				}
			}
			return out
		},
		NextEpisode: func(_ context.Context, k core.RatingKey) (*core.Title, error) {
			switch k {
			case f.fullS1E1.Key:
				cp := f.fullS1E2
				return &cp, nil
			case f.partS2E1Held.Key:
				cp := f.partS2E2Free
				return &cp, nil
			default:
				return nil, nil
			}
		},
		SameDayUnavailable: func(context.Context, *core.Title) bool { return false },
		SectionAllowed:     func(*core.User, core.MediaType) bool { return true },
	}
	deck, _ := cont.InProgress(ctx, viewer, "", 10)
	if got, want := titleKeys(deck), []core.RatingKey{f.partS2E2Free.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("plex Continue/OnDeck = %v, want %v", got, want)
	}
}

func TestStepAsideJellyfinUnaffected(t *testing.T) {
	f := newStepAsideFixture()
	// Protocol-scoped: Jellyfin never receives a StepAside instance.
	jfSvc, _ := f.buildService(t, "jellyfin", nil)
	ctx := context.Background()
	viewer := &core.User{ID: 1, Username: "alex"}

	moviesPage, err := jfSvc.LibraryItems(ctx, viewer, core.Device{}, "1", frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(moviesPage.Items), []core.RatingKey{f.heldMovie.Key, f.freeMovie.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("jellyfin movies = %v, want %v", got, want)
	}

	showsPage, err := jfSvc.LibraryItems(ctx, viewer, core.Device{}, "2", frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(showsPage.Items), []core.RatingKey{f.fullShow.Key, f.partialShow.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("jellyfin shows = %v, want %v", got, want)
	}

	seasons, err := jfSvc.Children(ctx, viewer, core.Device{}, f.partialShow.Key, frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(seasons.Items), []core.RatingKey{f.partS1.Key, f.partS2.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("jellyfin partial show seasons = %v, want %v", got, want)
	}

	eps, err := jfSvc.Children(ctx, viewer, core.Device{}, f.partS2.Key, frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := titleKeys(eps.Items), []core.RatingKey{f.partS2E1Held.Key, f.partS2E2Free.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("jellyfin season 2 episodes = %v, want %v", got, want)
	}
}

func TestStepAsideInProgressTitleRemainsVisibleAndPlayable(t *testing.T) {
	f := newStepAsideFixture()
	resumingViewer := &core.User{ID: 1, Username: "alex"}
	liveSessionViewer := &core.User{ID: 2, Username: "sam"}
	idleViewer := &core.User{ID: 3, Username: "pat"}

	// Viewer 1 has Continue Watching progress on Ether for heldMovie and fullS1E1.
	f.state.inProgress[resumingViewer.ID] = []core.WatchStatus{
		{Key: f.heldMovie.Key, ViewOffset: 45_000, ViewCount: 0, LastViewedAt: f.now.Add(-10 * time.Minute)},
		{Key: f.fullS1E1.Key, ViewOffset: 120_000, ViewCount: 0, LastViewedAt: f.now.Add(-5 * time.Minute)},
	}
	// Viewer 2 has an active PlaySession on Ether for heldMovie.
	f.sessions = []*core.PlaySession{
		{ID: "sess-1", UserID: liveSessionViewer.ID, Key: f.heldMovie.Key},
	}

	sa := f.buildStepAside()
	plexSvc, plexVis := f.buildService(t, "plex", sa)
	ctx := context.Background()

	// Viewer 1: heldMovie and fullS1E1 stay in Continue Watching and remain openable/playable,
	// along with fullS1E1's parent season and show.
	cont := Continue{
		State:   f.state,
		Now:     func() time.Time { return f.now },
		Visible: plexVis.Visible,
		GetTitles: func(_ context.Context, keys []core.RatingKey) []core.Title {
			var out []core.Title
			for _, k := range keys {
				if ti, ok := f.byKey[k]; ok {
					out = append(out, ti)
				}
			}
			return out
		},
		SameDayUnavailable: func(context.Context, *core.Title) bool { return false },
		SectionAllowed:     func(*core.User, core.MediaType) bool { return true },
	}
	deck, _ := cont.InProgress(ctx, resumingViewer, "", 10)
	if got, want := titleKeys(deck), []core.RatingKey{f.fullS1E1.Key, f.heldMovie.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resuming viewer Continue = %v, want %v", got, want)
	}

	for _, key := range []core.RatingKey{f.heldMovie.Key, f.fullShow.Key, f.fullS1.Key, f.fullS1E1.Key} {
		if _, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: resumingViewer, Key: key}); err != nil {
			t.Fatalf("resuming viewer Item(%v) err = %v, want nil", key, err)
		}
	}
	s1Eps, err := plexSvc.Children(ctx, resumingViewer, core.Device{}, f.fullS1.Key, frontend.ItemQuery{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	// Only the in-progress episode of the otherwise-held season stays visible.
	if got, want := titleKeys(s1Eps.Items), []core.RatingKey{f.fullS1E1.Key}; !reflect.DeepEqual(got, want) {
		t.Fatalf("resuming viewer season children = %v, want %v", got, want)
	}

	// Viewer 2 (active PlaySession on Ether): heldMovie remains openable/playable.
	if _, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: liveSessionViewer, Key: f.heldMovie.Key}); err != nil {
		t.Fatalf("live session viewer Item(heldMovie) err = %v, want nil", err)
	}

	// Viewer 3 (no progress or session on Ether): both remain hidden on the same Plex service.
	if _, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: idleViewer, Key: f.heldMovie.Key}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("idle viewer Item(heldMovie) err = %v, want ErrNotFound", err)
	}
	if _, err := plexSvc.Item(ctx, frontend.ItemRequest{Viewer: idleViewer, Key: f.fullShow.Key}); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("idle viewer Item(fullShow) err = %v, want ErrNotFound", err)
	}
}
