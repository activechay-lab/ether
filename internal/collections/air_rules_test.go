package collections_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/collections"
	"github.com/braedonsaunders/ether/internal/core"
)

func TestAirDateRulesLocalEvaluation(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, withOpts(collections.WithNow(func() time.Time { return now })))
	day := func(v string) time.Time {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			t.Fatal(err)
		}
		return d.UTC()
	}

	// Pin Fresh Series (508) to 20 days before `now` (2026-10-08) with detail=0, last_air_ts=0.
	if err := e.cat.UpsertTitle(e.ctx, core.Title{
		Key: core.ShowKey(508), Type: core.TypeShow, IDs: core.IDs{TMDB: 508},
		Title: "Fresh Series", Genres: []string{"Drama"}, Language: "en",
		Year: 2026, Released: day("2026-10-08"), VoteCount: 50, Rating: 7.4, Popularity: 90,
		PosterPath: "/p508.jpg", BackdropPath: "/b508.jpg",
	}, true); err != nil {
		t.Fatal(err)
	}

	// Seed rich TV air-date states into the local catalogue mirror:
	// - Chemistry Teacher (501): first aired 2008, Season 6 premiered 15 days ago (2026-10-13), latest episode 3 days ago (2026-10-25).
	// - Office Life (502): first aired 2005, Season 4 premiered 120 days ago (2026-06-30), latest episode 7 days ago (2026-10-21).
	// - Meltdown (503): ended in 2019 (Season 1 2019-05-06, last episode 2019-06-03), plus a recent Season 0 special (2026-10-26).
	rawDB := e.db.(*sql.DB)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := rawDB.ExecContext(e.ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE catalog_titles SET detail=1, last_air_ts=? WHERE key=?`, day("2026-10-25").Unix(), int64(core.ShowKey(501)))
	exec(`INSERT INTO catalog_titles(key,type,show_key,season,title,released_ts,child_count) VALUES (?,?,?,?,?,?,?)`,
		int64(core.SeasonKey(501, 1)), "season", int64(core.ShowKey(501)), 1, "Season 1", day("2008-01-20").Unix(), 7)
	exec(`INSERT INTO catalog_titles(key,type,show_key,season,title,released_ts,child_count) VALUES (?,?,?,?,?,?,?)`,
		int64(core.SeasonKey(501, 6)), "season", int64(core.ShowKey(501)), 6, "Season 6", day("2026-10-13").Unix(), 8)

	exec(`UPDATE catalog_titles SET detail=1, last_air_ts=? WHERE key=?`, day("2026-10-21").Unix(), int64(core.ShowKey(502)))
	exec(`INSERT INTO catalog_titles(key,type,show_key,season,title,released_ts,child_count) VALUES (?,?,?,?,?,?,?)`,
		int64(core.SeasonKey(502, 4)), "season", int64(core.ShowKey(502)), 4, "Season 4", day("2026-06-30").Unix(), 12)

	exec(`UPDATE catalog_titles SET detail=1, last_air_ts=? WHERE key=?`, day("2019-06-03").Unix(), int64(core.ShowKey(503)))
	exec(`INSERT INTO catalog_titles(key,type,show_key,season,title,released_ts,child_count) VALUES (?,?,?,?,?,?,?)`,
		int64(core.SeasonKey(503, 1)), "season", int64(core.ShowKey(503)), 1, "Season 1", day("2019-05-06").Unix(), 5)
	exec(`INSERT INTO catalog_titles(key,type,show_key,season,episode,title,released_ts) VALUES (?,?,?,?,?,?,?)`,
		int64(core.EpisodeKey(503, 0, 1)), "episode", int64(core.ShowKey(503)), 0, 1, "Special", day("2026-10-26").Unix())

	cases := []struct {
		name  string
		media string
		local bool
		sort  string
		rules []core.CollectionRule
		excl  []core.CollectionRule
		want  []string
	}{
		{
			name:  "catalog non-local popularity sort runs pure SQL with zero TMDB requests",
			media: "show",
			local: false,
			sort:  "popularity",
			rules: []core.CollectionRule{rl("latestSeasonPremiere", "within", "30d")},
			want:  []string{"Chemistry Teacher", "Fresh Series"},
		},
		{
			name:  "latestSeasonPremiere within 30d sorted by latestSeasonPremiere locally",
			media: "show",
			local: true,
			sort:  "latestSeasonPremiere",
			rules: []core.CollectionRule{rl("latestSeasonPremiere", "within", "30d")},
			want:  []string{"Chemistry Teacher", "Fresh Series"},
		},
		{
			name:  "latestEpisode within 14d includes midseason episode but excludes special",
			media: "show",
			local: true,
			sort:  "latestEpisode",
			rules: []core.CollectionRule{rl("latestEpisode", "within", "14d")},
			want:  []string{"Chemistry Teacher", "Office Life"},
		},
		{
			name:  "mixed media collection only matches shows on air-date rule",
			media: "mixed",
			local: true,
			sort:  "popularity",
			rules: []core.CollectionRule{rl("latestEpisode", "gte", "2026-10-20")},
			want:  []string{"Chemistry Teacher", "Office Life"},
		},
		{
			name:  "latestSeasonPremiere between summer window",
			media: "show",
			local: true,
			sort:  "popularity",
			rules: []core.CollectionRule{rl("latestSeasonPremiere", "between", []string{"2026-06-01", "2026-07-31"})},
			want:  []string{"Office Life"},
		},
		{
			name:  "latestEpisode lte 2020-01-01 matches ended show",
			media: "show",
			local: true,
			sort:  "popularity",
			rules: []core.CollectionRule{rl("latestEpisode", "lte", "2020-01-01")},
			want:  []string{"Meltdown"},
		},
		{
			name:  "exclude latestEpisode lte 2020-01-01 removes ended show",
			media: "show",
			local: true,
			sort:  "popularity",
			rules: []core.CollectionRule{rl("genre", "is", "Drama")},
			excl:  []core.CollectionRule{rl("latestEpisode", "lte", "2020-01-01")},
			want:  []string{"Chemistry Teacher", "Fresh Series", "Seoul Romance", "Nordic Bridge"},
		},
	}

	beforeReqs := len(e.tmdb.seen("/"))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sp := spec("tv-air", tc.media, []core.CollectionSource{{Kind: "catalog"}}, tc.rules...)
			sp.LocalOnly = tc.local
			sp.Sort = tc.sort
			sp.SortDesc = true
			sp.Exclude = tc.excl
			p, err := e.svc.Preview(e.ctx, sp, 50)
			if err != nil {
				t.Fatal(err)
			}
			if got := titlesOf(p); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %v want %v (warnings=%v)", got, tc.want, p.Warnings)
			}
		})
	}
	if gotReqs := len(e.tmdb.seen("/")) - beforeReqs; gotReqs != 0 {
		t.Fatalf("expected 0 TMDB requests for local SQL air-date evaluation, got %d (%v)", gotReqs, e.tmdb.seen("/"))
	}
}

type airRuleSpyTMDB struct {
	mu            sync.Mutex
	discoverCalls []url.Values
	detailCalls   map[string]int
}

func (s *airRuleSpyTMDB) GetJSON(_ context.Context, path string, q url.Values, out any) error {
	var data any
	switch {
	case path == "/genre/tv/list":
		data = map[string]any{"genres": []any{map[string]any{"id": 18, "name": "Drama"}}}
	case path == "/watch/providers/tv" || path == "/watch/providers/movie":
		data = map[string]any{"results": []any{map[string]any{"provider_id": 8, "provider_name": "Netflix"}}}
	case path == "/network/49":
		data = map[string]any{"id": 49, "name": "HBO"}
	case path == "/discover/tv":
		cp := url.Values{}
		for k, v := range q {
			cp[k] = append([]string(nil), v...)
		}
		s.mu.Lock()
		s.discoverCalls = append(s.discoverCalls, cp)
		s.mu.Unlock()
		// Return four shows whose episodes touched the discover air_date window:
		// 911: Season 3 premiered 2026-10-10, latest episode 2026-10-17 (true premiere + episode match).
		// 912: Season 2 premiered 2026-04-01, mid-season episode 2026-10-18 (episode match only, NOT season premiere).
		// 913: Season 1 premiered 2020-01-01, Season 0 special 2026-10-20 (neither regular episode nor premiere).
		// 914: Season 2 premiered 2026-10-05, newer episode 2026-10-26 (exceeds a 2026-10-20 upper bound).
		data = map[string]any{"page": 1, "total_pages": 1, "total_results": 4, "results": []any{
			map[string]any{"id": 911, "name": "New Season Hit", "first_air_date": "2021-01-01", "popularity": 95, "vote_count": 500, "genre_ids": []int{18}},
			map[string]any{"id": 912, "name": "Midseason Drama", "first_air_date": "2022-01-01", "popularity": 85, "vote_count": 500, "genre_ids": []int{18}},
			map[string]any{"id": 913, "name": "Holiday Special Only", "first_air_date": "2020-01-01", "popularity": 75, "vote_count": 500, "genre_ids": []int{18}},
			map[string]any{"id": 914, "name": "Still Airing Weekly", "first_air_date": "2023-01-01", "popularity": 65, "vote_count": 500, "genre_ids": []int{18}},
		}}
	case strings.HasPrefix(path, "/tv/") && strings.Contains(path, "/season/"):
		// Season 1 of 913 aired in 2020.
		data = map[string]any{"episodes": []any{
			map[string]any{"episode_number": 1, "air_date": "2020-01-01"},
			map[string]any{"episode_number": 2, "air_date": "2020-01-08"},
		}}
	case strings.HasPrefix(path, "/tv/"):
		s.mu.Lock()
		if s.detailCalls == nil {
			s.detailCalls = map[string]int{}
		}
		s.detailCalls[path]++
		s.mu.Unlock()
		switch path {
		case "/tv/911":
			data = map[string]any{
				"last_episode_to_air": map[string]any{"season_number": 3, "episode_number": 2, "air_date": "2026-10-17"},
				"seasons": []any{
					map[string]any{"season_number": 1, "episode_count": 8, "air_date": "2021-01-01"},
					map[string]any{"season_number": 3, "episode_count": 8, "air_date": "2026-10-10"},
				},
			}
		case "/tv/912":
			data = map[string]any{
				"last_episode_to_air": map[string]any{"season_number": 2, "episode_number": 12, "air_date": "2026-10-18"},
				"seasons":             []any{map[string]any{"season_number": 2, "episode_count": 12, "air_date": "2026-04-01"}},
			}
		case "/tv/913":
			data = map[string]any{
				"last_episode_to_air": map[string]any{"season_number": 0, "episode_number": 1, "air_date": "2026-10-20"},
				"seasons":             []any{map[string]any{"season_number": 1, "episode_count": 6, "air_date": "2020-01-01"}},
			}
		case "/tv/914":
			data = map[string]any{
				"last_episode_to_air": map[string]any{"season_number": 2, "episode_number": 4, "air_date": "2026-10-26"},
				"seasons":             []any{map[string]any{"season_number": 2, "episode_count": 8, "air_date": "2026-10-05"}},
			}
		default:
			return fmt.Errorf("unexpected %s", path)
		}
	default:
		return fmt.Errorf("unexpected %s", path)
	}
	b, _ := json.Marshal(data)
	return json.Unmarshal(b, out)
}

func TestAirDateRulesDiscoverPushdownAndLocalVerification(t *testing.T) {
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	spy := &airRuleSpyTMDB{}
	e := newEnv(t, withOpts(collections.WithTMDB(spy), collections.WithNow(func() time.Time { return now })))

	// 1. Streaming-service source + latestSeasonPremiere within 30d + Sort latestSeasonPremiere:
	// Pushes air_date.gte/lte to /discover/tv, hydrates cold shows once, and rejects mid-season (912) and special (913).
	sp := spec("New Seasons on Netflix", "show", []core.CollectionSource{{Kind: "watchProvider", ID: 8, Region: "US"}},
		rl("latestSeasonPremiere", "within", "30d"))
	sp.Sort = "latestSeasonPremiere"
	sp.SortDesc = true
	p, err := e.svc.Preview(e.ctx, sp, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := titlesOf(p); !reflect.DeepEqual(got, []string{"New Season Hit", "Still Airing Weekly"}) {
		t.Fatalf("premiere within 30d got %v want [New Season Hit Still Airing Weekly] (warnings=%v)", got, p.Warnings)
	}
	if len(spy.discoverCalls) != 1 {
		t.Fatalf("expected 1 discover call, got %d", len(spy.discoverCalls))
	}
	q := spy.discoverCalls[0]
	if q.Get("air_date.gte") != "2026-09-28" || q.Get("air_date.lte") != "2026-10-28" {
		t.Fatalf("discover air_date params = %v", q)
	}
	for _, k := range []string{"/tv/911", "/tv/912", "/tv/913", "/tv/914"} {
		if spy.detailCalls[k] != 1 {
			t.Fatalf("expected 1 detail fetch for %s (reused by sort), got %d", k, spy.detailCalls[k])
		}
	}

	// 2. Catalogue pushdown (network rule + latestEpisode between [2026-10-01, 2026-10-20]):
	// Pushes air_date.gte/lte alongside with_networks, and local evaluation rejects 913 (special) and 914 (newer 2026-10-26 episode).
	spy.discoverCalls = nil
	sp2 := spec("HBO Mid-October", "show", []core.CollectionSource{{Kind: "catalog"}},
		rl("network", "is", map[string]any{"id": 49, "name": "HBO"}),
		rl("latestEpisode", "between", []string{"2026-10-01", "2026-10-20"}))
	p2, err := e.svc.Preview(e.ctx, sp2, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := titlesOf(p2); !reflect.DeepEqual(got, []string{"New Season Hit", "Midseason Drama"}) {
		t.Fatalf("between pushdown got %v want [New Season Hit Midseason Drama]", got)
	}
	if len(spy.discoverCalls) != 1 || spy.discoverCalls[0].Get("with_networks") != "49" ||
		spy.discoverCalls[0].Get("air_date.gte") != "2026-10-01" || spy.discoverCalls[0].Get("air_date.lte") != "2026-10-20" {
		t.Fatalf("pushdown discover params = %v", spy.discoverCalls)
	}

	// 3. Watch-provider rule on catalog source + latestEpisode gte 2026-10-20:
	// Both pushdown and providerSet push air_date.gte/lte to /discover/tv.
	spy.discoverCalls = nil
	sp3 := spec("Streaming Recent", "show", []core.CollectionSource{{Kind: "catalog"}},
		rl("watchProvider", "is", map[string]any{"id": 8, "name": "Netflix", "region": "US"}),
		rl("latestEpisode", "gte", "2026-10-20"))
	p3, err := e.svc.Preview(e.ctx, sp3, 50)
	if err != nil {
		t.Fatal(err)
	}
	if got := titlesOf(p3); !reflect.DeepEqual(got, []string{"Still Airing Weekly"}) {
		t.Fatalf("provider rule got %v want [Still Airing Weekly]", got)
	}
	if len(spy.discoverCalls) == 0 {
		t.Fatalf("expected discover calls for watchProvider rule")
	}
	for i, dc := range spy.discoverCalls {
		if dc.Get("with_watch_providers") != "8" || dc.Get("air_date.gte") != "2026-10-20" || dc.Get("air_date.lte") != "2026-10-28" {
			t.Fatalf("discover call %d params = %v", i, dc)
		}
	}
}
