package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

// A candidate search (core.SearchCandidates) ranks and filters the same
// window as a full search, so it answers with the same titles in the same
// order; read in full by SearchDetails, its partial titles are the full
// search's. Its window reads only the facts a rule or the release gate
// decides by, and the caller's own (Facts) for what it returns.
func TestSearchCandidatesMatchFullSearch(t *testing.T) {
	for _, indexed := range []bool{true, false} {
		t.Run(fmt.Sprintf("indexed=%t", indexed), func(t *testing.T) { testSearchCandidates(t, indexed) })
	}
}

func testSearchCandidates(t *testing.T, indexed bool) {
	c := newChunkTestCatalog(t)
	ctx := context.Background()
	var titles []core.Title
	for i := int64(1); i <= 60; i++ {
		typ, key := core.TypeMovie, core.MovieKey(i)
		if i%4 == 0 {
			typ, key = core.TypeShow, core.ShowKey(i)
		}
		country := "US"
		if i%3 == 0 {
			country = "KR"
		}
		title := core.Title{Key: key, Type: typ, IDs: core.IDs{TMDB: i}, Title: fmt.Sprintf("Star %d", i), Released: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC), Year: 2000,
			Popularity: float64(200 - i), VoteCount: 1000, Genres: []string{"Drama", "Science Fiction"}, Keywords: []string{"space"}, Studios: []string{"Studio"},
			Countries: []string{country}, AlternativeTitles: []string{fmt.Sprintf("Stern %d", i)}}
		for n := int64(1); n <= 5; n++ {
			title.Cast = append(title.Cast, core.Person{TMDB: n, Name: fmt.Sprintf("Actor %d", n)})
		}
		titles = append(titles, title)
	}
	if err := c.upsertMany(ctx, titles, upsertOpts{browsable: true, detail: true}); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		// Before the title index's first build, the literal match answers.
		c.searchIx.next = c.now().Add(time.Hour)
	} else if c.searchIndex(ctx, true) == nil {
		t.Fatal("index unavailable")
	}
	hideKR := []core.LibraryExclusion{{MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: "country", Op: "is", Value: json.RawMessage(`"KR"`)}}}}
	all := []core.MediaType{core.TypeMovie, core.TypeShow, core.TypeSeason, core.TypeEpisode}
	for _, tc := range []struct {
		name  string
		cfg   config.Catalog
		types []core.MediaType
		// window is the facts a candidate search's window reads.
		window core.TitleFacts
	}{
		{"open", config.Catalog{}, nil, 0},
		{"open with episodes", config.Catalog{}, all, 0},
		{"country rule", config.Catalog{Exclusions: hideKR}, all, core.FactCountries},
		{"digital release gate", config.Catalog{ReleaseMode: config.ModeDigital}, nil, core.FactHomeRelease},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c.UpdateConfig(tc.cfg)
			setTestPolicies(t, c, tc.cfg.Exclusions, tc.cfg.VisibilityDefault)
			if got := c.policyFacts(c.visibilityFor(ctx)); got != tc.window {
				t.Fatalf("window facts %b, want %b", got, tc.window)
			}
			full, err := c.Search(ctx, "star", tc.types, 20)
			if err != nil || len(full) == 0 {
				t.Fatalf("full search: %d %v", len(full), err)
			}
			c.cacheMu.Lock()
			c.titleCache = nil
			c.cacheMu.Unlock()
			first := full[0].Key
			cand := &core.SearchCandidates{Facts: core.FactGenres, Detail: func(t core.Title) bool { return t.Key == first }}
			candCtx := core.WithSearchCandidates(ctx, cand)
			measured, stats := store.WithQueryStats(candCtx)
			got, err := c.Search(measured, "star", tc.types, 20)
			if err != nil || len(got) != len(full) {
				t.Fatalf("candidate search: %d titles, want %d: %v", len(got), len(full), err)
			}
			// 60 rows, plus one fact per candidate when the window reads one,
			// genres (two each) for the 19 partial titles returned and the
			// one Detail title read in full.
			if limit := 60 + 60*bitsOf(tc.window) + 2*19 + 20; stats.Rows() > int64(limit) {
				t.Fatalf("candidate search read %d rows, want at most %d", stats.Rows(), limit)
			}
			for i := range got {
				if got[i].Key != full[i].Key || got[i].SearchScore != full[i].SearchScore {
					t.Fatalf("candidate %d is %v (%v), full search has %v (%v)", i, got[i].Key, got[i].SearchScore, full[i].Key, full[i].SearchScore)
				}
				partial := cand.Partial(got[i].Key)
				if partial == (got[i].Key == first) {
					t.Fatalf("candidate %d partial=%t", i, partial)
				}
				if partial && (len(got[i].Cast) != 0 || !reflect.DeepEqual(got[i].Genres, full[i].Genres)) {
					t.Fatalf("partial candidate %d: cast %d, genres %v", i, len(got[i].Cast), got[i].Genres)
				}
			}
			// Shown as list items (a Plex hub), the rows gain the list's facts.
			// (Before the title index's first build, a full search returns
			// the literal match's titles in full; a candidate search reads
			// them as list items as it does once the index is built.)
			listCtx := core.WithSearchListFacts(ctx)
			wantList, err := c.Search(listCtx, "star", tc.types, 20)
			if err != nil {
				t.Fatal(err)
			}
			listCand := &core.SearchCandidates{Facts: core.FactGenres, Detail: cand.Detail}
			listCandCtx := core.WithSearchCandidates(listCtx, listCand)
			gotList, err := c.Search(listCandCtx, "star", tc.types, 20)
			if err != nil {
				t.Fatal(err)
			}
			if gotList, err = c.SearchListDetails(listCandCtx, gotList); err != nil {
				t.Fatal(err)
			}
			for i := range wantList {
				if indexed && listCand.Partial(wantList[i].Key) && !reflect.DeepEqual(gotList[i], wantList[i]) {
					t.Fatalf("list detail %d:\n%+v\nwant\n%+v", i, gotList[i], wantList[i])
				}
			}
			details, err := c.SearchDetails(candCtx, got)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(details, full) {
				for i := range details {
					if !reflect.DeepEqual(details[i], full[i]) {
						t.Fatalf("detail %d:\n%+v\nwant\n%+v", i, details[i], full[i])
					}
				}
			}
		})
	}
}

func bitsOf(f core.TitleFacts) int {
	n := 0
	for ; f != 0; f &= f - 1 {
		n++
	}
	return n
}
