package resolver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

type seasonCatalog struct {
	titles   map[core.RatingKey]*core.Title
	episodes map[core.RatingKey][]core.Title
}

func (c seasonCatalog) Get(_ context.Context, key core.RatingKey) (*core.Title, error) {
	if t, ok := c.titles[key]; ok {
		cp := *t
		return &cp, nil
	}
	return nil, core.ErrNotFound
}

func (c seasonCatalog) Children(_ context.Context, key core.RatingKey) ([]core.Title, error) {
	if eps, ok := c.episodes[key]; ok {
		return append([]core.Title(nil), eps...), nil
	}
	return nil, core.ErrNotFound
}

func TestConsoleSeasonReleasesAndBatchPin(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)

	showKey := core.ShowKey(1396)
	seasonKey := core.SeasonKey(1396, 1)
	ep1Key := core.EpisodeKey(1396, 1, 1)
	ep2Key := core.EpisodeKey(1396, 1, 2)
	ep3Key := core.EpisodeKey(1396, 1, 3)

	show := &core.Title{
		Key:   showKey,
		Type:  core.TypeShow,
		Title: "Boundary Bay",
		Year:  2020,
		IDs:   core.IDs{TMDB: 1396, TVDB: 81189},
	}
	season := &core.Title{
		Key:       seasonKey,
		Type:      core.TypeSeason,
		ShowKey:   showKey,
		ShowTitle: "Boundary Bay",
		Title:     "Season 1",
		Season:    1,
	}
	ep1 := core.Title{
		Key:      ep1Key,
		Type:     core.TypeEpisode,
		Title:    "Pilot",
		Season:   1,
		Episode:  1,
		Released: time.Date(2020, 3, 1, 0, 0, 0, 0, time.UTC),
		Runtime:  48 * time.Minute,
	}
	ep2 := core.Title{
		Key:      ep2Key,
		Type:     core.TypeEpisode,
		Title:    "Second Tide",
		Season:   1,
		Episode:  2,
		Released: time.Date(2020, 3, 8, 0, 0, 0, 0, time.UTC),
		Runtime:  48 * time.Minute,
	}
	ep3Future := core.Title{
		Key:      ep3Key,
		Type:     core.TypeEpisode,
		Title:    "Future Tide",
		Season:   1,
		Episode:  3,
		Released: time.Now().UTC().Add(14 * 24 * time.Hour),
		Runtime:  48 * time.Minute,
	}

	ep1Single := torrent(rel("ep1-web", "Boundary.Bay.S01E01.1080p.WEB-DL.x264-GRP", 2200))
	ep1Single.MagnetURL = "magnet:?xt=urn:btih:secret1"
	ep2Single := torrent(rel("ep2-web", "Boundary.Bay.S01E02.1080p.WEB-DL.x264-GRP", 2200))
	ep2Single.MagnetURL = "magnet:?xt=urn:btih:secret2"
	seasonPack := torrent(rel("s01-pack", "Boundary.Bay.S01.720p.BluRay.x264-PACK", 12000))
	seasonPack.MagnetURL = "magnet:?xt=urn:btih:secretpack"

	idx := &rescueFakeIndexer{
		fakeIndexer: &fakeIndexer{releases: []core.Release{ep1Single, ep2Single}},
		rescued:     []core.Release{seasonPack},
	}
	svc := New(idx, fakeHealth{}, db, testCfg(), Options{})
	defer svc.Close()

	var mu sync.Mutex
	var invalidated []core.RatingKey
	cat := seasonCatalog{
		titles: map[core.RatingKey]*core.Title{
			showKey:   show,
			seasonKey: season,
			ep1Key:    {Key: ep1Key, Type: core.TypeEpisode, ShowTitle: "Boundary Bay", Title: "Pilot", Season: 1, Episode: 1},
			ep2Key:    {Key: ep2Key, Type: core.TypeEpisode, ShowTitle: "Boundary Bay", Title: "Second Tide", Season: 1, Episode: 2},
		},
		episodes: map[core.RatingKey][]core.Title{
			seasonKey: {ep1, ep2, ep3Future},
		},
	}
	srv := httptest.NewServer(svc.ConsoleHandler(ConsoleDeps{
		Catalog: cat,
		Invalidate: func(k core.RatingKey) {
			mu.Lock()
			invalidated = append(invalidated, k)
			mu.Unlock()
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}))
	defer srv.Close()

	do := func(method, path, body string, out any) int {
		t.Helper()
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req, err := http.NewRequest(method, srv.URL+path, r)
		if err != nil {
			t.Fatal(err)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, _ := io.ReadAll(res.Body)
		if out != nil && len(raw) > 0 {
			if err := json.Unmarshal(raw, out); err != nil {
				t.Fatalf("%s %s: %v: %s", method, path, err, raw)
			}
		}
		return res.StatusCode
	}

	var seasonRes ConsoleSeasonSearch
	if code := do("GET", "/season?show="+showKey.String()+"&season=1", "", &seasonRes); code != http.StatusOK {
		t.Fatalf("GET /season status=%d", code)
	}
	if seasonRes.SeasonKey != seasonKey || seasonRes.TitleName != "Boundary Bay · Season 1" {
		t.Fatalf("season header: %+v", seasonRes)
	}
	if len(seasonRes.Episodes) != 2 {
		t.Fatalf("expected 2 aired episodes (excluding future ep3), got %d: %+v", len(seasonRes.Episodes), seasonRes.Episodes)
	}
	if seasonRes.Episodes[0].Winner != "ep1-web" || seasonRes.Episodes[1].Winner != "ep2-web" {
		t.Fatalf("unexpected per-episode winners: ep1=%q ep2=%q", seasonRes.Episodes[0].Winner, seasonRes.Episodes[1].Winner)
	}
	for _, ep := range seasonRes.Episodes {
		for _, cr := range ep.Releases {
			if cr.Release.MagnetURL != "" || cr.Release.NZBURL != "" {
				t.Fatalf("browser release leaked credentials: %+v", cr.Release)
			}
		}
	}
	if len(seasonRes.Packs) != 1 {
		t.Fatalf("expected 1 discovered season pack, got %+v", seasonRes.Packs)
	}
	pack := seasonRes.Packs[0]
	wantEp1PackID := core.TargetReleaseID("s01-pack", core.ReleaseTarget{Season: 1, Episode: 1})
	wantEp2PackID := core.TargetReleaseID("s01-pack", core.ReleaseTarget{Season: 1, Episode: 2})
	if pack.EpisodeReleases[ep1Key.String()] != wantEp1PackID || pack.EpisodeReleases[ep2Key.String()] != wantEp2PackID {
		t.Fatalf("pack episodeReleases=%v, want ep1=%s ep2=%s", pack.EpisodeReleases, wantEp1PackID, wantEp2PackID)
	}

	// Atomic validation: if any episode's releaseId is unknown, no pins are written.
	badBatch, _ := json.Marshal(map[string]any{
		"note": "partial should fail",
		"pins": []map[string]string{
			{"key": ep1Key.String(), "releaseId": "ep1-web"},
			{"key": ep2Key.String(), "releaseId": "missing-id"},
		},
	})
	if code := do("POST", "/pins/batch", string(badBatch), nil); code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown release in batch, got %d", code)
	}
	if len(svc.Pins()) != 0 {
		t.Fatalf("failed batch wrote partial pins: %+v", svc.Pins())
	}

	// Pin the chosen season pack across every episode in one batch request.
	goodBatch, _ := json.Marshal(map[string]any{
		"note": "BluRay pack for all of S1",
		"pins": []map[string]string{
			{"key": ep1Key.String(), "releaseId": pack.EpisodeReleases[ep1Key.String()]},
			{"key": ep2Key.String(), "releaseId": pack.EpisodeReleases[ep2Key.String()]},
		},
	})
	var pinnedOut struct {
		Pins []Pin `json:"pins"`
	}
	if code := do("POST", "/pins/batch", string(goodBatch), &pinnedOut); code != http.StatusOK {
		t.Fatalf("POST /pins/batch status=%d", code)
	}
	if len(pinnedOut.Pins) != 2 {
		t.Fatalf("expected 2 pins returned, got %+v", pinnedOut.Pins)
	}
	mu.Lock()
	invCount := len(invalidated)
	mu.Unlock()
	if invCount != 2 {
		t.Fatalf("expected 2 invalidations, got %d", invCount)
	}

	// Verify persistence in resolver_pins and end-to-end Resolve playback per episode.
	reloaded := New(&torrentIndexer{fakeIndexer: idx.fakeIndexer}, fakeHealth{}, db, testCfg(), Options{})
	defer reloaded.Close()
	if len(reloaded.Pins()) != 2 {
		t.Fatalf("expected 2 persisted pins in resolver_pins, got %+v", reloaded.Pins())
	}

	ep1Full := ep1
	ep1Full.ShowKey, ep1Full.ShowTitle, ep1Full.ShowYear, ep1Full.ShowIDs = showKey, show.Title, show.Year, show.IDs
	res1, err := reloaded.Resolve(ctx, core.ResolveRequest{Title: &ep1Full, Client: webCaps()})
	if err != nil {
		t.Fatalf("resolve ep1: %v", err)
	}
	if res1.Release.ID != wantEp1PackID || res1.Release.Target == nil || res1.Release.Target.Episode != 1 {
		t.Fatalf("ep1 resolved %+v, want pinned pack target %s", res1.Release, wantEp1PackID)
	}

	ep2Full := ep2
	ep2Full.ShowKey, ep2Full.ShowTitle, ep2Full.ShowYear, ep2Full.ShowIDs = showKey, show.Title, show.Year, show.IDs
	res2, err := reloaded.Resolve(ctx, core.ResolveRequest{Title: &ep2Full, Client: webCaps()})
	if err != nil {
		t.Fatalf("resolve ep2: %v", err)
	}
	if res2.Release.ID != wantEp2PackID || res2.Release.Target == nil || res2.Release.Target.Episode != 2 {
		t.Fatalf("ep2 resolved %+v, want pinned pack target %s", res2.Release, wantEp2PackID)
	}
}
