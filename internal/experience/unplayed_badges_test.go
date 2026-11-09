package experience

import (
	"context"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

// badgePolicy restricts a viewer with a rating cap, as safety does.
type badgePolicy struct{}

func (badgePolicy) Allowed(*core.User, *core.Title) bool { return true }
func (badgePolicy) Restricted(u *core.User) bool         { return u.MaxContentRating != "" }

// pendingBadgeState holds watch writes the database does not have yet, as
// state's pending writes do: reads overlay them.
type pendingBadgeState struct {
	*state.State
	writes map[core.RatingKey]int // view count the pending write leaves
}

func (p pendingBadgeState) PendingKeys(int64) []core.RatingKey {
	var keys []core.RatingKey
	for key := range p.writes {
		keys = append(keys, key)
	}
	return keys
}

func (p pendingBadgeState) PageUserData(ctx context.Context, userID int64, keys []core.RatingKey) (map[core.RatingKey]core.WatchStatus, error) {
	data, err := p.State.PageUserData(ctx, userID, keys)
	for key, views := range p.writes {
		if status, ok := data[key]; ok {
			status.ViewCount = views
			data[key] = status
		}
	}
	return data, err
}

// The counted badges are the listed ones: specials and seasons, capped,
// unaired, adult, hidden-series and restricted-viewer cases, availability's
// evidence window and watch writes not stored yet. Runs on SQLite and
// (ETHER_TEST_POSTGRES) PostgreSQL.
func TestLocalUnplayedCountsMatchListedEpisodes(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	now := time.Now().UTC().Truncate(time.Second)
	c, err := catalog.New(db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	old := now.Add(-30 * 24 * time.Hour)
	recent := now.Add(-2 * time.Hour)
	put := func(title core.Title) {
		t.Helper()
		if err := c.UpsertTitle(ctx, title, title.Type == core.TypeShow); err != nil {
			t.Fatal(err)
		}
	}
	episode := func(show int64, season, number int, released time.Time) core.Title {
		return core.Title{Key: core.EpisodeKey(show, season, number), Type: core.TypeEpisode, ShowKey: core.ShowKey(show), Season: season, Episode: number, Title: "E", Released: released, ContentRating: "TV-PG"}
	}
	// Show 1: specials, two seasons, an unaired, an adult and a stricter
	// rated episode, two inside the evidence window (one without evidence).
	put(core.Title{Key: core.ShowKey(1), Type: core.TypeShow, Title: "One", Released: old, ContentRating: "TV-PG"})
	for _, e := range []core.Title{episode(1, 0, 1, old), episode(1, 0, 2, old), episode(1, 1, 1, old), episode(1, 1, 2, old), episode(1, 1, 3, old),
		episode(1, 2, 1, old), episode(1, 2, 2, recent), episode(1, 2, 3, recent), episode(1, 2, 4, now.Add(48*time.Hour))} {
		put(e)
	}
	adult := episode(1, 1, 4, old)
	adult.Adult = true
	put(adult)
	mature := episode(1, 1, 5, old)
	mature.ContentRating = "TV-MA"
	put(mature)
	// Show 2: more aired episodes than the local list holds.
	put(core.Title{Key: core.ShowKey(2), Type: core.TypeShow, Title: "Two", Released: old})
	for n := 1; n <= localEpisodeCap+1; n++ {
		put(episode(2, 1, n, old))
	}
	// Show 3: an adult series. Show 4: no stored episodes.
	put(core.Title{Key: core.ShowKey(3), Type: core.TypeShow, Title: "Three", Released: old, Adult: true})
	put(episode(3, 1, 1, old))
	put(core.Title{Key: core.ShowKey(4), Type: core.TypeShow, Title: "Four", Released: old})

	watch := state.New(db, state.Options{})
	t.Cleanup(func() { watch.Close(ctx) })
	if err := watch.ScrobbleMany(ctx, 7, []core.RatingKey{core.EpisodeKey(1, 0, 1), core.EpisodeKey(1, 1, 1), core.EpisodeKey(1, 1, 2),
		core.EpisodeKey(1, 2, 2), core.EpisodeKey(1, 1, 4), core.EpisodeKey(1, 2, 4), core.EpisodeKey(2, 1, 1)}); err != nil {
		t.Fatal(err)
	}
	// Pending: S1E3 played, S1E2 (played in the database) marked unplayed.
	st := pendingBadgeState{State: watch, writes: map[core.RatingKey]int{core.EpisodeKey(1, 1, 3): 1, core.EpisodeKey(1, 1, 2): 0}}

	noEvidence := core.EpisodeKey(1, 2, 3)
	settled := now.Add(-24 * time.Hour)
	var requested []core.RatingKey
	deps := ServiceDeps{
		Visibility: Visibility{
			Policy:     badgePolicy{},
			SectionFor: func(core.MediaType) (int, bool) { return 2, true },
			GetTitle:   c.CachedGet,
			FilterAvailable: func(_ context.Context, titles []core.Title) []core.Title {
				var out []core.Title
				for _, title := range titles {
					if title.Key == noEvidence && title.Released.After(settled) {
						continue
					}
					out = append(out, title)
				}
				return out
			},
		},
		AiredEpisodes:     c.CachedAiredEpisodes,
		AiredEpisodesMany: c.CachedAiredEpisodesMany,
		CachedGetMany: func(ctx context.Context, keys []core.RatingKey) (map[core.RatingKey]core.Title, error) {
			requested = append(requested, keys...)
			return c.CachedGetMany(ctx, keys)
		},
		AiredEpisodeTallies:   c.AiredEpisodeTallies,
		EpisodesSettledBefore: func(context.Context) time.Time { return settled },
		State:                 st,
	}
	keys := []core.RatingKey{core.ShowKey(1), core.SeasonKey(1, 0), core.SeasonKey(1, 1), core.SeasonKey(1, 2), core.SeasonKey(1, 3),
		core.ShowKey(2), core.SeasonKey(2, 1), core.ShowKey(3), core.SeasonKey(3, 1), core.ShowKey(4), core.MovieKey(5)}
	for _, viewer := range []*core.User{{ID: 7}, {ID: 7, MaxContentRating: "TV-14"}, {ID: 8}} {
		d := deps
		d.Active = func(context.Context, int64) (*core.User, error) { return viewer, nil }
		s := &Service{d: d}
		want, err := s.listedUnplayedCounts(ctx, viewer, keys)
		if err != nil {
			t.Fatal(err)
		}
		requested = nil
		got, err := s.LocalUnplayedCounts(ctx, viewer, keys)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("viewer %+v: counted %v, listed %v", viewer, got, want)
		}
		if viewer.MaxContentRating == "" && slices.Contains(requested, core.EpisodeKey(1, 2, 1)) {
			t.Fatalf("viewer %+v: counted badges read a settled episode", viewer)
		}
	}
	// The cases above are not vacuous: for viewer 7 show 1 has S0E2,
	// S1E2 (pending unplay), S1E5, S2E1 unplayed, S1E3 is pending-played,
	// S2E3 lacks evidence; the TV-14 viewer loses S1E5.
	s := &Service{d: deps}
	s.d.Active = func(context.Context, int64) (*core.User, error) { return &core.User{ID: 7}, nil }
	got, err := s.LocalUnplayedCounts(ctx, &core.User{ID: 7}, keys)
	want := map[core.RatingKey]int{core.ShowKey(1): 4, core.SeasonKey(1, 0): 1, core.SeasonKey(1, 1): 2, core.SeasonKey(1, 2): 1, core.SeasonKey(1, 3): 0,
		core.ShowKey(3): 0, core.SeasonKey(3, 1): 0, core.ShowKey(4): 0}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("badges %v, want %v (%v)", got, want, err)
	}
	s.d.Active = func(context.Context, int64) (*core.User, error) {
		return &core.User{ID: 7, MaxContentRating: "TV-14"}, nil
	}
	got, err = s.LocalUnplayedCounts(ctx, &core.User{ID: 7, MaxContentRating: "TV-14"}, []core.RatingKey{core.ShowKey(1)})
	if err != nil || got[core.ShowKey(1)] != 3 {
		t.Fatalf("restricted badge %v (%v)", got, err)
	}
}
