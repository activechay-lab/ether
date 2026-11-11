package availability

import (
	"context"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/resolver"
	"testing"
	"time"
)

type suitabilityValidator struct {
	*fakeValidator
	r *resolver.Service
}

func (v suitabilityValidator) FeedCandidate(ctx context.Context, t *core.Title, r core.Release) (core.Release, string, error) {
	return v.r.FeedCandidate(ctx, t, r)
}

type cooldownValidator struct {
	suitabilityValidator
	missing map[core.RatingKey]time.Time
}

func (v cooldownValidator) KnownUnavailableSince(context.Context, []core.RatingKey) map[core.RatingKey]time.Time {
	return v.missing
}

func TestRecentEvidenceWindow(t *testing.T) {
	x := setup(t)
	r := resolver.New(nil, nil, nil, x.c, resolver.Options{})
	defer r.Close()
	x.s.d.Validator = suitabilityValidator{x.v, r}
	title := x.cat.titles[core.MovieKey(1)]
	title.Language = "en"
	title.HomeReleased = x.now.Add(-time.Hour)
	x.cat.titles[title.Key] = title
	put := func(target core.Title, id, name string) {
		t.Helper()
		if err := x.s.remember(t.Context(), x.f.sources[0], target, core.Release{ID: id, Name: name, Indexer: "Fake"}, x.s.policy()); err != nil {
			t.Fatal(err)
		}
	}
	visible := func(target core.Title, want int) {
		t.Helper()
		if got := x.s.FilterDiscovery(t.Context(), []core.Title{target}); len(got) != want {
			t.Fatalf("visible=%d want=%d", len(got), want)
		}
	}
	visible(title, 0)
	put(title, "cam", "Film.2020.1080p.CAM")
	put(title, "dub", "Film.2020.1080p.WEB-DL.[Ukr.Dub]")
	put(title, "pre", "Film.2020.1080p.HQ.Pre.Multi")
	visible(title, 0)
	put(title, "good", "Film.2020.1080p.WEB-DL.ENG")
	visible(title, 1)
	// Policy/credential changes start a fresh gate policy without reusing old evidence.
	x.f.sources[0].Generation = "two"
	visible(title, 0)
	x.f.sources[0].Generation = "one"
	visible(title, 1)
	// Once opened under a policy, Invalidate retracts the release's evidence
	// without closing the title's sticky gate.
	x.s.Invalidate(t.Context(), "good")
	visible(title, 1)

	boundary := core.Title{Key: core.MovieKey(2), Type: core.TypeMovie, Title: "Boundary", Year: 2020, Language: "en", Released: x.now.Add(-60 * 24 * time.Hour)}
	x.cat.titles[boundary.Key] = boundary
	visible(boundary, 1)
	boundary.Released = x.now.Add(-60*24*time.Hour + time.Second)
	x.cat.titles[boundary.Key] = boundary
	visible(boundary, 0)

	// Future content remains hidden even when a named upload exists.
	future := core.Title{Key: core.MovieKey(3), Type: core.TypeMovie, Title: "Film", Year: 2020, Language: "en", Released: x.now.Add(time.Hour)}
	x.cat.titles[future.Key] = future
	put(future, "future", "Film.2020.1080p.WEB-DL.ENG")
	visible(future, 0)
	if x.f.calls != 0 || x.v.calls != 0 {
		t.Fatal("browsing spent provider quota")
	}
}

func TestEpisodeEvidenceExpiresAtOneDayNotSeries(t *testing.T) {
	x := setup(t)
	ep := core.Title{Key: core.EpisodeKey(7, 1, 2), Type: core.TypeEpisode, Released: x.now.Add(-23 * time.Hour)}
	epNoEvidence := core.Title{Key: core.EpisodeKey(7, 1, 3), Type: core.TypeEpisode, Released: x.now.Add(-23 * time.Hour)}
	show := core.Title{Key: core.ShowKey(7), Type: core.TypeShow, Released: x.now.AddDate(-20, 0, 0)}
	got := x.s.FilterDiscovery(t.Context(), []core.Title{show, ep, epNoEvidence})
	if len(got) != 1 || got[0].Key != show.Key {
		t.Fatal(got)
	}
	if err := x.s.remember(t.Context(), x.f.sources[0], ep, x.release("ep"), x.s.policy()); err != nil {
		t.Fatal(err)
	}
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{ep}); len(got) != 1 {
		t.Fatal("upload must unlock immediately")
	}
	x.s.Invalidate(t.Context(), "ep")
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{ep}); len(got) != 1 {
		t.Fatal("sticky gate must not re-hide after Invalidate")
	}
	x.now = x.now.Add(time.Hour)
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{epNoEvidence}); len(got) != 1 {
		t.Fatal("24h assumption did not take effect")
	}
}

