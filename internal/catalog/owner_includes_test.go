package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

type includeTMDB struct {
	TMDB
	movies map[int64]*tmdb.MovieDetails
}

func (f *includeTMDB) MovieDetails(_ context.Context, id int64) (*tmdb.MovieDetails, error) {
	if d := f.movies[id]; d != nil {
		return d, nil
	}
	return nil, core.ErrNotFound
}

func (f *includeTMDB) MovieDetailsFresh(ctx context.Context, id int64) (*tmdb.MovieDetails, error) {
	return f.MovieDetails(ctx, id)
}

func TestExplainBrowsabilityAndOwnerIncludes(t *testing.T) {
	ctx := t.Context()
	db := store.OpenTest(t)
	frenchRule := core.LibraryExclusion{
		Name:      "French",
		MediaType: "movie",
		Match:     "all",
		Scope:     core.VisibilityEverywhere,
		Rules: []core.CollectionRule{
			{Field: "language", Op: "is", Value: json.RawMessage(`"fr"`)},
		},
	}
	cfg := config.Catalog{
		MinVotes:      50,
		MinPopularity: 5,
		Exclusions:    []core.LibraryExclusion{frenchRule},
	}
	fakeTMDB := &includeTMDB{
		movies: map[int64]*tmdb.MovieDetails{
			303: {
				Movie: tmdb.Movie{
					ID:               303,
					Title:            "Obscure Gem",
					ReleaseDate:      "2019-05-10",
					OriginalLanguage: "en",
					VoteCount:        12,
					Popularity:       1.4,
					VoteAverage:      7.8,
				},
				Runtime: 98,
			},
		},
	}
	c, err := New(db, fakeTMDB, cfg, nil, WithLibraryPolicies(testPolicies(cfg.Exclusions, cfg.VisibilityDefault)))
	if err != nil {
		t.Fatal(err)
	}

	// 1. Stored below-floor movie: ExplainBrowsability agrees with Browse and names exact vote floor.
	lowVotes := core.Title{
		Key:        core.MovieKey(101),
		IDs:        core.IDs{TMDB: 101},
		Type:       core.TypeMovie,
		Title:      "Festival Short",
		Language:   "en",
		VoteCount:  12,
		Popularity: 2.1,
		Released:   time.Now().Add(-365 * 24 * time.Hour),
	}
	if err := c.upsert(ctx, lowVotes, upsertOpts{browsable: false}); err != nil {
		t.Fatal(err)
	}
	page, err := c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("Browse before include = %+v, %v; want 0", page, err)
	}
	st, err := c.ExplainBrowsability(ctx, lowVotes.Key)
	if err != nil {
		t.Fatal(err)
	}
	if st.Browsable || !st.CanInclude || st.Included || st.ReleaseGated {
		t.Fatalf("unexpected status before include: %+v", st)
	}
	if len(st.Reasons) != 1 || st.Reasons[0] != "Below your popularity floor (12 votes; floor 50)" {
		t.Fatalf("unexpected reasons: %+v", st.Reasons)
	}

	// IncludeTitle promotes to browsable=1 and Browse now includes it.
	st, err = c.IncludeTitle(ctx, lowVotes.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Browsable || !st.Included || !st.InLibraryTier || st.CanInclude || len(st.Reasons) != 0 {
		t.Fatalf("unexpected status after include: %+v", st)
	}
	page, err = c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil || page.Total != 1 || page.Items[0].Key != lowVotes.Key {
		t.Fatalf("Browse after include = %+v, %v; want Festival Short", page, err)
	}

	// Tier migration touch sources keep owner-included titles in Tier A.
	touched, counts, err := c.collectTouched(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !touched.has(lowVotes.Key) || counts["catalog_owner_includes.key"] != 1 {
		t.Fatalf("collectTouched missed owner include: has=%v counts=%v", touched.has(lowVotes.Key), counts)
	}

	// RemoveInclude demotes a below-floor title back out of Browse.
	st, err = c.RemoveInclude(ctx, lowVotes.Key)
	if err != nil {
		t.Fatal(err)
	}
	if st.Browsable || st.Included || !st.CanInclude {
		t.Fatalf("unexpected status after remove: %+v", st)
	}
	page, err = c.Browse(ctx, core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil || page.Total != 0 {
		t.Fatalf("Browse after remove = %+v, %v; want 0", page, err)
	}

	// 2. Title excluded by a named catalogue rule ("French"): override works for
	// owner and inherited viewers, while custom viewer rules still win.
	french := core.Title{
		Key:        core.MovieKey(202),
		IDs:        core.IDs{TMDB: 202},
		Type:       core.TypeMovie,
		Title:      "Le Samouraï",
		Language:   "fr",
		VoteCount:  900,
		Popularity: 18,
		Released:   time.Now().Add(-400 * 24 * time.Hour),
	}
	if err := c.upsert(ctx, french, upsertOpts{browsable: true}); err != nil {
		t.Fatal(err)
	}
	st, err = c.ExplainBrowsability(ctx, french.Key)
	if err != nil {
		t.Fatal(err)
	}
	if st.Browsable || !st.CanInclude || len(st.Reasons) != 1 || st.Reasons[0] != "Excluded by your catalogue rule “French”" {
		t.Fatalf("unexpected rule status: %+v", st)
	}

	inheritedViewer := &core.User{ID: 10}
	customViewer := &core.User{ID: 20}
	if err := c.PutViewerRules(ctx, customViewer.ID, ViewerRules{
		Mode:       "custom",
		Exclusions: []core.LibraryExclusion{frenchRule},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.IncludeTitle(ctx, french.Key); err != nil {
		t.Fatal(err)
	}
	if !c.Visible(&french) || !c.SearchVisible(&french) {
		t.Fatal("owner-included title remained hidden from global Visible/SearchVisible")
	}
	if !c.VisibleForViewer(inheritedViewer, &french) || !c.DiscoveryVisibleForViewer(inheritedViewer, &french) {
		t.Fatal("inherited viewer should see owner-included title")
	}
	if c.VisibleForViewer(customViewer, &french) || c.DiscoveryVisibleForViewer(customViewer, &french) {
		t.Fatal("custom viewer rule was bypassed by owner include")
	}
	customPage, err := c.Browse(core.WithCatalogViewer(ctx, customViewer), core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil || customPage.Total != 0 {
		t.Fatalf("custom viewer Browse leaked included title: %+v %v", customPage, err)
	}
	inheritedPage, err := c.Browse(core.WithCatalogViewer(ctx, inheritedViewer), core.BrowseQuery{Filter: core.Filter{Type: core.TypeMovie}, Limit: 10})
	if err != nil || inheritedPage.Total != 1 {
		t.Fatalf("inherited viewer Browse missed included title: %+v %v", inheritedPage, err)
	}

	// 3. Unstored TMDB title: IncludeTitle fetches full details, promotes to
	// library tier, and subsequent refreshes preserve browsable=1.
	unstoredKey := core.MovieKey(303)
	st, err = c.ExplainBrowsability(ctx, unstoredKey)
	if err != nil {
		t.Fatal(err)
	}
	if st.Browsable || st.InLibraryTier || !st.CanInclude {
		t.Fatalf("unexpected unstored status: %+v", st)
	}
	st, err = c.IncludeTitle(ctx, unstoredKey)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Browsable || !st.Included || !st.InLibraryTier {
		t.Fatalf("unstored title not promoted: %+v", st)
	}
	if !c.meetsThreshold(core.Title{Key: unstoredKey, Type: core.TypeMovie, VoteCount: 12, Popularity: 1.4, Released: time.Now().Add(-24 * time.Hour)}) {
		t.Fatal("meetsThreshold did not preserve owner-included title on refresh")
	}
}

func TestOwnerIncludesRespectHardGates(t *testing.T) {
	ctx := t.Context()
	db := store.OpenTest(t)
	safeSvc, err := safety.New(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	owner := &core.User{ID: 1, IsOwner: true, MaxContentRating: "PG-13"}
	kid := &core.User{ID: 2, MaxContentRating: "PG"}
	if err := safeSvc.Put(ctx, owner.ID, safety.Policy{BlockedGenres: []string{"Horror"}}); err != nil {
		t.Fatal(err)
	}

	cfg := config.Catalog{
		MinVotes:           50,
		MinPopularity:      5,
		ShowUpcomingMovies: true,
		ReleaseMode:        config.ModeDigital,
	}
	c, err := New(db, nil, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	safeSvc.SetCatalogPolicy(c.VisibleForViewer, c.DiscoveryVisibleForViewer)
	c.SetOwnerContentPolicy(func(_ context.Context, title *core.Title) bool {
		return safeSvc.PolicyAllowed(owner, title)
	})

	gatedKey := core.MovieKey(404)
	c.SetReleaseGate(func(_ context.Context, title core.Title) bool {
		return title.Key != gatedKey
	})

	assertRejected := func(name string, title core.Title, wantReason string, wantReleaseGated bool) {
		t.Helper()
		if err := c.upsert(ctx, title, upsertOpts{browsable: false}); err != nil {
			t.Fatalf("%s upsert: %v", name, err)
		}
		st, err := c.ExplainBrowsability(ctx, title.Key)
		if err != nil {
			t.Fatalf("%s ExplainBrowsability: %v", name, err)
		}
		if st.CanInclude {
			t.Fatalf("%s: CanInclude = true, want false (%+v)", name, st)
		}
		if st.ReleaseGated != wantReleaseGated {
			t.Fatalf("%s: ReleaseGated = %v, want %v (%+v)", name, st.ReleaseGated, wantReleaseGated, st)
		}
		if wantReason != "" {
			found := false
			for _, r := range st.Reasons {
				if r == wantReason {
					found = true
				}
			}
			if !found {
				t.Fatalf("%s: reasons %v missing %q", name, st.Reasons, wantReason)
			}
		}
		_, err = c.IncludeTitle(ctx, title.Key)
		var conflict interface{ Conflict() bool }
		if err == nil || !errors.As(err, &conflict) || !conflict.Conflict() {
			t.Fatalf("%s: IncludeTitle err = %v, want Conflict error", name, err)
		}
	}

	now := time.Now()
	assertRejected("adult", core.Title{
		Key: core.MovieKey(401), Type: core.TypeMovie, Title: "Adult Film",
		Adult: true, VoteCount: 10, Released: now.Add(-400 * 24 * time.Hour),
	}, "Adult titles are off", false)

	// Even with ShowUpcomingMovies=true, unreleased movies must NEVER offer Add to library.
	assertRejected("unreleased", core.Title{
		Key: core.MovieKey(402), Type: core.TypeMovie, Title: "Future Film",
		VoteCount: 10, Released: now.Add(30 * 24 * time.Hour),
	}, "", true)

	assertRejected("digital-gated", core.Title{
		Key: core.MovieKey(403), Type: core.TypeMovie, Title: "In Theatres Now",
		VoteCount: 10, Released: now.Add(-10 * 24 * time.Hour), HomeReleaseChecked: true,
	}, "Waiting for a playable release (new movie)", true)

	assertRejected("availability-gated", core.Title{
		Key: gatedKey, Type: core.TypeMovie, Title: "Unplayable New Movie",
		VoteCount: 10, Released: now.Add(-200 * 24 * time.Hour),
	}, "Waiting for a playable release (new movie)", true)

	assertRejected("owner-content-policy-genre", core.Title{
		Key: core.MovieKey(405), Type: core.TypeMovie, Title: "Blocked Horror",
		Genres: []string{"Horror"}, VoteCount: 10, Released: now.Add(-200 * 24 * time.Hour),
	}, "Excluded by your content rules", false)

	assertRejected("owner-content-policy-rating", core.Title{
		Key: core.MovieKey(406), Type: core.TypeMovie, Title: "Rated R Film",
		ContentRating: "R", VoteCount: 10, Released: now.Add(-200 * 24 * time.Hour),
	}, "Excluded by your content rules", false)

	// Viewer safety restrictions still apply to an included PG-13 movie (owner sees it, PG kid does not).
	pg13 := core.Title{
		Key: core.MovieKey(407), Type: core.TypeMovie, Title: "PG-13 Indie",
		ContentRating: "PG-13", VoteCount: 12, Popularity: 1.5,
		Released: now.Add(-250 * 24 * time.Hour),
	}
	if err := c.upsert(ctx, pg13, upsertOpts{browsable: false}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.IncludeTitle(ctx, pg13.Key); err != nil {
		t.Fatal(err)
	}
	if !safeSvc.Allowed(owner, &pg13) {
		t.Fatal("owner should be allowed to view PG-13 included title")
	}
	if safeSvc.Allowed(kid, &pg13) {
		t.Fatal("viewer MaxContentRating=PG was bypassed by owner include")
	}
}

func TestOwnerIncludesPostgresDDLAndBrowsePlan(t *testing.T) {
	pgSQL := store.TranslateDDL(`CREATE TABLE catalog_owner_includes (key INTEGER PRIMARY KEY, added_at INTEGER NOT NULL DEFAULT 0);`)
	if !strings.Contains(pgSQL, "key BIGINT PRIMARY KEY") {
		t.Fatalf("TranslateDDL did not widen primary key to BIGINT for PostgreSQL: %s", pgSQL)
	}

	ctx := t.Context()
	db := store.OpenTest(t)
	c, err := New(db, nil, config.Catalog{MinVotes: 50, MinPopularity: 5}, nil)
	if err != nil {
		t.Fatal(err)
	}
	movie := core.Title{
		Key: core.MovieKey(501), Type: core.TypeMovie, Title: "Plan Check",
		VoteCount: 8, Popularity: 1.1, Released: time.Now().Add(-365 * 24 * time.Hour),
	}
	if err := c.upsert(ctx, movie, upsertOpts{browsable: false}); err != nil {
		t.Fatal(err)
	}

	whereBefore, _, err := c.browseWhere(core.Filter{Type: core.TypeMovie, LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.IncludeTitle(ctx, movie.Key); err != nil {
		t.Fatal(err)
	}
	whereAfter, _, err := c.browseWhere(core.Filter{Type: core.TypeMovie, LocalOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if whereBefore != whereAfter || strings.Contains(whereAfter, "catalog_owner_includes") {
		t.Fatalf("passive browseWhere changed after owner include:\nbefore: %s\nafter:  %s", whereBefore, whereAfter)
	}

	// Verify OwnerIncluded uses a single PK lookup in SQLite's query planner.
	rows, err := db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT 1 FROM catalog_owner_includes WHERE key=?`, int64(movie.Key))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	plan := strings.Join(details, " | ")
	if !strings.Contains(plan, "USING INTEGER PRIMARY KEY") {
		t.Fatalf("OwnerIncluded query plan did not use primary key: %s", plan)
	}
}
