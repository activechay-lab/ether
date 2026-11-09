package studio

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"

	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
)

func insertHistoryFixture(t *testing.T, db *sql.DB, id int64, ratingKey int64, titleName, winnerID string, cands []resolver.CandidateTrace) {
	t.Helper()
	tr := resolver.Trace{
		TitleName:  titleName,
		Profile:    "default",
		At:         time.Now().UTC(),
		Winner:     winnerID,
		Candidates: cands,
	}
	raw, err := json.Marshal(tr)
	if err != nil {
		t.Fatal(err)
	}
	var winnerName string
	for _, c := range cands {
		if c.ReleaseID == winnerID {
			winnerName = c.Name
			break
		}
	}
	_, err = db.Exec(
		`INSERT INTO resolver_history(id, resolved_at, rating_key, title_name, profile, release_id, release_name, indexer, size, error, failover, pinned, candidates, trace)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', 0, 0, ?, ?)`,
		id, time.Now().Unix()+id, ratingKey, titleName, "default", winnerID, winnerName, "test-idx", 4_000_000_000, len(cands), string(raw),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRankingReplayPrefersDubAndNeutralIsUnchanged(t *testing.T) {
	db := store.OpenTest(t)
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })

	// Trace 1: 1080p standard release beat 1080p DUB release on soft score.
	insertHistoryFixture(t, db, 1, 101, "Spirited Away (2001)", "rel-1a", []resolver.CandidateTrace{
		{
			ReleaseID: "rel-1a",
			Name:      "Spirited.Away.2001.1080p.BluRay.x264-GRP",
			Indexer:   "nzb",
			Score:     95,
			Size:      8_000_000_000,
			Height:    1080,
		},
		{
			ReleaseID: "rel-1b",
			Name:      "Spirited.Away.2001.DUB.1080p.BluRay.x264-GRP",
			Indexer:   "nzb",
			Score:     90,
			Size:      7_800_000_000,
			Height:    1080,
		},
	})

	// Trace 2: Neither candidate is a DUB release, so pick stays unchanged.
	insertHistoryFixture(t, db, 2, 102, "Arrival (2016)", "rel-2a", []resolver.CandidateTrace{
		{
			ReleaseID: "rel-2a",
			Name:      "Arrival.2016.2160p.WEB-DL.DDP5.1.HDR.HEVC-GRP",
			Indexer:   "nzb",
			Score:     120,
			Size:      18_000_000_000,
			Height:    2160,
		},
		{
			ReleaseID: "rel-2b",
			Name:      "Arrival.2016.1080p.WEB-DL.DDP5.1.H.264-GRP",
			Indexer:   "nzb",
			Score:     80,
			Size:      6_000_000_000,
			Height:    1080,
		},
	})

	// Trace 3: 2160p non-DUB beat 1080p DUB by resolution, but when the plugin
	// step is placed at position 0 (top of ladder), 1080p DUB wins; when placed
	// after resolution, 2160p non-DUB keeps winning.
	insertHistoryFixture(t, db, 3, 103, "Boy and the Heron (2023)", "rel-3a", []resolver.CandidateTrace{
		{
			ReleaseID: "rel-3a",
			Name:      "Boy.and.the.Heron.2023.2160p.WEB-DL.HEVC-GRP",
			Indexer:   "nzb",
			Score:     110,
			Size:      15_000_000_000,
			Height:    2160,
		},
		{
			ReleaseID: "rel-3b",
			Name:      "Boy.and.the.Heron.2023.DUB.1080p.WEB-DL.H.264-GRP",
			Indexer:   "nzb",
			Score:     85,
			Size:      5_000_000_000,
			Height:    1080,
		},
	})

	runner := NewRunner(ReplayDeps{
		DB:     db,
		Engine: eng,
	})
	t.Cleanup(func() { _ = runner.Close() })

	manifestJSON := json.RawMessage(`{
		"id": "org.example.dub-step",
		"version": "1.0.0",
		"name": "Dub Ranker",
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"summary": "Prefers dubbed releases during ranking.",
			"capabilities": ["ranking_step"],
			"runtime": { "kind": "hosted", "entry": "plugin.js" },
			"contributes": {
				"rankingSteps": [
					{ "key": "prefer_dub", "label": "Prefer dubbed audio" }
				]
			}
		}
	}`)

	dubJS := `
function rank_key(candidate, config) {
  if (config && config.neutral) {
    return 0;
  }
  return candidate.name.includes("DUB") ? 10 : 0;
}
globalThis.rank_key = rank_key;
`

	ctx := context.Background()

	// 1. The step's points join the custom format score: Trace 1 (both
	// 1080p) switches to DUB, while Trace 3 (2160p vs 1080p DUB) keeps the
	// 2160p release, since quality ranks first.
	rep, err := runner.Replay(ctx, ReplayRequest{
		DevInstance: &DevInstance{
			Manifest: manifestJSON,
			Files:    map[string]string{"plugin.js": dubJS},
		},
		Kind:   ReplayRanking,
		Params: ReplayParams{StepKey: "prefer_dub"},
	})
	if err != nil {
		t.Fatalf("ranking replay failed: %v", err)
	}
	if rep.Ranking == nil {
		t.Fatal("expected ranking report")
	}
	if rep.Ranking.TracesLoaded != 3 || rep.Ranking.TracesEvaluated != 3 {
		t.Fatalf("traces loaded=%d evaluated=%d, want 3", rep.Ranking.TracesLoaded, rep.Ranking.TracesEvaluated)
	}
	if rep.Ranking.ChangedCount != 1 || rep.Ranking.UnchangedCount != 2 {
		t.Fatalf("changed=%d unchanged=%d, want 1 changed and 2 unchanged", rep.Ranking.ChangedCount, rep.Ranking.UnchangedCount)
	}
	ch := rep.Ranking.Changed[0]
	if ch.TitleName != "Spirited Away (2001)" || !strings.Contains(ch.AfterReleaseName, "DUB") || strings.Contains(ch.BeforeReleaseName, "DUB") {
		t.Fatalf("unexpected changed pick: %+v", ch)
	}
	// 3. When the step is neutral (returns 0 for all candidates), 0 picks change.
	repNeutral, err := runner.Replay(ctx, ReplayRequest{
		DevInstance: &DevInstance{
			Manifest: manifestJSON,
			Files:    map[string]string{"plugin.js": dubJS},
		},
		Kind: ReplayRanking,
		Params: ReplayParams{
			StepKey: "prefer_dub",
			Config:  json.RawMessage(`{"neutral":true}`),
		},
	})
	if err != nil {
		t.Fatalf("neutral ranking replay failed: %v", err)
	}
	if repNeutral.Ranking.ChangedCount != 0 || repNeutral.Ranking.UnchangedCount != 3 {
		t.Fatalf("neutral replay changed=%d unchanged=%d, want 0 and 3", repNeutral.Ranking.ChangedCount, repNeutral.Ranking.UnchangedCount)
	}

	// 4. Rule condition (cond_match) replay.
	condManifest := json.RawMessage(`{
		"id": "org.example.dub-cond",
		"version": "1.0.0",
		"name": "Dub Condition",
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"summary": "Matches releases containing a custom tag.",
			"capabilities": ["rule_condition"],
			"runtime": { "kind": "hosted", "entry": "plugin.js" },
			"contributes": {
				"ruleConditions": [
					{ "key": "has_tag", "label": "Has tag", "operators": ["contains"] }
				]
			}
		}
	}`)
	condJS := `
function cond_match(candidate, op, val) {
  return op === "contains" && typeof val === "string" && candidate.name.includes(val);
}
globalThis.cond_match = cond_match;
`
	repCond, err := runner.Replay(ctx, ReplayRequest{
		DevInstance: &DevInstance{
			Manifest: condManifest,
			Files:    map[string]string{"plugin.js": condJS},
		},
		Kind: ReplayRanking,
		Params: ReplayParams{
			ConditionKey: "has_tag",
			Operator:     "contains",
			Value:        json.RawMessage(`"DUB"`),
			Action:       "prefer",
		},
	})
	if err != nil {
		t.Fatalf("condition replay failed: %v", err)
	}
	// A Prefer rule is a scored format: it wins within a quality (Trace 1)
	// but not over a better quality (Trace 3).
	if repCond.Ranking.Mode != "rule_condition" || repCond.Ranking.ChangedCount != 1 || repCond.Ranking.PreferScore != defaultPreferScore {
		t.Fatalf("condition replay mode=%s changed=%d, want rule_condition and 1", repCond.Ranking.Mode, repCond.Ranking.ChangedCount)
	}
}

