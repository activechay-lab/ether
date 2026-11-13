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
	// identity proof is dropped with the rest of the old tree's children; it
	// then has no identity record, so it keeps serving as legacy until the
	// repair worker re-verifies it under the new tree.
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
	if s.Cached(ctx, legacy) != legacyGUID {
		t.Fatal("legacy child was withheld")
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
		{"missing provider year", "Runner", 2026, canonical{"Runner", 0}, false},
		{"missing local year", "Runner", 0, canonical{"Runner", 2026}, false},
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
			// Stored identities from the old rule must be withheld even if no
			// second namesake exists in Ether's catalogue, then proved lazily.
			if _, err = db.Exec(`INSERT INTO plexguid_mappings(rating_key,guid,checked_at) VALUES(?,?,?)`, int64(core.MovieKey(770)), guid, time.Now().Unix()); err != nil {
				t.Fatal(err)
			}
			if s.Cached(ctx, core.MovieKey(770)) != "" || len(s.CachedMany(ctx, []core.RatingKey{core.MovieKey(770)})) != 0 {
				t.Fatal("unproven identity served")
			}
			if _, err = s.ExactKey(ctx, guid); err == nil {
				t.Fatal("unproven reverse identity served")
			}
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

// A mapping stored before identity records existed (no plexguid_identity or
// plexguid_verified row) keeps serving on the hot path so an upgrade does not
// blank a large library's plex:// guids. Resolve re-verifies it under the
// stricter rule: a wrong legacy identity is dropped, a right one gains its
// identity record and keeps serving.
func TestLegacyRowWithheldUntilRepairDropsWrongOne(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(`INSERT INTO catalog_titles(key,type,tmdb_id,title,year) VALUES(?,?,?,?,?)`, int64(core.MovieKey(770)), "movie", 770, "Runner", 2026); err != nil {
		t.Fatal(err)
	}
	const (
		legacyGood = "plex://movie/5d7768265af944001f1f6976"
		legacyBad  = "plex://movie/5d7768265af944001f1f6962"
	)
	now := time.Now().Unix()
	// Two legacy rows: one whose canonical record agrees, one whose canonical
	// record is the 1962 namesake.
	if _, err = db.Exec(`INSERT INTO plexguid_mappings(rating_key,guid,checked_at) VALUES(?,?,?), (?,?,?)`,
		int64(core.MovieKey(770)), legacyBad, now, int64(core.MovieKey(550)), legacyGood, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO catalog_titles(key,type,tmdb_id,title,year) VALUES(?,?,?,?,?)`, int64(core.MovieKey(550)), "movie", 550, "Runner", 1962); err != nil {
		t.Fatal(err)
	}
	s := New(db, Options{})
	defer s.Close()
	// Legacy rows are withheld until canonical verification.
	if got := s.Cached(ctx, core.MovieKey(770)); got != "" {
		t.Fatalf("legacy wrong row served before repair: %q", got)
	}
	if got := s.Cached(ctx, core.MovieKey(550)); got != "" {
		t.Fatalf("legacy right row served before repair: %q", got)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/metadata/matches":
			// No search hit proves identity for either title; verification must
			// come from each canonical record.
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[]}}`)
		case "/library/metadata/5d7768265af944001f1f6976":
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Runner","year":1962,"Guid":[{"id":"tmdb://550"}]}]}}`, legacyGood)
		case "/library/metadata/5d7768265af944001f1f6962":
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"guid":%q,"type":"movie","title":"Runner","year":1962,"Guid":[{"id":"tmdb://770"}]}]}}`, legacyBad)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	s.o.BaseURL = srv.URL
	s.o.Token = func() string { return "fixture" }
	// Repair re-verifies each legacy row's stored guid against its canonical
	// record. The wrong one (1962 namesake of a 2026 film) fails, and with no
	// search hit to replace it, is dropped; the right one's record agrees and
	// it keeps serving with an identity record.
	if _, err = s.reverify(ctx, core.MovieKey(770)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.reverify(ctx, core.MovieKey(550)); err != nil {
		t.Fatal(err)
	}
	if got := s.Cached(ctx, core.MovieKey(770)); got != "" {
		t.Fatalf("wrong legacy identity survived repair: %q", got)
	}
	if got := s.Cached(ctx, core.MovieKey(550)); got != legacyGood {
		t.Fatalf("right legacy identity lost after repair: %q", got)
	}
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM plexguid_identity WHERE rating_key=? AND guid=?`, int64(core.MovieKey(550)), legacyGood).Scan(&n); err != nil || n != 1 {
		t.Fatalf("right legacy row not granted identity record: n=%d err=%v", n, err)
	}
}
