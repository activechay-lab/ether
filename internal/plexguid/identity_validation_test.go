package plexguid

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestDirectChildMatchCannotProveParentIdentity(t *testing.T) {
	for _, tc := range []struct {
		name, kind, ids, indices string
		key                      core.RatingKey
		want                     bool
	}{
		{"season lacks index", "season", "tmdb://1396", "", core.SeasonKey(1396, 1), false},
		{"episode lacks indices", "episode", "tmdb://1396", "", core.EpisodeKey(1396, 1, 2), false},
		{"episode lacks parent", "episode", "tmdb://1396", `,"index":2`, core.EpisodeKey(1396, 1, 2), false},
		{"season exact external ID", "season", "tmdb://1396/1", "", core.SeasonKey(1396, 1), false},
		{"episode exact external ID", "episode", "tmdb://1396/1/2", "", core.EpisodeKey(1396, 1, 2), false},
		{"episode exact indices", "episode", "tmdb://1396", `,"index":2,"parentIndex":1`, core.EpisodeKey(1396, 1, 2), false},
		{"episode contradicts external ID", "episode", "tmdb://1396/1/2", `,"index":3,"parentIndex":1`, core.EpisodeKey(1396, 1, 2), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := store.OpenMemory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			guid := "plex://" + tc.kind + "/602e5e2294f08e002d003344"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				typ := r.URL.Query().Get("type")
				if (tc.kind == "season" && typ == "3") || (tc.kind == "episode" && typ == "4") {
					fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":%q,"Guid":[{"id":%q}]%s}]}}`, guid, tc.kind, tc.ids, tc.indices)
					return
				}
				fmt.Fprint(w, `{"MediaContainer":{"Metadata":[]}}`)
			}))
			defer srv.Close()
			s := New(db, Options{BaseURL: srv.URL, Token: func() string { return "fixture" }})
			defer s.Close()
			got, err := s.Resolve(ctx, tc.key)
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.want {
				want = guid
			}
			if got != want {
				t.Fatalf("Resolve=%q, want %q", got, want)
			}
		})
	}
}

// A child proof belongs to one specific parent mapping, including during a
// concurrent rematch. Legacy rows without parentage remain available.
func TestChildProofFollowsParentMapping(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := New(db, Options{})
	defer s.Close()
	show, season, episode := core.ShowKey(1396), core.SeasonKey(1396, 1), core.EpisodeKey(1396, 1, 2)
	now := time.Now().Unix()
	const oldShow = "plex://show/aaaaaaaaaaaaaaaaaaaaaaaa"
	const newShow = "plex://show/bbbbbbbbbbbbbbbbbbbbbbbb"
	const seasonGUID = "plex://season/cccccccccccccccccccccccc"
	const episodeGUID = "plex://episode/dddddddddddddddddddddddd"
	if err = s.storeMapping(ctx, show, identityRecord{GUID: oldShow, TMDB: 1396, Title: "Breaking Bad", Year: 2008}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.storeMappingUnder(ctx, season, identityRecord{GUID: seasonGUID, TMDB: 1396}, now, show, oldShow); err != nil {
		t.Fatal(err)
	}
	if err = s.storeMappingUnder(ctx, episode, identityRecord{GUID: episodeGUID, TMDB: 1396}, now, season, seasonGUID); err != nil {
		t.Fatal(err)
	}
	// A legacy child carries no parentage. When its parent changes, its
	// identity proof is dropped with the rest of the old tree's children, so it
	// is withheld until the repair worker re-verifies it under the new tree.
	legacy := core.SeasonKey(1396, 2)
	const legacyGUID = "plex://season/eeeeeeeeeeeeeeeeeeeeeeee"
	if err = s.storeMapping(ctx, legacy, identityRecord{GUID: legacyGUID, TMDB: 1396}, now); err != nil {
		t.Fatal(err)
	}
	if err = s.storeMapping(ctx, show, identityRecord{GUID: newShow, TMDB: 1396, Title: "Breaking Bad", Year: 2008}, now); err != nil {
		t.Fatal(err)
	}
	if s.Cached(ctx, season) != "" || len(s.CachedMany(ctx, []core.RatingKey{season, episode})) != 0 {
		t.Fatal("stale children exposed")
	}
	if _, err = s.ExactKey(ctx, episodeGUID); err == nil {
		t.Fatal("stale reverse mapping exposed")
	}
	if s.Cached(ctx, legacy) != "" {
		t.Fatal("legacy child of a changed parent still served")
	}
	if _, ok := s.freshCached(ctx, legacy); ok {
		t.Fatal("legacy child still marked fresh")
	}
	if err = s.storeMappingUnder(ctx, season, identityRecord{GUID: seasonGUID, TMDB: 1396}, now, show, oldShow); err == nil {
		t.Fatal("stale parent accepted")
	}
}

// A guid is attached only when the canonical record agrees with the local
// title on identity: the same title released in another year (the 2026 film
// "Runner" vs the 1962 documentary of the same name) is a namesake, never the
// same work, so it must not be matched.
func TestSameTitleDifferentYearNotMatched(t *testing.T) {
	type canonical struct {
		title string
		year  int
	}
	for _, tc := range []struct {
		name      string
		local     string
		localYear int
		canonical canonical
		want      bool
	}{
		{"same title, different year", "Runner", 2026, canonical{"Runner", 1962}, false},
		{"title with release year, different year", "Runner (2026)", 2026, canonical{"Runner", 1962}, false},
		{"same title and year", "Runner", 2026, canonical{"Runner", 2026}, true},
		{"release-year tolerance one year", "Runner", 2026, canonical{"Runner", 2025}, true},
		{"title with year suffix, same year", "Runner (2026)", 2026, canonical{"Runner", 2026}, true},
		{"different title, same year", "Runner", 2026, canonical{"The Runner", 2026}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			db, err := store.OpenMemory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			const guid = "plex://movie/5d7768265af944001f1f6976"
			if _, err = db.Exec(`INSERT INTO catalog_titles(key,type,tmdb_id,title,year) VALUES(?,?,?,?,?)`, int64(core.MovieKey(770)), "movie", 770, tc.local, tc.localYear); err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":%q,"year":%d,"Guid":[{"id":"tmdb://770"}]}]}}`, guid, tc.canonical.title, tc.canonical.year)
			}))
			defer srv.Close()
			s := New(db, Options{BaseURL: srv.URL, Token: func() string { return "fixture" }})
			defer s.Close()
			got, err := s.Resolve(ctx, core.MovieKey(770))
			if err != nil {
				t.Fatal(err)
			}
			want := ""
			if tc.want {
				want = guid
			}
			if got != want {
				t.Fatalf("Resolve=%q, want %q", got, want)
			}
			if s.Cached(ctx, core.MovieKey(770)) != want {
				t.Fatalf("Cached=%q, want %q", s.Cached(ctx, core.MovieKey(770)), want)
			}
		})
	}
}
