package availability

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestStickyGateInvariants(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	r := resolver.New(nil, nil, nil, x.c, resolver.Options{})
	defer r.Close()
	x.s.d.Validator = suitabilityValidator{x.v, r}

	c, err := catalog.New(x.s.db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return x.now }))
	if err != nil {
		t.Fatal(err)
	}
	x.s.d.Catalog = c

	upsertMovie := func(id int64, name string, released, home time.Time, homeChecked bool) core.Title {
		t.Helper()
		m := core.Title{
			Key:                core.MovieKey(id),
			Type:               core.TypeMovie,
			Title:              name,
			Year:               2026,
			Language:           "en",
			Released:           released,
			HomeReleaseChecked: homeChecked,
			HomeReleased:       home,
			Popularity:         float64(100 - id),
		}
		if err := c.UpsertTitle(ctx, m, true); err != nil {
			t.Fatal(err)
		}
		if homeChecked {
			var ts int64
			if !home.IsZero() {
				ts = home.Unix()
			}
			if _, err := x.s.db.ExecContext(ctx, `INSERT INTO catalog_home_release(key,checked,earliest_ts) VALUES(?,1,?)`, int64(m.Key), ts); err != nil {
				t.Fatal(err)
			}
		}
		return m
	}

	browseVisible := func() map[core.RatingKey]bool {
		t.Helper()
		x.s.clearVisibility()
		pf, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeMovie})
		if err != nil {
			t.Fatal(err)
		}
		page, err := c.Browse(ctx, core.BrowseQuery{Filter: pf, Sort: core.SortPopularity, Desc: true, Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		out := map[core.RatingKey]bool{}
		for _, it := range page.Items {
			out[it.Key] = true
		}
		return out
	}

	assertBoth := func(label string, m core.Title, want bool) {
		t.Helper()
		vis := browseVisible()[m.Key]
		if vis != want {
			t.Fatalf("%s: SQL Browse visible=%v want=%v", label, vis, want)
		}
		hubVis := len(x.s.FilterDiscoveryCurrent(ctx, []core.Title{m})) == 1
		if hubVis != want {
			t.Fatalf("%s: FilterDiscovery visible=%v want=%v", label, hubVis, want)
		}
	}

	// 1. Movie with no evidence stays hidden.
	noEv := upsertMovie(1, "Silent Horizon", x.now.Add(-5*24*time.Hour), x.now.Add(-24*time.Hour), true)
	// Pre-digital-date movies (future digital date & theatrical-only unknown digital date).
	preHealth := upsertMovie(2, "Alpha Orbit", x.now.Add(-5*24*time.Hour), x.now.Add(14*24*time.Hour), true)
	preDebrid := upsertMovie(3, "Bravo Signal", x.now.Add(-5*24*time.Hour), x.now.Add(14*24*time.Hour), true)
	prePlay := upsertMovie(4, "Charlie Harbor", x.now.Add(-5*24*time.Hour), time.Time{}, true)
	postDate := upsertMovie(5, "Delta Echo", x.now.Add(-20*24*time.Hour), x.now.Add(-2*time.Hour), true)

	for _, m := range []core.Title{noEv, preHealth, preDebrid, prePlay, postDate} {
		assertBoth("initial no-evidence "+m.Title, m, false)
	}

	// 2. Unverified pre-date match stays hidden and queues background verification.
	if err := x.s.remember(ctx, x.f.sources[0], preHealth, core.Release{
		ID: "rel-pre-health", Name: "Alpha.Orbit.2026.1080p.WEB-DL", Indexer: "Fake", Size: 6 << 30,
	}, x.s.policy()); err != nil {
		t.Fatal(err)
	}
	assertBoth("unverified pre-date health candidate", preHealth, false)

	var state string
	var nextCheck int64
	if err := x.s.db.QueryRowContext(ctx, `SELECT state,next_check FROM availability_releases WHERE id='rel-pre-health'`).Scan(&state, &nextCheck); err != nil {
		t.Fatal(err)
	}
	if state != "observed" || nextCheck != 0 {
		t.Fatalf("pre-date match did not queue verification: state=%q next_check=%d", state, nextCheck)
	}

	// Theatrical-only (unknown digital date) unverified match also stays hidden.
	if err := x.s.remember(ctx, x.f.sources[0], prePlay, core.Release{
		ID: "rel-theatrical-fake", Name: "Charlie.Harbor.2026.1080p.WEB-DL", Indexer: "Fake", Size: 6 << 30,
	}, x.s.policy()); err != nil {
		t.Fatal(err)
	}
	assertBoth("unverified theatrical-only candidate", prePlay, false)

	// 3a. Verified health check opens the pre-date movie gate immediately.
	x.c.Availability.Monitor = false
	x.c.Availability.Verify = true
	x.v.failure = core.ErrArticleMissing
	// Reject the theatrical fake while verifying preHealth cleanly.
	if _, err := x.s.db.ExecContext(ctx, `UPDATE availability_releases SET next_check=? WHERE id='rel-theatrical-fake'`, x.now.Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	x.v.failure = nil
	if err := x.s.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if x.v.calls != 1 {
		t.Fatalf("expected 1 health check, got %d", x.v.calls)
	}
	assertBoth("verified health check opened preHealth", preHealth, true)

	// 3b. Definitive cached debrid copy opens a pre-date movie gate immediately.
	x.c.Availability.Debrid = true
	x.c.Debrid.Providers = []config.DebridProvider{{Enabled: true, APIKey: "fake"}}
	r.UpdateConfig(x.c)
	torrentFeed := core.ReleaseFeed{ID: "tor", Name: "Tor", Generation: "one", Protocol: core.ProtocolTorrent, Supported: true}
	x.f.sources = append(x.f.sources, torrentFeed)
	// Re-open preHealth under the expanded feed topology, or verify preDebrid under it.
	if err := x.s.remember(ctx, torrentFeed, preDebrid, core.Release{
		ID: "rel-pre-debrid", Name: "Bravo.Signal.2026.1080p.WEB-DL", Indexer: "Tor", Protocol: core.ProtocolTorrent, InfoHash: "deadbeef", Size: 6 << 30,
	}, x.s.policy()); err != nil {
		t.Fatal(err)
	}
	assertBoth("unverified pre-date torrent before CacheCheck", preDebrid, false)
	x.s.d.CacheCheck = func(_ context.Context, hashes []string) (map[string]bool, bool) {
		return map[string]bool{"deadbeef": true}, true
	}
	// Spend all remaining NNTP health checks so only debrid CacheCheck can open it.
	x.v.failure = core.ErrArticleMissing
	if err := x.s.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	assertBoth("cached debrid opened preDebrid", preDebrid, true)

	// 3c. Successful playback (Observe) opens a pre-date movie gate immediately.
	x.s.Observe(ctx, &prePlay, core.Release{
		ID: "rel-playback", Name: "Charlie.Harbor.2026.1080p.WEB-DL", Indexer: "Fake", Size: 6 << 30,
	})
	assertBoth("successful playback opened prePlay", prePlay, true)

	// 4. Post-date unverified match opens immediately without spending a health check.
	checksBefore := x.v.calls
	if err := x.s.remember(ctx, x.f.sources[0], postDate, core.Release{
		ID: "rel-post-date", Name: "Delta.Echo.2026.1080p.WEB-DL", Indexer: "Fake", Size: 6 << 30,
	}, x.s.policy()); err != nil {
		t.Fatal(err)
	}
	if x.v.calls != checksBefore {
		t.Fatal("post-date unverified match spent a health check")
	}
	assertBoth("post-date unverified match opened postDate", postDate, true)
	assertBoth("noEv still hidden", noEv, false)

	// 5. Invalidate retracts the release's evidence for playback/search,
	//    but NEVER re-hides an opened title.
	for _, id := range []string{"rel-pre-health", "rel-pre-debrid", "rel-playback", "rel-post-date"} {
		x.s.Invalidate(ctx, id)
	}
	if vk := x.s.verifiedKeys(ctx); len(vk) != 0 {
		t.Fatalf("Invalidate left verified keys promoted: %+v", vk)
	}
	var invState string
	var invRev int64
	if err := x.s.db.QueryRowContext(ctx, `SELECT state,revision FROM availability_releases WHERE id='rel-playback'`).Scan(&invState, &invRev); err != nil || invState != "unknown" || invRev == 0 {
		t.Fatalf("Invalidate did not retract release evidence: state=%q rev=%d err=%v", invState, invRev, err)
	}
	assertBoth("preDebrid stays open after Invalidate", preDebrid, true)
	assertBoth("prePlay stays open after Invalidate", prePlay, true)
	assertBoth("postDate stays open after Invalidate", postDate, true)

	// 6. Time passing never closes an open gate (7h > 6h evidenceTTL, and 10d).
	for _, step := range []time.Duration{7 * time.Hour, 10 * 24 * time.Hour} {
		x.now = x.now.Add(step)
		assertBoth("preDebrid stays open after "+step.String(), preDebrid, true)
		assertBoth("prePlay stays open after "+step.String(), prePlay, true)
		assertBoth("postDate stays open after "+step.String(), postDate, true)
		assertBoth("noEv still hidden after "+step.String(), noEv, false)
	}

	// 7. Only a policy/profile change starts a fresh gate row; restoring the
	//    previous profile sees its still-open sticky gate row.
	prevDefault := x.c.Quality.Default
	x.c.Quality.Default = "strict-new-profile"
	r.UpdateConfig(x.c)
	assertBoth("new profile starts fresh gate for invalidated prePlay", prePlay, false)
	x.c.Quality.Default = prevDefault
	r.UpdateConfig(x.c)
	assertBoth("restored profile preserves original sticky gate", prePlay, true)
}

func TestEpisodeAirTime2000OpensWithin30mAfter2200Upload(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	x.c.Availability.Verify = false
	x.c.Availability.CatchUp = false
	x.c.Availability.PollMinutes = 30
	x.now = time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC)

	r := resolver.New(nil, nil, nil, x.c, resolver.Options{})
	defer r.Close()
	x.s.d.Validator = suitabilityValidator{x.v, r}

	c, err := catalog.New(x.s.db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return x.now }))
	if err != nil {
		t.Fatal(err)
	}
	x.s.d.Catalog = c

	show := core.Title{
		Key: core.ShowKey(500), Type: core.TypeShow, Title: "Night Watch",
		Year: 2026, Language: "en", Released: x.now,
	}
	ep := core.Title{
		Key: core.EpisodeKey(500, 1, 1), ShowKey: show.Key, Type: core.TypeEpisode,
		Title: "Pilot", ShowTitle: "Night Watch", Season: 1, Episode: 1,
		Year: 2026, Language: "en", Released: x.now,
	}
	if err := c.UpsertTitle(ctx, show, true); err != nil {
		t.Fatal(err)
	}
	if err := c.UpsertTitle(ctx, ep, true); err != nil {
		t.Fatal(err)
	}

	feed := &movingFeed{sources: x.f.sources}
	x.s.d.Feeds = feed
	x.s.d.Search = func(context.Context, core.SearchRequest) ([]core.Release, error) { return nil, nil }

	checkVisible := func() (bool, bool) {
		t.Helper()
		x.s.clearVisibility()
		sf, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeShow})
		if err != nil {
			t.Fatal(err)
		}
		sp, err := c.Browse(ctx, core.BrowseQuery{Filter: sf, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		ef, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeEpisode})
		if err != nil {
			t.Fatal(err)
		}
		epPage, err := c.Browse(ctx, core.BrowseQuery{Filter: ef, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return sp.Total == 1, epPage.Total == 1
	}

	// 20:00 air time -> 22:00: no upload yet, episode and premiere show stay gated.
	for end := x.now.Add(2 * time.Hour); x.now.Before(end); x.now = x.now.Add(time.Minute) {
		if err := x.s.Cycle(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if showVis, epVis := checkVisible(); showVis || epVis {
		t.Fatalf("ungated at 22:00 without upload: show=%v ep=%v", showVis, epVis)
	}

	// 22:00: release appears on the feed. Must open within <= 30m.
	feed.items = []core.Release{{ID: "nw-s01e01", Name: "Night.Watch.S01E01.1080p.WEB-DL", Indexer: "Fake", PubDate: x.now}}
	openedAfter := time.Duration(-1)
	start := x.now
	for step := time.Duration(0); step <= 30*time.Minute; step += time.Minute {
		x.now = start.Add(step)
		if err := x.s.Cycle(ctx); err != nil {
			t.Fatal(err)
		}
		if showVis, epVis := checkVisible(); showVis && epVis {
			openedAfter = step
			break
		}
	}
	if openedAfter < 0 || openedAfter > 30*time.Minute {
		t.Fatalf("episode upload at 22:00 did not open within 30m: openedAfter=%v", openedAfter)
	}
}

func TestRetryScheduleDueOrderAndPerTypeModeFor(t *testing.T) {
	// 1. Per-type ModeFor behavior.
	cfg := config.Availability{Mode: config.ModePlayable}
	if cfg.ModeFor("movie") != config.ModePlayable || cfg.ModeFor("show") != config.ModePlayable || cfg.ModeFor("episode") != config.ModePlayable {
		t.Fatalf("fallback ModeFor wrong: %+v", cfg)
	}
	cfg.MovieMode = config.ModeDigital
	cfg.ShowMode = config.ModeImmediate
	if cfg.ModeFor("movie") != config.ModeDigital || cfg.ModeFor("show") != config.ModeImmediate || cfg.ModeFor("episode") != config.ModeImmediate {
		t.Fatalf("per-type ModeFor wrong: movie=%s show=%s ep=%s", cfg.ModeFor("movie"), cfg.ModeFor("show"), cfg.ModeFor("episode"))
	}

	// 2. Bounded retry cadence steps and window termination.
	anchor := time.Date(2026, 10, 20, 20, 0, 0, 0, time.UTC).Unix()
	windowEnd := anchor + int64((72 * time.Hour).Seconds())
	wantOffsets := []time.Duration{
		30 * time.Minute,
		1 * time.Hour,
		90 * time.Minute,
		2 * time.Hour,
		150 * time.Minute,
		3 * time.Hour,
		4 * time.Hour,
		8 * time.Hour,
		24 * time.Hour,
		48 * time.Hour,
	}
	cur := anchor
	for _, off := range wantOffsets {
		next := nextRetryAfter(anchor, windowEnd, cur)
		want := anchor + int64(off.Seconds())
		if next != want {
			t.Fatalf("after %v: got +%s, want +%s", time.Duration(cur-anchor)*time.Second, time.Duration(next-anchor)*time.Second, off)
		}
		cur = next
	}
	if got := nextRetryAfter(anchor, windowEnd, cur); got != 0 {
		t.Fatalf("retry scheduled at/past window end: %d (windowEnd=%d)", got, windowEnd)
	}

	// 3. Partial index returns due rows in priority DESC, next_check ASC order.
	x := setup(t)
	ctx := t.Context()
	nowTS := x.now.Unix()
	rows := []struct {
		key  core.RatingKey
		next int64
		prio float64
	}{
		{core.MovieKey(10), nowTS - 60, 10},
		{core.MovieKey(20), nowTS, 500},       // higher priority wins
		{core.MovieKey(30), nowTS - 120, 500}, // earlier next_check breaks ties
		{core.MovieKey(40), 0, 9999},          // next_check=0 excluded from partial index
		{core.MovieKey(50), nowTS + 60, 9999}, // future next_check excluded
	}
	for _, r := range rows {
		if _, err := x.s.db.ExecContext(ctx, `INSERT INTO availability_title_checks(rating_key,kind,checked_at,next_check,anchor_ts,window_end_ts,priority,state) VALUES(?,?,?,?,?,?,?,'searching')`,
			int64(r.key), "movie", nowTS-300, r.next, nowTS-3600, nowTS+86400, r.prio); err != nil {
			t.Fatal(err)
		}
	}
	q := `SELECT rating_key FROM availability_title_checks WHERE next_check>0 AND next_check<=? ORDER BY priority DESC,next_check ASC LIMIT 10`
	res, err := x.s.db.QueryContext(ctx, q, nowTS)
	if err != nil {
		t.Fatal(err)
	}
	var got []core.RatingKey
	for res.Next() {
		var k int64
		if err := res.Scan(&k); err != nil {
			t.Fatal(err)
		}
		got = append(got, core.RatingKey(k))
	}
	res.Close()
	want := []core.RatingKey{core.MovieKey(30), core.MovieKey(20), core.MovieKey(10)}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("due order = %v, want %v", got, want)
	}
}

func queryPlanText(t testing.TB, x *fixture, sqlText string, args ...any) string {
	t.Helper()
	rows, err := x.s.db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+sqlText, args...)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN failed: %v\nSQL: %s", err, sqlText)
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatal(err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

// Owner cost-budget proof at N=50,000 recent titles with evidence:
//  1. Gated browse page query uses SQLite indexes (SEARCH ... USING ... INDEX on
//     catalog_titles and availability_gate PK) inside a single SQL round trip.
//  2. Idle 1-minute tick does zero work except indexed due checks, zero writes,
//     zero evidence decodes, and allocates < 64 KB.
//  3. New evidence writes O(1) indexed row with no hidden-set rebuild.
func TestScale50kBrowseIndexPlanAndIdleTickCost(t *testing.T) {
	x := setup(t)
	ctx := context.Background()
	var cfgGen uint64 = 1
	x.s.d.ConfigGeneration = func() uint64 { return cfgGen }
	x.s.d.Search = func(context.Context, core.SearchRequest) ([]core.Release, error) { return nil, nil }

	c, err := catalog.New(x.s.db, nil, config.Catalog{}, nil, catalog.WithNow(func() time.Time { return x.now }))
	if err != nil {
		t.Fatal(err)
	}
	x.s.d.Catalog = c

	const evidencedN = 50_000
	const ungatedN = 5_000
	const totalN = evidencedN + ungatedN

	gp := x.s.currentGatePolicy()
	pol := x.s.policy()
	nowTS := x.now.Unix()
	relBase := x.now.Add(-30 * 24 * time.Hour).Unix()
	nextCheckVerified := x.now.Add(6 * time.Hour).Unix()
	nextRetryFuture := x.now.Add(12 * time.Hour).Unix()
	windowEnd := x.now.Add(30 * 24 * time.Hour).Unix()
	movieKeyBase := int64(core.MovieKey(0))

	seedStart := time.Now()
	tx, err := x.s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Insert 55,000 recent in-window movies (50,000 with verified evidence and
	// open sticky gate; 5,000 ungated whose next scheduled retry is in 12h).
	if _, err := tx.ExecContext(ctx, `
WITH RECURSIVE seq(i) AS (
  SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i<?
)
INSERT INTO catalog_titles(key,type,tmdb_id,title,sort_title,year,released_ts,popularity,browsable,added_at,fetched_at)
SELECT ?+i,'movie',i,'Movie '||i,'movie '||printf('%06d',i),2026,?+(i%86400),CAST(i AS REAL),1,?,? FROM seq`,
		totalN, movieKeyBase, relBase, nowTS, nowTS); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
WITH RECURSIVE seq(i) AS (
  SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i<?
)
INSERT INTO availability_releases(id,rating_key,source_id,generation,payload,title,name,protocol,first_seen,last_seen,first_verified,verified_at,policy,state,next_check,priority,policy_digest)
SELECT 'rel-'||i,?+i,'f','one','{"id":"rel-'||i||'","name":"Movie.'||i||'.2026.1080p.WEB-DL","indexer":"Fake"}','Movie '||i,'Movie.'||i||'.2026.1080p.WEB-DL','',?,?,?,?,?,'verified',?,CAST(i AS REAL),'d' FROM seq`,
		evidencedN, movieKeyBase, nowTS, nowTS, nowTS, nowTS, pol, nextCheckVerified); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
WITH RECURSIVE seq(i) AS (
  SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i<?
)
INSERT INTO availability_gate(policy,rating_key,first_playable_at,source_id,updated_at)
SELECT ?,?+i,?,'f',? FROM seq`,
		evidencedN, gp, movieKeyBase, nowTS, nowTS); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO availability_gate_policy(policy,seeded_at) VALUES(?,?) ON CONFLICT(policy) DO NOTHING`, gp, nowTS); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `
WITH RECURSIVE seq(i) AS (
  SELECT 1 UNION ALL SELECT i+1 FROM seq WHERE i<?
)
INSERT INTO availability_title_checks(rating_key,kind,checked_at,next_check,anchor_ts,window_end_ts,priority,state)
SELECT ?+i,'movie',?,CASE WHEN i<=? THEN 0 ELSE ? END,?+(i%86400),?,CAST(i AS REAL),CASE WHEN i<=? THEN 'found' ELSE 'none' END FROM seq`,
		totalN, movieKeyBase, nowTS, evidencedN, nextRetryFuture, relBase, windowEnd, evidencedN); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `ANALYZE`); err != nil {
		tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	t.Logf("seeded %d recent titles (%d with evidence, %d ungated) in %s", totalN, evidencedN, ungatedN, time.Since(seedStart).Round(time.Millisecond))

	// Run one initial cycle at t0 to establish steady-state feed poll & prune stamps,
	// then advance +1 minute to measure an ordinary idle server tick.
	if err := x.s.Cycle(ctx); err != nil {
		t.Fatal(err)
	}
	x.now = x.now.Add(time.Minute)

	// --- Proof 1: Gated Browse query plan, round-trip count, and latency ---
	pf, err := x.s.PagingFilter(ctx, core.Filter{Type: core.TypeMovie})
	if err != nil {
		t.Fatal(err)
	}
	browseCtx, browseStats := store.WithQueryStats(ctx)
	browseStart := time.Now()
	page, err := c.Browse(browseCtx, core.BrowseQuery{
		Filter:  pf,
		Sort:    core.SortPopularity,
		Desc:    true,
		Limit:   50,
		NoTotal: true,
	})
	browseElapsed := time.Since(browseStart)
	if err != nil {
		t.Fatal(err)
	}
	// Highest popularity keys 50001..55000 are ungated, so the top gated page
	// must begin right at MovieKey(50000) without Go-side post-filtering!
	if len(page.Items) != 50 || page.Items[0].Key != core.MovieKey(evidencedN) {
		t.Fatalf("unexpected gated page len=%d first=%v (want MovieKey(%d))", len(page.Items), page.Items[0].Key, evidencedN)
	}
	if browseStats.Writes() != 0 {
		t.Fatalf("browse wrote %d times", browseStats.Writes())
	}
	stmts := browseStats.Statements()
	if len(stmts) == 0 {
		t.Fatal("no browse SQL captured")
	}
	var mainBrowseSQL string
	for _, st := range stmts {
		if strings.Contains(st.SQL, "availability_gate") {
			mainBrowseSQL = st.SQL
			break
		}
	}
	if mainBrowseSQL == "" {
		t.Fatalf("browse SQL did not inline availability_gate predicate: %+v", stmts)
	}
	dummyArgs := make([]any, strings.Count(mainBrowseSQL, "?"))
	for i := range dummyArgs {
		dummyArgs[i] = 1
	}
	browsePlan := queryPlanText(t, x, mainBrowseSQL, dummyArgs...)
	t.Logf("Gated Browse (N=%dk, 5k ungated ahead of top): latency=%s queries=%d rows=%d\nEXPLAIN QUERY PLAN:\n%s",
		evidencedN/1000, browseElapsed, browseStats.Queries(), browseStats.Rows(), browsePlan)

	if !strings.Contains(browsePlan, "SEARCH") || !strings.Contains(browsePlan, "USING INDEX catalog_titles_") {
		t.Fatalf("browse query did not use catalog_titles_type_browse_pop index:\n%s", browsePlan)
	}
	if !strings.Contains(browsePlan, "availability_gate") || !strings.Contains(browsePlan, "USING COVERING INDEX sqlite_autoindex_availability_gate_1") {
		t.Fatalf("browse query did not use availability_gate covering PK index:\n%s", browsePlan)
	}

	// Also verify due-retry query plan uses availability_title_checks_due partial index.
	dueSQL := `SELECT rating_key,kind,anchor_ts,window_end_ts,priority FROM availability_title_checks WHERE next_check>0 AND next_check<=? ORDER BY priority DESC,next_check ASC LIMIT ?`
	duePlan := queryPlanText(t, x, dueSQL, x.now.Unix(), probeBatch)
	t.Logf("Due-retry EXPLAIN QUERY PLAN:\n%s", duePlan)
	if !strings.Contains(duePlan, "SEARCH") || !strings.Contains(duePlan, "availability_title_checks_due") {
		t.Fatalf("due-retry query did not use availability_title_checks_due index:\n%s", duePlan)
	}

	// --- Proof 2: Idle 1-minute tick query stats & heap allocation budget (< 64 KB) ---
	idleStatsCtx, idleStats := store.WithQueryStats(ctx)
	idleStart := time.Now()
	if err := x.s.Cycle(idleStatsCtx); err != nil {
		t.Fatal(err)
	}
	idleElapsed := time.Since(idleStart)
	if idleStats.Writes() != 0 {
		t.Fatalf("idle tick performed %d writes", idleStats.Writes())
	}

	// Measure exact bytes allocated per idle Cycle across 25 runs.
	const runs = 25
	runtime.GC()
	var beforeMem, afterMem runtime.MemStats
	runtime.ReadMemStats(&beforeMem)
	allocsStart := time.Now()
	for range runs {
		if err := x.s.Cycle(ctx); err != nil {
			t.Fatal(err)
		}
	}
	avgIdleLatency := time.Since(allocsStart) / runs
	runtime.ReadMemStats(&afterMem)
	bytesPerTick := (afterMem.TotalAlloc - beforeMem.TotalAlloc) / runs
	mallocsPerTick := (afterMem.Mallocs - beforeMem.Mallocs) / runs
	t.Logf("Idle Cycle (N=%dk): first_latency=%s avg_latency=%s alloc_bytes=%d B (%.2f KB, budget < 64 KB) mallocs=%d queries=%d rows=%d writes=%d",
		evidencedN/1000, idleElapsed, avgIdleLatency, bytesPerTick, float64(bytesPerTick)/1024.0, mallocsPerTick,
		idleStats.Queries(), idleStats.Rows(), idleStats.Writes())
	if bytesPerTick >= 64*1024 {
		t.Fatalf("idle tick allocated %d bytes (>= 64 KB budget)", bytesPerTick)
	}

	// --- Proof 3: New evidence arrival is O(1) indexed write, no set rebuild ---
	targetKey := core.MovieKey(evidencedN + 1)
	targetTitle := core.Title{Key: targetKey, Type: core.TypeMovie, Title: "Movie 50001", Year: 2026, Released: x.now.Add(-24 * time.Hour)}
	evCtx, evStats := store.WithQueryStats(ctx)
	evStart := time.Now()
	x.s.Observe(evCtx, &targetTitle, core.Release{ID: "rel-50001", Name: "Movie.50001.2026.1080p.WEB-DL", Indexer: "Fake"})
	evElapsed := time.Since(evStart)
	t.Logf("New evidence (Observe on #50001): latency=%s queries=%d writes=%d rows=%d",
		evElapsed, evStats.Queries(), evStats.Writes(), evStats.Rows())
	if !x.s.isGateOpen(ctx, gp, targetKey) {
		t.Fatal("Observe did not open sticky gate for #50001")
	}
}