func TestNewReleaseOverridesEarlierUnavailableWithoutSearching(t *testing.T) {
	x := setup(t)
	r := resolver.New(nil, nil, nil, x.c, resolver.Options{})
	defer r.Close()
	title := x.cat.titles[core.MovieKey(1)]
	title.HomeReleased = x.now.Add(-time.Hour)
	x.cat.titles[title.Key] = title
	missing := map[core.RatingKey]time.Time{title.Key: x.now}
	x.s.d.Validator = cooldownValidator{suitabilityValidator{x.v, r}, missing}
	put := func(id string) {
		t.Helper()
		if err := x.s.remember(t.Context(), x.f.sources[0], title, core.Release{ID: id, Name: "Film.2020.1080p.WEB-DL", Indexer: "Fake"}, x.s.policy()); err != nil {
			t.Fatal(err)
		}
	}
	put("old")
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{title}); len(got) != 0 {
		t.Fatal("old evidence defeated negative")
	}
	x.now = x.now.Add(time.Minute)
	put("new")
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{title}); len(got) != 1 {
		t.Fatal("new release stuck behind old cooldown")
	}
	if x.f.calls != 0 || x.v.calls != 0 {
		t.Fatal("provider calls")
	}
}

func TestRetainedFeedUnlocksLaterCatalogEpisode(t *testing.T) {
	x := setup(t)
	r := resolver.New(nil, nil, nil, x.c, resolver.Options{})
	defer r.Close()
	x.s.d.Validator = suitabilityValidator{x.v, r}
	rel := core.Release{ID: "upload", Name: "Series.S01E02.1080p.WEB-DL", Indexer: "Fake"}
	if err := x.s.retainFeed(t.Context(), x.f.sources[0], []core.Release{rel}); err != nil {
		t.Fatal(err)
	}
	ep := core.Title{Key: core.EpisodeKey(7, 1, 2), Type: core.TypeEpisode, Title: "Second", ShowTitle: "Series", Season: 1, Episode: 2, Released: x.now.Add(-time.Hour)}
	x.cat.titles[ep.Key] = ep
	next := ep
	next.Key = core.EpisodeKey(7, 1, 3)
	next.Episode = 3
	x.cat.titles[next.Key] = next
	got := x.s.FilterDiscovery(t.Context(), []core.Title{ep, next})
	if len(got) != 1 || got[0].Key != ep.Key {
		t.Fatalf("exact episode match: %+v", got)
	}
	x.s.Invalidate(t.Context(), rel.ID)
	// Sticky gate stays open under the current policy, while the invalidated
	// retained upload cannot seed a fresh policy fingerprint.
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{ep}); len(got) != 1 {
		t.Fatal("sticky gate closed on Invalidate")
	}
	x.c.Quality.Default = "new-policy"
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{ep}); len(got) != 0 {
		t.Fatal("retained feed resurrected invalidated source under new policy")
	}
	if x.f.calls != 0 {
		t.Fatal("refetched feed")
	}
}

func TestTransientCheckDoesNotEraseReleaseExistence(t *testing.T) {
	x := setup(t)
	title := x.cat.titles[core.MovieKey(1)]
	title.HomeReleased = x.now.Add(-time.Hour)
	x.cat.titles[title.Key] = title
	if err := x.s.remember(t.Context(), x.f.sources[0], title, x.release("known"), x.s.policy()); err != nil {
		t.Fatal(err)
	}
	if _, err := x.s.db.Exec(`UPDATE availability_releases SET state='unknown',reason='timeout'`); err != nil {
		t.Fatal(err)
	}
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{title}); len(got) != 1 {
		t.Fatal("timeout erased observed existence")
	}
	x.s.Invalidate(t.Context(), "known")
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{title}); len(got) != 1 {
		t.Fatal("sticky gate closed after Invalidate")
	}
	var state string
	var rev int64
	if err := x.s.db.QueryRow(`SELECT state,revision FROM availability_releases WHERE id='known'`).Scan(&state, &rev); err != nil || state != "unknown" || rev == 0 {
		t.Fatalf("Invalidate did not mark release evidence: state=%q rev=%d err=%v", state, rev, err)
	}
}