func TestEventReplayDryRunRecordsWithoutApplying(t *testing.T) {
	db := store.OpenTest(t)
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })

	var liveWatchWrites int
	var liveCollectionWrites int
	var liveNotices []string

	mgr, err := plugins.New(plugins.Deps{
		DB:       db,
		Registry: extend.New(),
		Tools:    tools.NewCatalogue(),
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Backends: plugins.Backends{
			SetWatchState: func(context.Context, int64, string, bool) (bool, error) {
				liveWatchWrites++
				return false, nil
			},
			CollectionsUpsert: func(context.Context, json.RawMessage) (json.RawMessage, error) {
				liveCollectionWrites++
				return nil, nil
			},
			Notice: func(text string) {
				liveNotices = append(liveNotices, text)
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", plugins.HostedRuntimeFactory(eng))

	ctx := context.Background()
	fixtureEvents := []plugins.Event{{
		ID:       "ev-1",
		Name:     plugins.EventPlaybackCompleted,
		At:       time.Now().UTC().Add(-2 * time.Hour),
		ViewerID: 7,
		Payload:  json.RawMessage(`{"ratingKey":501,"title":"Dune (2021)"}`),
	}, {
		ID:      "ev-2",
		Name:    plugins.EventTitleAvailable,
		At:      time.Now().UTC().Add(-1 * time.Hour),
		Payload: json.RawMessage(`{"ratingKey":502,"title":"Nosferatu (2024)"}`),
	}}
	// Replay consumes retained log rows; seed them directly rather than
	// relying on publication before any plugin has subscribed.
	for _, event := range fixtureEvents {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO plugins_event_log(name,payload,created) VALUES(?,?,?)`, event.Name, string(raw), time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}

	eventManifest := json.RawMessage(`{
		"id": "org.example.scrobbler",
		"version": "1.0.0",
		"name": "Dry Run Scrobbler",
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"summary": "Syncs playback completions to collections.",
			"capabilities": ["events"],
			"runtime": { "kind": "hosted", "entry": "plugin.js" },
			"permissions": {
				"collections": "write",
				"viewers": {
					"identity": "all",
					"write_watch_state": "granted_viewers"
				}
			},
			"contributes": {
				"events": {
					"subscribe": ["playback.completed"]
				}
			}
		}
	}`)

	eventJS := `
function handle(req) {
  if (req.path === "/ether/event") {
    const body = typeof req.body === "string" ? JSON.parse(req.body) : req.body;
    const events = (body && body.events) || [];
    const ack = [];
    for (const ev of events) {
      ack.push(ev.id);
      ether.kv.put("last_event", ev.id);
      ether.viewers.setWatchState({ viewerId: ev.viewerId || 1, titleId: "501", watched: true });
      ether.collections.upsert({ id: "recent-completions", title: "Recent completions" });
      ether.notify.owner("Synced playback completion for " + ev.id);
    }
    return {
      status: 200,
      body: {
        ack,
        notifications: [{ eventId: ack[0] || "", title: "Scrobbled", text: "Marked Dune as watched" }]
      }
    };
  }
  return { status: 200, body: { status: "ok" } };
}
globalThis.handle = handle;
`

	// Install the plugin on the manager to verify installed-plugin replay too.
	prev, err := mgr.PreviewInstall(ctx, plugins.Source{
		Kind:     "studio",
		Manifest: eventManifest,
		Files:    map[string][]byte{"plugin.js": []byte(eventJS)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mgr.Install(ctx, prev, plugins.InstallOptions{
		Grants: plugins.Grants{Viewers: []int64{7}},
	}); err != nil {
		t.Fatal(err)
	}

	runner := NewRunner(ReplayDeps{
		DB:      db,
		Manager: mgr,
		Engine:  eng,
	})
	t.Cleanup(func() { _ = runner.Close() })

	rep, err := runner.Replay(ctx, ReplayRequest{
		Plugin: "org.example.scrobbler",
		Kind:   ReplayEvents,
	})
	if err != nil {
		t.Fatalf("event replay failed: %v", err)
	}
	if rep.Events == nil {
		t.Fatal("expected event replay report")
	}
	if rep.Events.EventsLoaded != 2 {
		t.Fatalf("events loaded = %d, want 2", rep.Events.EventsLoaded)
	}
	if rep.Events.EventsMatched != 1 || rep.Events.EventsDelivered != 1 || rep.Events.EventsAcked != 1 {
		t.Fatalf("matched=%d delivered=%d acked=%d, want 1", rep.Events.EventsMatched, rep.Events.EventsDelivered, rep.Events.EventsAcked)
	}
	if len(rep.Events.Writes) != 3 {
		t.Fatalf("recorded writes = %d (%+v), want 3 (kv.put, viewers.setWatchState, collections.upsert)", len(rep.Events.Writes), rep.Events.Writes)
	}
	if len(rep.Events.Notifications) != 2 {
		t.Fatalf("recorded notifications = %d (%+v), want 2", len(rep.Events.Notifications), rep.Events.Notifications)
	}

	// Verify nothing was applied to live storage or live backends.
	if liveWatchWrites != 0 || liveCollectionWrites != 0 || len(liveNotices) != 0 {
		t.Fatalf("live backends were called during dry-run: watch=%d col=%d notices=%v", liveWatchWrites, liveCollectionWrites, liveNotices)
	}
	var kvCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM plugins_kv WHERE plugin = 'org.example.scrobbler'`).Scan(&kvCount)
	if kvCount != 0 {
		t.Fatalf("plugins_kv rows = %d, want 0 after dry-run event replay", kvCount)
	}
}

func TestListAndChannelReplay(t *testing.T) {
	db := store.OpenTest(t)
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })

	knownIMDb := map[string]MatchedTitle{
		"tt0816692":  {RatingKey: "1", ExternalID: "tt0816692", Title: "Interstellar", Type: "movie", Year: 2014},
		"tt0133093":  {RatingKey: "2", ExternalID: "tt0133093", Title: "The Matrix", Type: "movie", Year: 1999},
		"tt15239678": {RatingKey: "3", ExternalID: "tt15239678", Title: "Dune: Part Two", Type: "movie", Year: 2024},
	}

	var probedURLs []string
	runner := NewRunner(ReplayDeps{
		DB:     db,
		Engine: eng,
		CatalogMatch: func(_ context.Context, item ListItem) (MatchedTitle, bool) {
			m, ok := knownIMDb[item.IMDbID]
			return m, ok
		},
		ProbeStream: func(_ context.Context, rawURL string, _ map[string]string) StreamProbeResult {
			probedURLs = append(probedURLs, rawURL)
			return StreamProbeResult{
				URL:       rawURL,
				OK:        !strings.Contains(rawURL, "broken"),
				Status:    200,
				Kind:      "hls",
				LatencyMS: 12,
			}
		},
	})
	t.Cleanup(func() { _ = runner.Close() })

	ctx := context.Background()

	// 1. List replay across 2 pages (4 total items, 3 in catalogue -> 0.75 match rate).
	listManifest := json.RawMessage(`{
		"id": "org.example.curated-list",
		"version": "1.0.0",
		"name": "Sci-Fi Curated List",
		"types": ["movie"],
		"catalogs": [{ "type": "movie", "id": "scifi", "name": "Sci-Fi" }],
		"ether": {
			"protocol": 1,
			"kind": "extension",
			"summary": "Provides a curated science fiction list.",
			"capabilities": ["list_source"],
			"runtime": { "kind": "hosted", "entry": "plugin.js" },
			"contributes": {
				"listSources": [
					{ "key": "scifi", "label": "Sci-Fi classics", "catalog": "scifi", "maxItems": 100, "refresh": "1h" }
				]
			}
		}
	}`)
	listJS := `
function handle(req) {
  if (req.path.includes("skip=")) {
    return {
      status: 200,
      body: {
        metas: [
          { id: "tt15239678", type: "movie", name: "Dune: Part Two", year: 2024 },
          { id: "tt9999999", type: "movie", name: "Missing Film", year: 2025 }
        ]
      }
    };
  }
  return {
    status: 200,
    body: {
      metas: [
        { id: "tt0816692", type: "movie", name: "Interstellar", year: 2014 },
        { id: "tt0133093", type: "movie", name: "The Matrix", year: 1999 }
      ]
    }
  };
}
globalThis.handle = handle;
`
	listRep, err := runner.Replay(ctx, ReplayRequest{
		DevInstance: &DevInstance{
			Manifest: listManifest,
			Files:    map[string]string{"plugin.js": listJS},
		},
		Kind:   ReplayList,
		Params: ReplayParams{Pages: 2},
	})
	if err != nil {
		t.Fatalf("list replay failed: %v", err)
	}
	if listRep.List == nil {
		t.Fatal("expected list report")
	}
	if listRep.List.PagesFetched != 2 || listRep.List.TotalItems != 4 || listRep.List.MatchedItems != 3 {
		t.Fatalf("list report = %+v, want 2 pages, 4 total, 3 matched", listRep.List)
	}
	if listRep.List.MatchRate != 0.75 {
		t.Fatalf("match rate = %v, want 0.75", listRep.List.MatchRate)
	}
	if len(listRep.List.Preview) != 3 {
		t.Fatalf("preview len = %d, want 3", len(listRep.List.Preview))
	}

	// 2. Channel replay: 6 channels (4 with guide, 2 without), sample probe capped at 5 inputs.
	chanManifest := json.RawMessage(`{
		"id": "org.example.channels",
		"version": "1.0.0",
		"name": "Community Channels",
		"ether": {
			"protocol": 1,
			"kind": "livetv_source",
			"summary": "Provides community live television channels.",
			"capabilities": ["livetv_channels"],
			"runtime": { "kind": "hosted", "entry": "plugin.js" },
			"contributes": {
				"channels": { "refresh": "1h", "test": true }
			}
		}
	}`)
	chanJS := `
function handle(req) {
  if (req.path === "/ether/channels.json") {
    return {
      status: 200,
      body: {
        channels: [
          { id: "ch1", name: "One", url: "https://streams.example.com/1.m3u8", programs: [{ title: "News" }] },
          { id: "ch2", name: "Two", url: "https://streams.example.com/2.m3u8", programs: [{ title: "Sports" }] },
          { id: "ch3", name: "Three", url: "https://streams.example.com/3.m3u8" },
          { id: "ch4", name: "Four", url: "https://streams.example.com/broken.m3u8", hasGuide: true },
          { id: "ch5", name: "Five", url: "https://streams.example.com/5.m3u8" },
          { id: "ch6", name: "Six", url: "https://streams.example.com/6.m3u8" }
        ]
      }
    };
  }
  if (req.path === "/ether/guide/ch3.json") {
    return {
      status: 200,
      body: { programs: [{ title: "Evening Movie" }, { title: "Late Show" }] }
    };
  }
  return { status: 404, body: {} };
}
globalThis.handle = handle;
`
	chanRep, err := runner.Replay(ctx, ReplayRequest{
		DevInstance: &DevInstance{
			Manifest: chanManifest,
			Files:    map[string]string{"plugin.js": chanJS},
		},
		Kind: ReplayChannels,
	})
	if err != nil {
		t.Fatalf("channel replay failed: %v", err)
	}
	if chanRep.Channels == nil {
		t.Fatal("expected channels report")
	}
	if chanRep.Channels.ChannelCount != 6 || chanRep.Channels.WithGuideCount != 4 {
		t.Fatalf("channel count=%d withGuide=%d, want 6 and 4", chanRep.Channels.ChannelCount, chanRep.Channels.WithGuideCount)
	}
	if len(chanRep.Channels.Probes) != 5 || len(probedURLs) != 5 {
		t.Fatalf("probes len=%d, want 5 sample inputs", len(chanRep.Channels.Probes))
	}
	if chanRep.Channels.ProbesOK != 4 {
		t.Fatalf("probesOk=%d, want 4", chanRep.Channels.ProbesOK)
	}
}

func TestRankingReplayReportsManifestBudget(t *testing.T) {
	db := store.OpenTest(t)
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })
	runner := NewRunner(ReplayDeps{DB: db, Engine: eng})
	t.Cleanup(func() { _ = runner.Close() })
	manifest := json.RawMessage(`{"id":"org.example.budget","version":"1.0.0","name":"Budget","ether":{"protocol":1,"kind":"extension","summary":"Ranks releases","capabilities":["ranking_step"],"runtime":{"kind":"hosted","entry":"plugin.js"},"contributes":{"rankingSteps":[{"key":"rank","label":"Rank","budget":{"perCandidateMicros":500,"perBatchMillis":20}}]}}}`)
	report, err := runner.Replay(context.Background(), ReplayRequest{DevInstance: &DevInstance{Manifest: manifest, Files: map[string]string{"plugin.js": "globalThis.rank_key = function() { return 0; };"}}, Kind: ReplayRanking})
	if err != nil {
		t.Fatal(err)
	}
	if report.Ranking == nil || report.Ranking.Budget.PerCandidateLimitMS != 0.5 || report.Ranking.Budget.BatchLimitMS != 20 {
		t.Fatalf("budget = %+v", report.Ranking)
	}
}
