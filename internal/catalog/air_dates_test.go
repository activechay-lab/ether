package catalog

import (
	"context"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

func TestMatchingAirDateShowsAndIndexPlan(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	c := newChunkTestCatalog(t)
	c.SetNow(func() time.Time { return now })

	day := func(v string) time.Time {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			t.Fatal(err)
		}
		return d.UTC()
	}

	// 1. Returning show warmed via storeShowDetails (season summaries + last_air_ts, no episode rows, plus future season 5).
	s1Detail := &tmdb.TVDetails{
		TVShow:      tmdb.TVShow{ID: 701, Name: "Returning Sci-Fi", FirstAirDate: "2019-03-01", VoteCount: 1200, Popularity: 80},
		Status:      "Returning Series",
		LastAirDate: "2026-10-25",
		Seasons: []tmdb.SeasonSummary{
			{SeasonNumber: 0, EpisodeCount: 3, AirDate: "2026-10-27"}, // specials ignored
			{SeasonNumber: 1, EpisodeCount: 8, AirDate: "2019-03-01"},
			{SeasonNumber: 4, EpisodeCount: 8, AirDate: "2026-10-08"},
			{SeasonNumber: 5, EpisodeCount: 8, AirDate: "2026-12-01"}, // future season ignored
		},
	}
	if err := c.storeShowDetails(ctx, c.tvDetailsToTitle(ctx, s1Detail), s1Detail); err != nil {
		t.Fatal(err)
	}

	// 2. Show with mirrored episode rows: Season 2 premiered 80 days ago, latest episode 5 days ago.
	showEp := core.ShowKey(702)
	if err := c.upsertMany(ctx, []core.Title{
		{Key: showEp, Type: core.TypeShow, Title: "Midseason Runner", Released: day("2021-01-10"), VoteCount: 800, Popularity: 60},
		{Key: core.EpisodeKey(702, 1, 1), Type: core.TypeEpisode, ShowKey: showEp, Season: 1, Episode: 1, Title: "Pilot", Released: day("2021-01-10")},
		{Key: core.EpisodeKey(702, 2, 1), Type: core.TypeEpisode, ShowKey: showEp, Season: 2, Episode: 1, Title: "S2E1", Released: day("2026-08-09")},
		{Key: core.EpisodeKey(702, 2, 6), Type: core.TypeEpisode, ShowKey: showEp, Season: 2, Episode: 6, Title: "S2E6", Released: day("2026-10-23")},
		{Key: core.EpisodeKey(702, 2, 7), Type: core.TypeEpisode, ShowKey: showEp, Season: 2, Episode: 7, Title: "S2E7 Future", Released: day("2026-11-05")},
	}, upsertOpts{browsable: true, detail: true}); err != nil {
		t.Fatal(err)
	}

	// 3. Ended show from 2018 with a recent Season 0 special: must not match recent regular activity.
	showSpecial := core.ShowKey(703)
	if err := c.upsertMany(ctx, []core.Title{
		{Key: showSpecial, Type: core.TypeShow, Title: "Ended With Special", Released: day("2018-04-01"), LastAirDate: day("2018-06-01"), VoteCount: 900, Popularity: 50},
		{Key: core.SeasonKey(703, 1), Type: core.TypeSeason, ShowKey: showSpecial, Season: 1, ChildCount: 8, Released: day("2018-04-01")},
		{Key: core.SeasonKey(703, 0), Type: core.TypeSeason, ShowKey: showSpecial, Season: 0, ChildCount: 1, Released: day("2026-10-26")},
		{Key: core.EpisodeKey(703, 0, 1), Type: core.TypeEpisode, ShowKey: showSpecial, Season: 0, Episode: 1, Title: "Reunion Special", Released: day("2026-10-26")},
	}, upsertOpts{browsable: true, detail: true}); err != nil {
		t.Fatal(err)
	}

	// 4. Higher aired season with undated premiere: must not borrow Season 1's premiere.
	showUndatedPrem := core.ShowKey(704)
	if err := c.upsertMany(ctx, []core.Title{
		{Key: showUndatedPrem, Type: core.TypeShow, Title: "Undated S2 Premiere", Released: day("2026-09-15"), VoteCount: 500, Popularity: 45},
		{Key: core.EpisodeKey(704, 1, 1), Type: core.TypeEpisode, ShowKey: showUndatedPrem, Season: 1, Episode: 1, Title: "S1E1", Released: day("2026-09-15")},
		{Key: core.EpisodeKey(704, 2, 1), Type: core.TypeEpisode, ShowKey: showUndatedPrem, Season: 2, Episode: 1, Title: "S2E1"},
		{Key: core.EpisodeKey(704, 2, 2), Type: core.TypeEpisode, ShowKey: showUndatedPrem, Season: 2, Episode: 2, Title: "S2E2", Released: day("2026-10-24")},
	}, upsertOpts{browsable: true, detail: true}); err != nil {
		t.Fatal(err)
	}

	// 5. Brand-new undetailed summary show (first aired 8 days ago).
	showBrandNew := core.ShowKey(705)
	if err := c.UpsertTitle(ctx, core.Title{
		Key: showBrandNew, Type: core.TypeShow, Title: "Brand New Summary", Released: day("2026-10-20"), VoteCount: 300, Popularity: 70,
	}, true); err != nil {
		t.Fatal(err)
	}

	// 6. Pre-1970 classic show (negative Unix timestamps).
	sClassic := &tmdb.TVDetails{
		TVShow:      tmdb.TVShow{ID: 706, Name: "Sixties Serial", FirstAirDate: "1963-11-23", VoteCount: 1500, Popularity: 55},
		Status:      "Ended",
		LastAirDate: "1965-05-01",
		Seasons: []tmdb.SeasonSummary{
			{SeasonNumber: 1, EpisodeCount: 42, AirDate: "1963-11-23"},
			{SeasonNumber: 2, EpisodeCount: 39, AirDate: "1964-10-31"},
		},
	}
	if err := c.storeShowDetails(ctx, c.tvDetailsToTitle(ctx, sClassic), sClassic); err != nil {
		t.Fatal(err)
	}

	m := NewMirror(c.db, func() time.Time { return now })
	keysList := func(set map[core.RatingKey]bool) []core.RatingKey {
		out := make([]core.RatingKey, 0, len(set))
		for k := range set {
			out = append(out, k)
		}
		sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
		return out
	}

	cases := []struct {
		name string
		q    AirDateQuery
		want []core.RatingKey
	}{
		{
			name: "latestEpisode within 14d",
			q:    AirDateQuery{Now: now, Premiere: false, HasLo: true, Lo: now.Add(-14 * 24 * time.Hour).Unix(), HasHi: true, Hi: now.Unix()},
			want: []core.RatingKey{core.ShowKey(701), showEp, showUndatedPrem, showBrandNew},
		},
		{
			name: "latestSeasonPremiere within 30d",
			q:    AirDateQuery{Now: now, Premiere: true, HasLo: true, Lo: now.Add(-30 * 24 * time.Hour).Unix(), HasHi: true, Hi: now.Unix()},
			want: []core.RatingKey{core.ShowKey(701), showBrandNew},
		},
		{
			name: "latestSeasonPremiere within 90d includes Midseason Runner but not Undated S2 Premiere",
			q:    AirDateQuery{Now: now, Premiere: true, HasLo: true, Lo: now.Add(-90 * 24 * time.Hour).Unix(), HasHi: true, Hi: now.Unix()},
			want: []core.RatingKey{core.ShowKey(701), showEp, showBrandNew},
		},
		{
			name: "latestEpisode lte 2018-12-31 excludes returning shows",
			q:    AirDateQuery{Now: now, Premiere: false, HasHi: true, Hi: day("2018-12-31").Add(24*time.Hour - time.Second).Unix()},
			want: []core.RatingKey{showSpecial, core.ShowKey(706)},
		},
		{
			name: "latestSeasonPremiere between 1964",
			q:    AirDateQuery{Now: now, Premiere: true, HasLo: true, Lo: day("1964-01-01").Unix(), HasHi: true, Hi: day("1964-12-31").Add(24*time.Hour - time.Second).Unix()},
			want: []core.RatingKey{core.ShowKey(706)},
		},
		{
			name: "latestEpisode between 1965",
			q:    AirDateQuery{Now: now, Premiere: false, HasLo: true, Lo: day("1965-01-01").Unix(), HasHi: true, Hi: day("1965-12-31").Add(24*time.Hour - time.Second).Unix()},
			want: []core.RatingKey{core.ShowKey(706)},
		},
	}
	for _, tc := range cases {
		got, err := m.MatchingAirDateShows(ctx, tc.q)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if list := keysList(got); !reflect.DeepEqual(list, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, list, tc.want)
		}
	}

	// Verify SQLite query plans use range seeks on existing indexes and never scan catalog_titles.
	cutoff := AirDateQuery{Now: now}.Cutoff()
	lo := now.Add(-30 * 24 * time.Hour).Unix()
	plans := []struct {
		name  string
		query string
		args  []any
		want  []string
	}{
		{
			name: "season premiere range",
			query: `EXPLAIN QUERY PLAN SELECT DISTINCT cand.show_key FROM (
				SELECT show_key, season FROM catalog_titles WHERE type = 'season' AND release_known = 1 AND adult = 0 AND season > 0 AND child_count > 0 AND released_ts >= ? AND released_ts <= ?
				UNION ALL
				SELECT e.show_key, e.season FROM catalog_titles e WHERE e.type = 'episode' AND e.release_known = 1 AND e.season > 0 AND e.episode = 1 AND e.released_ts >= ? AND e.released_ts <= ?
					AND NOT EXISTS (SELECT 1 FROM catalog_titles sn WHERE sn.type = 'season' AND sn.show_key = e.show_key AND sn.season = e.season AND sn.child_count > 0 AND sn.release_known = 1 AND sn.released_ts < ?)
			) cand
			WHERE NOT EXISTS (SELECT 1 FROM catalog_titles s2 WHERE s2.type = 'season' AND s2.show_key = cand.show_key AND s2.season > cand.season AND s2.child_count > 0 AND s2.release_known = 1 AND s2.released_ts < ?)
			AND NOT EXISTS (SELECT 1 FROM catalog_titles e2 WHERE e2.type = 'episode' AND e2.show_key = cand.show_key AND e2.season > cand.season AND e2.episode > 0 AND e2.release_known = 1 AND e2.released_ts < ?)`,
			args: []any{lo, cutoff - 1, lo, cutoff - 1, cutoff, cutoff, cutoff},
			want: []string{"released_ts>? AND released_ts<?", "catalog_titles_show_seasons", "catalog_titles_season_episode_aired"},
		},
		{
			name: "latest episode range",
			query: `EXPLAIN QUERY PLAN SELECT show_key FROM (
				SELECT key AS show_key, last_air_ts AS ts FROM catalog_titles WHERE type = 'show' AND adult = 0 AND last_air_ts <> 0 AND last_air_ts >= ? AND last_air_ts < ?
				UNION ALL
				SELECT show_key, released_ts AS ts FROM catalog_titles WHERE type = 'episode' AND release_known = 1 AND season > 0 AND episode > 0 AND released_ts >= ? AND released_ts < ?
				UNION ALL
				SELECT show_key, released_ts AS ts FROM catalog_titles WHERE type = 'season' AND release_known = 1 AND adult = 0 AND season > 0 AND child_count > 0 AND released_ts >= ? AND released_ts < ?
			) u GROUP BY show_key`,
			args: []any{lo, cutoff, lo, cutoff, lo, cutoff},
			want: []string{"SEARCH catalog_titles USING INDEX", "released_ts>? AND released_ts<?"},
		},
	}
	for _, p := range plans {
		rows, err := c.db.QueryContext(ctx, p.query, p.args...)
		if err != nil {
			t.Fatalf("%s explain: %v", p.name, err)
		}
		var details []string
		for rows.Next() {
			var id, parent, notused int
			var detail string
			if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			details = append(details, detail)
			if strings.HasPrefix(detail, "SCAN catalog_titles") {
				t.Errorf("%s performed catalog_titles scan instead of SEARCH: %s", p.name, detail)
			}
		}
		rows.Close()
		joined := strings.Join(details, "\n")
		for _, sub := range p.want {
			if !strings.Contains(joined, sub) {
				t.Errorf("%s plan missing %q:\n%s", p.name, sub, joined)
			}
		}
	}
}