func TestConfigurableMovieWindow(t *testing.T) {
	now := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	movie := core.Title{Key: core.MovieKey(1), Type: core.TypeMovie, Released: now.Add(-90 * 24 * time.Hour)}
	for _, tc := range []struct {
		cfg  config.Availability
		want bool
	}{
		{config.Availability{}, false}, {config.Availability{MovieEvidenceDays: 120}, true},
		{config.Availability{MovieEvidenceDays: 90}, false}, {config.Availability{MovieEvidenceDays: 120, DisableMovieEvidenceGate: true}, false},
	} {
		if got := evidenceWindow(movie, now, tc.cfg); got != tc.want {
			t.Fatalf("%+v: got %v", tc.cfg, got)
		}
	}
	ep := core.Title{Key: core.EpisodeKey(1, 1, 1), Type: core.TypeEpisode, Released: now.Add(-23 * time.Hour)}
	if !evidenceWindow(ep, now, config.Availability{DisableMovieEvidenceGate: true, MovieEvidenceDays: 120}) {
		t.Fatal("episode policy changed")
	}
	x := setup(t)
	movie.Released = x.now.Add(-90 * 24 * time.Hour)
	x.c.Availability.MovieEvidenceDays = 120
	if len(x.s.FilterDiscovery(t.Context(), []core.Title{movie})) != 0 {
		t.Fatal("extended window not applied")
	}
	x.c.Availability.DisableMovieEvidenceGate = true
	if len(x.s.FilterDiscovery(t.Context(), []core.Title{movie})) != 1 {
		t.Fatal("disabled gate still applied")
	}
	x.c.Availability.DisableMovieEvidenceGate = false
	if len(x.s.FilterDiscovery(t.Context(), []core.Title{movie})) != 0 {
		t.Fatal("gate did not resume")
	}
}

func TestCurrentDiscoveryBypassesRemoteObservationMemo(t *testing.T) {
	x := setup(t)
	title := x.cat.titles[core.MovieKey(1)]
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{title}); len(got) != 0 {
		t.Fatal("fixture unexpectedly visible without evidence")
	}
	// Another cluster node opens the sticky gate without clearing this node's memo.
	gp := x.s.currentGatePolicy()
	nowTS := x.now.Unix()
	if _, err := x.s.db.Exec(`INSERT INTO availability_gate(policy,rating_key,first_playable_at,source_id,updated_at) VALUES(?,?,?,?,?)`, gp, int64(title.Key), nowTS, "f", nowTS); err != nil {
		t.Fatal(err)
	}
	if got := x.s.FilterDiscovery(t.Context(), []core.Title{title}); len(got) != 0 {
		t.Fatal("fixture did not retain cached memo")
	}
	if got := x.s.FilterDiscoveryCurrent(t.Context(), []core.Title{title}); len(got) != 1 {
		t.Fatal("current preflight reused stale negative memo")
	}
}

func TestFilterDiscoveryReadsConfigOncePerBatch(t *testing.T) {
	x := setup(t)
	var calls int
	x.s.cfg = func() config.Config {
		calls++
		return x.c
	}
	titles := make([]core.Title, 8)
	for i := range titles {
		titles[i] = core.Title{Key: core.MovieKey(int64(100 + i)), Type: core.TypeMovie, Title: "Old", Released: x.now.Add(-400 * 24 * time.Hour)}
	}
	calls = 0
	if got := x.s.FilterDiscovery(t.Context(), titles); len(got) != len(titles) || calls != 1 {
		t.Fatalf("batch reads=%d visible=%d", calls, len(got))
	}
	var gen uint64 = 1
	x.s.d.ConfigGeneration = func() uint64 { return gen }
	calls = 0
	x.s.FilterDiscovery(t.Context(), titles)
	if calls != 1 {
		t.Fatalf("first generation read config %d times", calls)
	}
	calls = 0
	if got := x.s.FilterDiscovery(t.Context(), titles); len(got) != len(titles) || calls != 1 {
		t.Fatalf("fresh snapshot reads=%d visible=%d", calls, len(got))
	}
	before := x.s.policy()
	calls = 0
	gen++
	x.c.Quality.Default = "bench-changed"
	after := x.s.policy()
	if calls != 1 || before == after {
		t.Fatalf("generation bump reads=%d before=%s after=%s", calls, before, after)
	}
}
