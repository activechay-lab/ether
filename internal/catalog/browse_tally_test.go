package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/contentrating"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// A library page's total must be the number of titles its pages return, for
// every viewer restriction, availability hidden set, release setting and
// clock, through every kind of catalogue write and tally fold. The tally
// answers the common listings; this pins it to the direct count and to the
// pages themselves.
func TestBrowseTallyTotalsMatchPages(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	c, err := New(db, nil, config.Catalog{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	rng := rand.New(rand.NewPCG(7, 11))
	ratings := []string{"", "G", "PG", "PG-13", "R", "NC-17", "TV-Y", "TV-14", "TV-MA", "XYZ"}
	genres := []string{"Drama", "Comedy", "Kids", "Horror", ""}
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	released := func() int64 {
		switch rng.IntN(8) {
		case 0:
			return 0 // unknown
		case 1:
			return now.Add(time.Duration(rng.IntN(48)) * time.Hour).Unix() // coming days
		case 2:
			return now.Add(-time.Duration(rng.IntN(3600)) * time.Second).Unix() // the last hour
		case 3:
			return -int64(rng.IntN(1_000_000_000)) // before 1970
		default:
			return now.Add(-time.Duration(rng.IntN(20_000)) * time.Hour).Unix()
		}
	}
	setGenres := func(key core.RatingKey) {
		exec(`DELETE FROM catalog_genres WHERE key=?`, int64(key))
		for _, g := range genres {
			if rng.IntN(3) == 0 {
				exec(`INSERT INTO catalog_genres(key,genre) VALUES(?,?) ON CONFLICT DO NOTHING`, int64(key), g)
			}
		}
		exec(`DELETE FROM catalog_countries WHERE key=?`, int64(key))
		for _, co := range []string{"US", "GB", "FR", ""} {
			if rng.IntN(3) == 0 {
				exec(`INSERT INTO catalog_countries(key,country) VALUES(?,?) ON CONFLICT DO NOTHING`, int64(key), co)
			}
		}
		exec(`DELETE FROM catalog_studios WHERE key=?`, int64(key))
		for _, st := range []string{"Acme", "Banner", "acme", "", "Échelon", "Zeta", "Delta Films", "omega", "Beta", "Kino", "Lumen", "Mono"} {
			if rng.IntN(4) == 0 {
				exec(`INSERT INTO catalog_studios(key,studio) VALUES(?,?) ON CONFLICT DO NOTHING`, int64(key), st)
			}
		}
	}
	// Sort titles in every jump-bar bucket: digits and punctuation, letters,
	// accented and past-'z' first characters.
	sortTitle := func(key core.RatingKey) string {
		return []string{"alpha", "beta", "zeta", "1st", "éclair", "~x", "Zed", "{brace", "mango", "#hash"}[rng.IntN(10)] + fmt.Sprint(int64(key))
	}
	var keys []core.RatingKey
	insert := func(key core.RatingKey, typ core.MediaType) {
		r := ratings[rng.IntN(len(ratings))]
		exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult,detail,content_rating,content_rating_rank,popularity,vote_count,year)
			VALUES(?,?,?,?,?,?,?,1,?,?,?,?,?)`, int64(key), string(typ), fmt.Sprint("t", int64(key)), sortTitle(key), released(),
			flagInt(rng.IntN(5) > 0), flagInt(rng.IntN(10) == 0), r, int(contentrating.LevelOf(r)), float64(rng.IntN(20)), rng.IntN(50), []int{0, 1988, 1994, 2003, 2019, 2026}[rng.IntN(6)])
		setGenres(key)
		keys = append(keys, key)
	}
	for i := int64(1); i <= 140; i++ {
		insert(core.MovieKey(i), core.TypeMovie)
	}
	for i := int64(1); i <= 70; i++ {
		insert(core.ShowKey(i), core.TypeShow)
	}
	// An episode and a season never count toward a movie or series library.
	exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult,detail) VALUES(?,?,?,?,?,1,0,1)`, int64(core.EpisodeKey(1, 1, 1)), "episode", "e", "e", now.Add(-time.Hour*30).Unix())
	// The availability component owns this table; its schema as there.
	exec(`CREATE TABLE IF NOT EXISTS availability_hidden(kind TEXT NOT NULL,generation TEXT NOT NULL,rating_key BIGINT NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(kind,generation,rating_key))`)
	for _, k := range keys {
		if rng.IntN(4) == 0 {
			kind := "movie"
			if k.Kind() == core.KindShow {
				kind = "show"
			}
			exec(`INSERT INTO availability_hidden(kind,generation,rating_key,created_at) VALUES(?,?,?,0)`, kind, "g-"+kind, int64(k))
		}
	}
	excluded := []core.RatingKey{keys[3], keys[50], keys[150], core.MovieKey(9999)}
	// The state component owns this table; its columns the filter reads.
	exec(`CREATE TABLE IF NOT EXISTS state_watch(user_id INTEGER NOT NULL, rating_key INTEGER NOT NULL, view_offset_ns INTEGER NOT NULL DEFAULT 0, view_count INTEGER NOT NULL DEFAULT 0, last_viewed_at INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(user_id, rating_key))`)
	for i, k := range keys {
		if i%3 == 0 {
			exec(`INSERT INTO state_watch(user_id,rating_key,view_count) VALUES(7,?,?) ON CONFLICT DO NOTHING`, int64(k), i%2)
		}
	}
	unwatched := true
	filters := []core.Filter{
		{Type: core.TypeMovie},
		{Type: core.TypeShow},
		{},
		{Type: core.TypeMovie, Genres: []string{"Drama"}},
		{Type: core.TypeShow, Kids: true},
		{Genres: []string{"Kids"}, Kids: true},
		{Type: core.TypeMovie, HiddenMovieGeneration: "g-movie"},
		{Type: core.TypeShow, HiddenShowGeneration: "g-show", Genres: []string{"Comedy"}},
		{HiddenMovieGeneration: "g-movie", HiddenShowGeneration: "g-show"},
		{Type: core.TypeMovie, HiddenMovieGeneration: "g-movie", DigitalDiscovery: true, DigitalDateEvidence: true},
		{Type: core.TypeShow, HiddenShowGeneration: "g-show", DigitalDiscovery: true},
		// A restricted viewer: rating cap, key exclusions, empty block lists.
		{Type: core.TypeMovie, MaxContentRating: "PG-13", ExcludeKeys: excluded, ExcludeGenres: []string{}, ExcludeKeywords: []string{}, CatalogViewerID: 5, HiddenMovieGeneration: "g-movie"},
		{Type: core.TypeShow, MinContentRating: "PG", MaxContentRating: "TV-MA", Genres: []string{"Horror"}, ExcludeKeys: excluded},
		{MaxContentRating: "R", KnownAddedDate: true},
		{Type: core.TypeMovie, Countries: []string{"US"}},
		{Type: core.TypeMovie, YearFrom: 1990, YearTo: 1999},
		{Type: core.TypeShow, YearFrom: 2003, MaxContentRating: "TV-14", HiddenShowGeneration: "g-show", ExcludeKeys: excluded},
		{YearTo: 2010},
		{Type: core.TypeMovie, Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatched}},
		{Type: core.TypeShow, Genres: []string{"Drama"}, MaxContentRating: "TV-14", HiddenShowGeneration: "g-show", Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatched}},
		{Type: core.TypeShow, Countries: []string{"GB"}, MaxContentRating: "TV-14", ExcludeKeys: excluded, HiddenShowGeneration: "g-show"},
		{Countries: []string{"FR"}, HiddenMovieGeneration: "g-movie"},
		// Alphabet jumps on bucket edges: Plex firstCharacter and Jellyfin's
		// NameStartsWith=B, NameLessThan=A ("#") and NameStartsWithOrGreater.
		{Type: core.TypeMovie, FirstCharacter: "B"},
		{FirstCharacter: "~"},
		{Type: core.TypeShow, NameStartsWith: "z"},
		{NameLessThan: "A"},
		{Type: core.TypeMovie, NameStartsWith: "É"},
		{Type: core.TypeMovie, NameStartsWithOrGreater: "M", MaxContentRating: "PG-13", ExcludeKeys: excluded, HiddenMovieGeneration: "g-movie"},
		{Type: core.TypeShow, NameStartsWithOrGreater: "b", NameLessThan: "N", Group: &core.FilterGroup{UserID: 7, Unwatched: &unwatched}},
		// Counted directly: these must still agree with their pages.
		{Type: core.TypeMovie, Genres: []string{"Drama", "Comedy"}},
		{Type: core.TypeMovie, ExcludeGenres: []string{"horror"}},
		{Type: core.TypeMovie, MaxContentRating: "XYZ"},
		{Type: core.TypeMovie, YearFrom: 1990, Genres: []string{"Drama"}},
		{Type: core.TypeMovie, IncludeKeys: []core.RatingKey{}},
		{Type: core.TypeShow, IncludeKeys: []core.RatingKey{keys[150], keys[151]}},
		{Type: core.TypeMovie, DigitalDiscovery: true, DigitalDateEvidence: true},
		{Type: core.TypeMovie, Countries: []string{"US", "GB"}},
		{Type: core.TypeMovie, Countries: []string{"US"}, Genres: []string{"Drama"}},
		{Type: core.TypeMovie, NameStartsWith: "al"},
		{NameStartsWithOrGreater: "ma"},
		{Type: core.TypeMovie, NameStartsWith: "B", Genres: []string{"Drama"}},
		{Type: core.TypeMovie, NameStartsWith: "z", NameLessThan: "b"},
	}
	configs := []config.Catalog{
		{},
		{ShowUpcomingMovies: true},
		{ShowUpcomingMovies: true, MinPopularity: 5, MinVotes: 10},
		{ReleaseMode: config.ModeDigital},
		{ReleaseMode: config.ModeDigital, ShowUpcomingMovies: true},
		{Exclusions: []core.LibraryExclusion{{MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "genre", Op: "in", Value: json.RawMessage(`["Horror"]`)}}}}},
	}
	tallied, paged := 0, 0
	check := func(stage string) {
		t.Helper()
		for ci, cfg := range configs {
			c.UpdateConfig(cfg)
			for fi, f := range filters {
				ff := c.viewerPaging(ctx, f)
				where, args, err := c.browseWhere(ff)
				if err != nil {
					t.Fatal(err)
				}
				var exact int
				if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM catalog_titles t WHERE `+where, args...).Scan(&exact); err != nil {
					t.Fatal(err)
				}
				got, ok, err := c.tallyTotal(ctx, ff, where, args)
				if err != nil {
					t.Fatalf("%s config %d filter %d: %v", stage, ci, fi, err)
				}
				if ok {
					tallied++
					if got != exact {
						t.Errorf("%s config %d filter %d: tally total %d, titles %d", stage, ci, fi, got, exact)
					}
				}
				// The jump bar and filter lists from their tallies are the
				// direct counts, in the same order.
				for facet := range tallyFacets {
					got, ok, err := c.facetFromTally(ctx, ff, facet, where, args)
					if err != nil {
						t.Fatalf("%s config %d filter %d: %s: %v", stage, ci, fi, facet, err)
					}
					if !ok {
						continue
					}
					tallied++
					q, err := c.facetCountSQL(facet, where)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := db.QueryContext(ctx, q, args...)
					if err != nil {
						t.Fatal(err)
					}
					var want []string
					for rows.Next() {
						var k string
						var n int
						if err := rows.Scan(&k, &n); err != nil {
							t.Fatal(err)
						}
						want = append(want, fmt.Sprint(k, "=", n))
					}
					rows.Close()
					var have []string
					for _, v := range got {
						have = append(have, fmt.Sprint(v.Key, "=", v.Count))
					}
					if fmt.Sprint(have) != fmt.Sprint(want) {
						t.Errorf("%s config %d filter %d: %s from the tally %v, titles %v", stage, ci, fi, facet, have, want)
					}
				}
				// The most-counted studios, cut short, are the direct count's
				// first ones.
				if s, ok := c.tallyShape(ff); ok && s.genre == "" && s.country == "" && !s.years && s.first == nil {
					for _, limit := range []int{1, 3, 6} {
						got, ok, err := c.topFromPresence(ctx, s, ff, "studio", where, args, limit)
						if err != nil {
							t.Fatal(err)
						}
						if !ok {
							break
						}
						paged++
						q, _ := c.facetCountSQL("studio", where)
						rows, err := db.QueryContext(ctx, q, args...)
						if err != nil {
							t.Fatal(err)
						}
						var want, have []string
						for rows.Next() {
							var k string
							var n int
							if err := rows.Scan(&k, &n); err != nil {
								t.Fatal(err)
							}
							if len(want) < limit {
								want = append(want, fmt.Sprint(k, "=", n))
							}
						}
						rows.Close()
						for _, v := range got {
							have = append(have, fmt.Sprint(v.Key, "=", v.Count))
						}
						if fmt.Sprint(have) != fmt.Sprint(want) {
							t.Errorf("%s config %d filter %d: top %d studios %v, titles %v", stage, ci, fi, limit, have, want)
						}
					}
				}
				// A studio page and its total are the direct count's, in
				// byte order, for every window.
				if got, total, ok, err := c.FacetPage(ctx, f, "studioAll", 0, 1000); err != nil {
					t.Fatalf("%s config %d filter %d: studio page: %v", stage, ci, fi, err)
				} else if ok {
					tallied++
					paged++
					q, err := c.facetCountSQL("studioAll", where)
					if err != nil {
						t.Fatal(err)
					}
					rows, err := db.QueryContext(ctx, q, args...)
					if err != nil {
						t.Fatal(err)
					}
					var want []string
					for rows.Next() {
						var k string
						var n int
						if err := rows.Scan(&k, &n); err != nil {
							t.Fatal(err)
						}
						want = append(want, fmt.Sprint(k, "=", n))
					}
					rows.Close()
					sort.Strings(want)
					for _, w := range [][2]int{{0, 1000}, {0, 1}, {0, 3}, {2, 3}, {4, 2}, {len(want) - 1, 5}, {len(want) + 2, 5}, {1, 0}} {
						if w != [2]int{0, 1000} {
							if got, total, ok, err = c.FacetPage(ctx, f, "studioAll", w[0], w[1]); err != nil || !ok {
								t.Fatalf("%s config %d filter %d: studio page %v: %v %v", stage, ci, fi, w, ok, err)
							}
						}
						var have []string
						for _, v := range got {
							have = append(have, fmt.Sprint(v.Key, "=", v.Count))
						}
						lo, hi := min(max(w[0], 0), len(want)), min(max(w[0], 0)+w[1], len(want))
						if total != len(want) || fmt.Sprint(have) != fmt.Sprint(want[lo:hi]) {
							t.Errorf("%s config %d filter %d: studio page %v %v of %d, titles %v of %d", stage, ci, fi, w, have, total, want[lo:hi], len(want))
						}
					}
				}
				page, err := c.Browse(ctx, core.BrowseQuery{Filter: f, Sort: core.SortTitle, Limit: 500})
				if err != nil {
					t.Fatal(err)
				}
				if page.Total != exact || len(page.Items) != exact {
					t.Errorf("%s config %d filter %d: total %d, page %d, titles %d", stage, ci, fi, page.Total, len(page.Items), exact)
				}
			}
		}
	}
	// The trigger-kept presence and its counts are what a rebuild counts.
	presence := func(stage string) {
		t.Helper()
		dump := func(q string) string {
			rows, err := db.QueryContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var a, b string
				var x, y int64
				if err := rows.Scan(&a, &b, &x, &y); err != nil {
					t.Fatal(err)
				}
				out = append(out, fmt.Sprint(a, b, x, y))
			}
			return fmt.Sprint(out)
		}
		live := dump(`SELECT type||facet, value, ranks, n FROM catalog_facet_presence WHERE ranks<>0 ORDER BY 1,2`) + dump(`SELECT type, facet, lo*100+hi, n FROM catalog_facet_counts WHERE n<>0 ORDER BY 1,2,3`)
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		for _, q := range strings.Split(strings.TrimSpace(presenceRebuild()), ";") {
			if q = strings.TrimSpace(q); q != "" {
				if _, err := tx.ExecContext(ctx, q); err != nil {
					t.Fatal(err)
				}
			}
		}
		read := func(q string) string {
			rows, err := tx.QueryContext(ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var out []string
			for rows.Next() {
				var a, b string
				var x, y int64
				if err := rows.Scan(&a, &b, &x, &y); err != nil {
					t.Fatal(err)
				}
				out = append(out, fmt.Sprint(a, b, x, y))
			}
			return fmt.Sprint(out)
		}
		rebuilt := read(`SELECT type||facet, value, ranks, n FROM catalog_facet_presence WHERE ranks<>0 ORDER BY 1,2`) + read(`SELECT type, facet, lo*100+hi, n FROM catalog_facet_counts WHERE n<>0 ORDER BY 1,2,3`)
		if live != rebuilt {
			t.Errorf("%s: presence kept %s, rebuilt %s", stage, live, rebuilt)
		}
	}
	presence("initial")
	check("initial")
	if tallied == 0 || paged == 0 {
		t.Fatalf("no total came from the tally (%d), no studio page from the presence (%d)", tallied, paged)
	}

	// Membership changes through every column and both tables.
	for i, k := range keys {
		switch i % 7 {
		case 0:
			exec(`UPDATE catalog_titles SET browsable=1-browsable WHERE key=?`, int64(k))
		case 1:
			exec(`UPDATE catalog_titles SET content_rating_rank=?, content_rating='R' WHERE key=?`, int(contentrating.LevelOf("R")), int64(k))
		case 2:
			setGenres(k)
		case 3:
			exec(`UPDATE catalog_titles SET released_ts=? WHERE key=?`, released(), int64(k))
		case 4:
			exec(`UPDATE catalog_titles SET adult=1-adult WHERE key=?`, int64(k))
		case 5:
			exec(`UPDATE catalog_genres SET genre='Drama' WHERE key=? AND genre='Comedy' AND NOT EXISTS (SELECT 1 FROM catalog_genres d WHERE d.key=catalog_genres.key AND d.genre='Drama')`, int64(k))
			exec(`UPDATE catalog_countries SET country='US' WHERE key=? AND country='GB' AND NOT EXISTS (SELECT 1 FROM catalog_countries d WHERE d.key=catalog_countries.key AND d.country='US')`, int64(k))
		case 6:
			exec(`UPDATE catalog_titles SET sort_title=?, year=year+1 WHERE key=?`, sortTitle(k), int64(k))
			exec(`UPDATE catalog_studios SET studio='Banner' WHERE key=? AND studio='Acme' AND NOT EXISTS (SELECT 1 FROM catalog_studios d WHERE d.key=catalog_studios.key AND d.studio='Banner')`, int64(k))
		}
	}
	exec(`DELETE FROM catalog_titles WHERE key IN (?,?)`, int64(keys[10]), int64(keys[160]))
	exec(`DELETE FROM catalog_genres WHERE key=?`, int64(keys[11]))
	exec(`DELETE FROM catalog_countries WHERE key=?`, int64(keys[12]))
	// A title written through the catalogue's own upsert.
	if err := c.UpsertTitle(ctx, core.Title{Key: core.MovieKey(500), Type: core.TypeMovie, Title: "New", Released: now.Add(-time.Hour * 100), ContentRating: "PG", Genres: []string{"Drama", "Kids"}}, true); err != nil {
		t.Fatal(err)
	}
	check("after writes")

	if _, err := c.foldLibraryTallies(ctx); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM catalog_browse_tally_delta)+(SELECT COUNT(*) FROM catalog_facet_tally_delta)`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending deltas after a fold: %d %v", pending, err)
	}
	presence("after a fold")
	check("after a fold")

	// Titles due in the coming days are released as the clock moves.
	now = now.Add(30 * time.Hour)
	check("a day later")
	// Folds over a folded tally: studios appear, vanish and change ranks.
	for i, k := range keys {
		switch i % 3 {
		case 0:
			setGenres(k)
		case 1:
			exec(`UPDATE catalog_titles SET content_rating_rank=?, content_rating='PG' WHERE key=?`, int(contentrating.LevelOf("PG")), int64(k))
		}
	}
	exec(`DELETE FROM catalog_studios WHERE studio='Zeta'`)
	if _, err := c.foldLibraryTallies(ctx); err != nil {
		t.Fatal(err)
	}
	presence("a second fold")
	check("a second fold")
	exec(`UPDATE catalog_titles SET browsable=1 WHERE key=?`, int64(keys[20]))
	if err := RebuildLibraryCounts(ctx, db); err != nil {
		t.Fatal(err)
	}
	check("after a rebuild")
}

// A restricted viewer's whole-library episode total (Plex allLeaves) must be
// the number of episodes its pages return: the series rating histograms
// stand in for a join over every episode, so they are pinned to it through
// episode and series writes, inherited ratings and the clock.
func TestRestrictedEpisodeTotalsMatchPages(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	c, err := New(db, nil, config.Catalog{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	c.SetNow(func() time.Time { return now })
	rng := rand.New(rand.NewPCG(3, 5))
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`CREATE TABLE IF NOT EXISTS availability_hidden(kind TEXT NOT NULL,generation TEXT NOT NULL,rating_key BIGINT NOT NULL,created_at BIGINT NOT NULL,PRIMARY KEY(kind,generation,rating_key))`)
	ratings := []string{"", "TV-Y", "TV-PG", "TV-14", "TV-MA", "NR", "PG-13"}
	collections := []string{"", "", "Hidden", " hidden ", "Other"}
	released := func() int64 {
		switch rng.IntN(6) {
		case 0:
			return 0
		case 1:
			return now.Add(time.Duration(rng.IntN(72)) * time.Hour).Unix()
		default:
			return now.Add(-time.Duration(rng.IntN(20_000)) * time.Hour).Unix()
		}
	}
	rate := func() (string, int) {
		r := ratings[rng.IntN(len(ratings))]
		return r, int(contentrating.LevelOf(r))
	}
	var episodes []core.RatingKey
	for s := int64(1); s <= 25; s++ {
		r, rank := rate()
		exec(`INSERT INTO catalog_titles(key,type,title,sort_title,released_ts,browsable,adult,detail,content_rating,content_rating_rank,collection_name) VALUES(?,'show','s','s',?,1,0,1,?,?,?)`,
			int64(core.ShowKey(s)), now.Add(-time.Hour*24*400).Unix(), r, rank, collections[rng.IntN(len(collections))])
		for e := 1; e <= 8; e++ {
			key := core.EpisodeKey(s, 1+e/4, e)
			r, rank := rate()
			exec(`INSERT INTO catalog_titles(key,type,title,sort_title,show_key,season,episode,released_ts,browsable,adult,detail,content_rating,content_rating_rank,collection_name) VALUES(?,'episode','e','e',?,?,?,?,0,?,1,?,?,?)`,
				int64(key), int64(core.ShowKey(s)), 1+e/4, e, released(), flagInt(rng.IntN(12) == 0), r, rank, collections[rng.IntN(len(collections))])
			episodes = append(episodes, key)
			if rng.IntN(6) == 0 {
				exec(`INSERT INTO availability_hidden(kind,generation,rating_key,created_at) VALUES('episode','g-ep',?,0)`, int64(key))
			}
		}
	}
	share := core.Filter{MaxContentRating: "TV-14", ExcludeContentRatings: []string{"TV-MA", "NR"}, ExcludeCollectionNames: []string{"hidden"}}
	filters := []core.Filter{
		{},
		{ChildVisibility: &core.Filter{MaxContentRating: "TV-14"}, MaxContentRating: "TV-14"},
		{ChildVisibility: &share, MaxContentRating: "TV-14", ExcludeCollectionNames: []string{"hidden"}, HiddenEpisodeGeneration: "g-ep", ExcludeKeys: []core.RatingKey{episodes[3], episodes[40]}},
		{ChildVisibility: &core.Filter{MinContentRating: "TV-PG", ContentRatings: []string{"TV-PG", "TV-14", "NR"}, IncludeCollectionNames: []string{"other"}}},
		{ChildVisibility: &core.Filter{MaxContentRating: "XYZ"}},
	}
	check := func(stage string) {
		t.Helper()
		c.writeGen.Add(1)
		for fi, f := range filters {
			ff := c.viewerPaging(ctx, f)
			if _, ok, err := c.countStoredEpisodesBySeries(ctx, ff); err != nil || !ok {
				t.Fatalf("%s filter %d: not counted by series (%v)", stage, fi, err)
			}
			total, err := c.CountStoredEpisodes(ctx, f)
			if err != nil {
				t.Fatal(err)
			}
			page, err := c.StoredEpisodesByKey(ctx, f, 0, 10_000)
			if err != nil {
				t.Fatal(err)
			}
			if total != len(page) {
				t.Errorf("%s filter %d: total %d, episodes %d", stage, fi, total, len(page))
			}
		}
	}
	check("initial")
	for i, k := range episodes {
		switch i % 6 {
		case 0:
			r, rank := rate()
			exec(`UPDATE catalog_titles SET content_rating=?, content_rating_rank=? WHERE key=?`, r, rank, int64(k))
		case 1:
			exec(`UPDATE catalog_titles SET collection_name=? WHERE key=?`, collections[rng.IntN(len(collections))], int64(k))
		case 2:
			exec(`UPDATE catalog_titles SET released_ts=? WHERE key=?`, released(), int64(k))
		case 3:
			exec(`UPDATE catalog_titles SET adult=1-adult WHERE key=?`, int64(k))
		case 4:
			exec(`UPDATE catalog_titles SET show_key=? WHERE key=?`, int64(core.ShowKey(int64(1+rng.IntN(25)))), int64(k))
		}
	}
	exec(`DELETE FROM catalog_titles WHERE key IN (?,?)`, int64(episodes[7]), int64(episodes[70]))
	r, rank := rate()
	exec(`UPDATE catalog_titles SET content_rating=?, content_rating_rank=? WHERE key=?`, r, rank, int64(core.ShowKey(2)))
	check("after writes")
	now = now.Add(48 * time.Hour)
	check("two days later")
	if err := RebuildLibraryCounts(ctx, db); err != nil {
		t.Fatal(err)
	}
	check("after a rebuild")
}
