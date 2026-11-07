package builtin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/builtin"
	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/resolver"
)

type fakeEnv struct {
	guideRequests []livetv.GuideMatchRequest
	writes        int
	blocked       []string
	pinned        []string
	createdSpecs  []core.CollectionSpec
	genCalls      int
	rankings      map[string][]string
	rules         map[string][]core.ReleaseRule
	stremioURLs   []string
	m3uSources    []livetv.Source
	frontends     map[string]bool
	plugins       map[string]bool
	restarted     []bool
}

func newFakeEnv() (*fakeEnv, builtin.Deps) {
	f := &fakeEnv{
		rankings:  map[string][]string{"Default": append([]string(nil), core.DefaultRankingSteps...)},
		rules:     map[string][]core.ReleaseRule{"Default": {}},
		frontends: map[string]bool{"plex": true, "jellyfin": false},
		plugins:   map[string]bool{"subtitles-pro": true},
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	d := builtin.Deps{
		Status:      f,
		Playback:    f,
		Resolver:    resolverAdapter{f},
		Logs:        f,
		Catalog:     f,
		Collections: f,
		LiveTV:      f,
		Sources:     f,
		Plugins:     f,
		Approvals:   f,
		Profiles:    f,
		Frontends:   f,
		Server:      f,
		OwnerUser: func(ctx context.Context) (*core.User, error) {
			return &core.User{ID: 1, Username: "owner", IsOwner: true}, nil
		},
		Routes: func() []builtin.RouteInfo {
			return []builtin.RouteInfo{
				{Route: "/", Label: "Overview"},
				{Route: "/releases", Label: "Releases"},
				{Route: "/sources", Label: "Sources"},
			}
		},
		Now: func() time.Time { return now },
	}
	return f, d
}

func (f *fakeEnv) ServerStatus(ctx context.Context) (builtin.ServerStatus, error) {
	return builtin.ServerStatus{
		Version:         "1.2.3",
		ServerName:      "Home Ether",
		StartedAt:       time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		SetupComplete:   true,
		ActiveSessions:  1,
		IndexersEnabled: 2,
		IndexersTotal:   3,
		LiveChannels:    12,
	}, nil
}

func (f *fakeEnv) Health(ctx context.Context) ([]builtin.HealthCheck, error) {
	return []builtin.HealthCheck{
		{Name: "Database", Status: "ok", Detail: "Database is reachable."},
	}, nil
}

func (f *fakeEnv) History(ctx context.Context, viewerID int64, since time.Time, limit int) ([]core.HistoryEntry, error) {
	return []core.HistoryEntry{
		{
			Key:       core.MovieKey(550),
			ViewedAt:  time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC),
			Completed: false,
		},
	}, nil
}

func (f *fakeEnv) HistoryEntry(ctx context.Context, id int64) (resolver.HistoryRecord, error) {
	if id != 42 {
		return resolver.HistoryRecord{}, core.ErrNotFound
	}
	return resolver.HistoryRecord{
		ID:          42,
		At:          time.Date(2026, 11, 2, 2, 0, 0, 0, time.UTC),
		TitleKey:    core.MovieKey(550),
		TitleName:   "Heat",
		Profile:     "Default",
		ReleaseID:   "rel-bad-1",
		ReleaseName: "Heat.1995.2160p.WEB-DL.DDP5.1.H.265-BADGRP",
		Indexer:     "NZBGeek",
		Size:        18 << 30,
		Candidates:  2,
		Trace: &resolver.Trace{
			SearchDuration: 120 * time.Millisecond,
			TotalDuration:  340 * time.Millisecond,
			Candidates: []resolver.CandidateTrace{
				{
					ReleaseID:     "rel-bad-1",
					Name:          "Heat.1995.2160p.WEB-DL.DDP5.1.H.265-BADGRP",
					Indexer:       "NZBGeek",
					Score:         95,
					Size:          18 << 30,
					Height:        2160,
					Attempted:     true,
					HealthChecked: true,
					Healthy:       true,
				},
				{
					ReleaseID:     "rel-good-2",
					Name:          "Heat.1995.1080p.BluRay.x264-SPARKS",
					Indexer:       "DrunkenSlug",
					Score:         88,
					Size:          10 << 30,
					Height:        1080,
					Attempted:     false,
					HealthChecked: true,
					Healthy:       true,
				},
			},
		},
	}, nil
}

func (f *fakeEnv) ReleaseHealth(ctx context.Context, releaseID string, titleKey core.RatingKey) (builtin.ReleaseHealth, error) {
	return builtin.ReleaseHealth{
		Known:     releaseID == "rel-bad-1" || releaseID == "rel-good-2",
		Blocked:   false,
		Pinned:    false,
		Status:    "unhealthy",
		Reason:    "missing articles during playback",
		CheckedAt: time.Date(2026, 11, 2, 2, 5, 0, 0, time.UTC),
	}, nil
}

func (f *fakeEnv) LookupRelease(id string) (core.Release, bool) {
	switch id {
	case "rel-bad-1":
		return core.Release{
			ID:      "rel-bad-1",
			Name:    "Heat.1995.2160p.WEB-DL.DDP5.1.H.265-BADGRP",
			Indexer: "NZBGeek",
			Size:    18 << 30,
		}, true
	case "rel-good-2":
		return core.Release{
			ID:      "rel-good-2",
			Name:    "Heat.1995.1080p.BluRay.x264-SPARKS",
			Indexer: "DrunkenSlug",
			Size:    10 << 30,
		}, true
	default:
		return core.Release{}, false
	}
}

func (f *fakeEnv) Block(ctx context.Context, req resolver.BlockRequest, reason string) (resolver.BlockEntry, error) {
	f.writes++
	f.blocked = append(f.blocked, req.ReleaseID)
	return resolver.BlockEntry{ReleaseID: req.ReleaseID, Reason: reason}, nil
}

func (f *fakeEnv) PinRelease(ctx context.Context, titleKey core.RatingKey, releaseID, note string) error {
	f.writes++
	f.pinned = append(f.pinned, releaseID)
	return nil
}

type resolverAdapter struct {
	*fakeEnv
}

func (r resolverAdapter) History(ctx context.Context, flt resolver.HistoryFilter) ([]resolver.HistoryRecord, int, error) {
	rec, err := r.fakeEnv.HistoryEntry(ctx, 42)
	if err != nil {
		return nil, 0, err
	}
	if flt.TitleKey != 0 && flt.TitleKey != rec.TitleKey {
		return nil, 0, nil
	}
	return []resolver.HistoryRecord{rec}, 1, nil
}

func (f *fakeEnv) Tail(limit int, level, query string) []builtin.LogLine {
	return []builtin.LogLine{
		{
			Time:    time.Date(2026, 11, 2, 2, 4, 0, 0, time.UTC),
			Level:   "warn",
			Message: "article missing on provider X-Plex-Token=supersecret123",
			Attrs:   map[string]string{"Authorization": "Bearer ethg_secret_token_value", "password": "plain-password-value"},
		},
	}
}

func (f *fakeEnv) Search(ctx context.Context, query string, types []core.MediaType, limit int) ([]core.Title, error) {
	return []core.Title{
		{Key: core.MovieKey(550), Type: core.TypeMovie, Title: "Heat", Year: 1995, Rating: 8.3},
	}, nil
}

func (f *fakeEnv) Get(ctx context.Context, key core.RatingKey) (*core.Title, error) {
	if key != core.MovieKey(550) {
		return nil, core.ErrNotFound
	}
	return &core.Title{
		Key:           core.MovieKey(550),
		Type:          core.TypeMovie,
		Title:         "Heat",
		Year:          1995,
		Summary:       "A group of professional bank robbers start to feel the heat from LAPD.",
		Genres:        []string{"Action", "Crime", "Drama"},
		Runtime:       170 * time.Minute,
		ContentRating: "R",
		Rating:        8.3,
	}, nil
}

func (f *fakeEnv) SnapshotList(ctx context.Context, offset, limit int) ([]core.Collection, error) {
	return []core.Collection{
		{Key: core.CollectionKey(10), Title: "90s Thrillers", Kind: "smart", Type: core.TypeMovie, ChildCount: 14},
	}, nil
}

func (f *fakeEnv) SnapshotItems(ctx context.Context, key core.RatingKey, offset, limit int) (core.Page, error) {
	if key != core.CollectionKey(10) {
		return core.Page{}, core.ErrNotFound
	}
	return core.Page{
		Total: 1,
		Items: []core.Title{{Key: core.MovieKey(550), Type: core.TypeMovie, Title: "Heat", Year: 1995}},
	}, nil
}

func (f *fakeEnv) Generate(ctx context.Context, prompt, mediaType string) (*core.CollectionSpec, string, error) {
	f.genCalls++
	return &core.CollectionSpec{
		ID:          "90s-action",
		Name:        "90s Action",
		Description: prompt,
		MediaType:   "movie",
	}, "Generated from prompt", nil
}

func (f *fakeEnv) Draft(ctx context.Context, raw, mediaType string) (*core.CollectionSpec, []string, error) {
	var sp core.CollectionSpec
	if err := json.Unmarshal([]byte(raw), &sp); err != nil {
		return nil, nil, err
	}
	return &sp, nil, nil
}

func (f *fakeEnv) Preview(ctx context.Context, sp core.CollectionSpec, n int) (*core.CollectionPreview, error) {
	return &core.CollectionPreview{Total: 1, Items: []core.Title{{Key: core.MovieKey(550), Type: core.TypeMovie, Title: "Heat", Year: 1995}}}, nil
}

func (f *fakeEnv) CreateSpec(ctx context.Context, sp core.CollectionSpec) (*core.CollectionSpec, error) {
	f.writes++
	f.createdSpecs = append(f.createdSpecs, sp)
	return &sp, nil
}

func (f *fakeEnv) Channels(ctx context.Context) ([]livetv.Channel, error) {
	return []livetv.Channel{
		{
			ID:            "ch-1",
			Number:        "101",
			Name:          "News HD",
			Kind:          "m3u",
			Group:         "News",
			Enabled:       true,
			StreamSources: []livetv.StreamSource{{ID: "ss-1"}},
		},
	}, nil
}

func (f *fakeEnv) StreamSourceStatuses(ctx context.Context, channelID string) ([]livetv.StreamSourceStatus, error) {
	return []livetv.StreamSourceStatus{
		{ID: "ss-1", State: "cooldown"},
	}, nil
}

func (f *fakeEnv) ImportSource(ctx context.Context, src livetv.Source) (livetv.Source, error) {
	f.writes++
	src.ID = "src-1"
	src.ChannelCount = 45
	f.m3uSources = append(f.m3uSources, src)
	return src, nil
}

func (f *fakeEnv) Sources(ctx context.Context) ([]builtin.SourceInfo, error) {
	return []builtin.SourceInfo{
		{Name: "NZBGeek", Kind: "newznab", Class: "indexer", Enabled: true, Host: "api.nzbgeek.info", Health: "ok"},
	}, nil
}

func (f *fakeEnv) AddStremio(ctx context.Context, addonURL string) (builtin.SourceInfo, error) {
	f.writes++
	f.stremioURLs = append(f.stremioURLs, addonURL)
	return builtin.SourceInfo{Name: "addon.example", Kind: "stremio", Enabled: true}, nil
}

func (f *fakeEnv) Plugins(ctx context.Context) ([]builtin.PluginInfo, error) {
	return []builtin.PluginInfo{
		{ID: "subtitles-pro", Name: "Subtitles Pro", Version: "1.0.0", Kind: "subtitle", Enabled: f.plugins["subtitles-pro"], Health: "ok"},
	}, nil
}

func (f *fakeEnv) PluginHealth(ctx context.Context, id string) (builtin.PluginInfo, error) {
	if id != "subtitles-pro" {
		return builtin.PluginInfo{}, core.ErrNotFound
	}
	return builtin.PluginInfo{ID: id, Name: "Subtitles Pro", Version: "1.0.0", Enabled: f.plugins[id], Health: "ok"}, nil
}

func (f *fakeEnv) SetPluginEnabled(ctx context.Context, id string, enabled bool) error {
	f.writes++
	f.plugins[id] = enabled
	return nil
}

func (f *fakeEnv) Approval(ctx context.Context, id string) (builtin.ApprovalInfo, error) {
	if id != "ap-1" {
		return builtin.ApprovalInfo{}, builtin.ErrApprovalNotFound
	}
	return builtin.ApprovalInfo{
		ID:          "ap-1",
		Tool:        "release_blocklist",
		Category:    "Blocklist a release",
		Description: "Never pick Heat.1995.2160p again.",
		Status:      "pending",
		CreatedAt:   time.Date(2026, 11, 2, 2, 10, 0, 0, time.UTC),
	}, nil
}

func (f *fakeEnv) Profiles(ctx context.Context) ([]builtin.ProfileInfo, error) {
	return []builtin.ProfileInfo{
		{
			Name:    "Default",
			Ranking: f.rankings["Default"],
			Rules:   f.rules["Default"],
		},
	}, nil
}

func (f *fakeEnv) SetRanking(ctx context.Context, profile, media string, ranking []string) error {
	f.writes++
	f.rankings[profile] = append([]string(nil), ranking...)
	return nil
}

func (f *fakeEnv) AddRule(ctx context.Context, profile, media string, rule core.ReleaseRule) error {
	f.writes++
	f.rules[profile] = append(f.rules[profile], rule)
	return nil
}

func (f *fakeEnv) Frontends(ctx context.Context) ([]builtin.FrontendInfo, error) {
	return []builtin.FrontendInfo{
		{ID: "plex", Name: "Plex", Enabled: f.frontends["plex"]},
		{ID: "jellyfin", Name: "Jellyfin", Enabled: f.frontends["jellyfin"]},
	}, nil
}

func (f *fakeEnv) SetFrontendEnabled(ctx context.Context, id string, enabled bool) error {
	f.writes++
	f.frontends[id] = enabled
	return nil
}

func (f *fakeEnv) Restart(ctx context.Context, whenIdle bool) error {
	f.writes++
	f.restarted = append(f.restarted, whenIdle)
	return nil
}

func buildDeps() (*fakeEnv, builtin.Deps) {
	return newFakeEnv()
}

func callTool(t *testing.T, cat *tools.Catalogue, ctx context.Context, p tools.Principal, name string, input map[string]any) (tools.Result, error) {
	t.Helper()
	tl, ok := cat.Lookup(ctx, p, name)
	if !ok {
		t.Fatalf("tool %q not visible to principal %+v", name, p)
	}
	return tl.Execute(ctx, p, input)
}

func TestBuiltinCatalogueAndSchemaInvariants(t *testing.T) {
	_, d := buildDeps()
	all := builtin.All(d)
	if len(all) != 35 {
		t.Fatalf("expected 35 built-in tools, got %d", len(all))
	}

	wantRead := []string{
		"server_status", "health_details", "playback_history", "resolve_trace",
		"release_health", "logs_tail", "catalog_search", "title_details",
		"collection_list", "collection_preview", "collection_draft", "livetv_channels", "sources_list",
		"plugins_list", "plugin_health", "approval_status", "navigate",
		"web_search", "web_fetch", "look_draft", "livetv_look_draft", "channel_summary",
	}
	wantWrite := []string{
		"livetv_guide_match",
		"collection_create_from_prompt", "release_blocklist", "release_pin",
		"ranking_reorder", "rule_add", "source_add_stremio", "source_add_m3u",
		"frontend_enable", "plugin_enable", "server_restart", "livetv_look_save", "livetv_channel_look",
	}

	byName := map[string]tools.Tool{}
	for _, tl := range all {
		byName[tl.Name] = tl
		if tl.Audience != tools.AudienceOwner {
			t.Errorf("%s: Audience = %q, want owner", tl.Name, tl.Audience)
		}
		if tl.Owner != "builtin" {
			t.Errorf("%s: Owner = %q, want builtin", tl.Name, tl.Owner)
		}
		if tl.Title == "" || tl.Description == "" {
			t.Errorf("%s: empty Title or Description", tl.Name)
		}
		var sch map[string]any
		if err := json.Unmarshal(tl.InputSchema, &sch); err != nil {
			t.Fatalf("%s: invalid InputSchema JSON: %v", tl.Name, err)
		}
		if sch["type"] != "object" || sch["additionalProperties"] != false {
			t.Errorf("%s: top-level schema must have type=object and additionalProperties=false, got %v", tl.Name, sch)
		}
		var checkArrays func(map[string]any)
		checkArrays = func(node map[string]any) {
			if node["type"] == "array" {
				max, ok := node["maxItems"].(float64)
				if !ok || max < 1 || max > 200 {
					t.Errorf("%s: unbounded array: %v", tl.Name, node)
				}
			}
			if item, ok := node["items"].(map[string]any); ok {
				checkArrays(item)
			}
			if props, ok := node["properties"].(map[string]any); ok {
				for _, value := range props {
					if child, ok := value.(map[string]any); ok {
						checkArrays(child)
					}
				}
			}
		}
		checkArrays(sch)
		props, _ := sch["properties"].(map[string]any)
		if ps, ok := props["pageSize"].(map[string]any); ok {
			if mx, _ := ps["maximum"].(float64); mx > 50 || mx < 1 {
				t.Errorf("%s: pageSize maximum = %v, want 1..50", tl.Name, mx)
			}
		}
	}

	for _, name := range wantRead {
		tl, ok := byName[name]
		if !ok {
			t.Errorf("missing read tool %q", name)
			continue
		}
		if !tl.ReadOnly {
			t.Errorf("read tool %q has ReadOnly=false", name)
		}
	}
	for _, name := range wantWrite {
		tl, ok := byName[name]
		if !ok {
			t.Errorf("missing write tool %q", name)
			continue
		}
		if tl.ReadOnly {
			t.Errorf("write tool %q has ReadOnly=true", name)
		}
	}

	cat := tools.NewCatalogue()
	if err := cat.Register(all...); err != nil {
		t.Fatalf("Catalogue.Register failed: %v", err)
	}
}

func TestReadToolsNeverMutate(t *testing.T) {
	ctx := context.Background()
	f, d := buildDeps()
	cat := tools.NewCatalogue()
	if err := cat.Register(builtin.All(d)...); err != nil {
		t.Fatal(err)
	}
	p := tools.Principal{ViewerID: 1, IsOwner: true, Surface: "console"}

	inputs := map[string]map[string]any{
		"server_status":      {},
		"health_details":     {},
		"playback_history":   {"page": 1, "pageSize": 10, "sinceHours": 24},
		"resolve_trace":      {"titleKey": fmt.Sprint(int64(core.MovieKey(550)))},
		"release_health":     {"releaseId": "rel-bad-1", "titleKey": fmt.Sprint(int64(core.MovieKey(550)))},
		"logs_tail":          {"lines": 20, "level": "warn"},
		"catalog_search":     {"query": "Heat", "type": "movie", "limit": 5},
		"title_details":      {"titleKey": fmt.Sprint(int64(core.MovieKey(550)))},
		"collection_list":    {},
		"collection_preview": {"collectionKey": fmt.Sprint(int64(core.CollectionKey(10))), "limit": 5},
		"livetv_channels":    {"onlyFailing": true},
		"sources_list":       {},
		"plugins_list":       {},
		"plugin_health":      {"pluginId": "subtitles-pro"},
		"approval_status":    {"approvalId": "ap-1"},
		"navigate":           {"route": "/releases"},
	}

	for name, in := range inputs {
		res, err := callTool(t, cat, ctx, p, name, in)
		if err != nil {
			t.Fatalf("read tool %s failed: %v", name, err)
		}
		if res.Proposal != nil || res.Secret != nil {
			t.Fatalf("read tool %s returned Proposal or Secret", name)
		}
		if f.writes != 0 {
			t.Fatalf("read tool %s mutated state (writes=%d)", name, f.writes)
		}
	}

	// Verify logs_tail redacted secrets in both message and attrs.
	res, err := callTool(t, cat, ctx, p, "logs_tail", inputs["logs_tail"])
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(res.Data)
	if strings.Contains(string(encoded), "supersecret123") || strings.Contains(string(encoded), "ethg_secret_token_value") || strings.Contains(string(encoded), "plain-password-value") {
		t.Fatalf("logs_tail leaked secret: %s", string(encoded))
	}

	// Verify navigate with no route returns known routes.
	navList, err := callTool(t, cat, ctx, p, "navigate", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := navList.Data["routes"]; !ok {
		t.Fatalf("expected routes list from navigate({}), got %+v", navList.Data)
	}
}

func TestWriteToolsProposeBeforeMutating(t *testing.T) {
	ctx := context.Background()
	f, d := buildDeps()
	cat := tools.NewCatalogue()
	if err := cat.Register(builtin.All(d)...); err != nil {
		t.Fatal(err)
	}
	p := tools.Principal{ViewerID: 1, IsOwner: true, Surface: "console"}

	reordered := append([]string(nil), core.DefaultRankingSteps...)
	reordered[0], reordered[1] = reordered[1], reordered[0]

	cases := []struct {
		name  string
		input map[string]any
		check func(t *testing.T)
	}{
		{
			name:  "collection_create_from_prompt",
			input: map[string]any{"prompt": "90s action movies rated above 7", "mediaType": "movie"},
			check: func(t *testing.T) {
				if len(f.createdSpecs) != 1 || f.createdSpecs[0].Name != "90s Action" {
					t.Fatalf("expected created collection spec, got %+v", f.createdSpecs)
				}
			},
		},
		{
			name:  "release_blocklist",
			input: map[string]any{"releaseId": "rel-bad-1", "reason": "buffered last night"},
			check: func(t *testing.T) {
				if len(f.blocked) != 1 || f.blocked[0] != "rel-bad-1" {
					t.Fatalf("expected rel-bad-1 blocked, got %v", f.blocked)
				}
			},
		},
		{
			name:  "release_pin",
			input: map[string]any{"titleKey": fmt.Sprint(int64(core.MovieKey(550))), "releaseId": "rel-good-2", "note": "clean BluRay"},
			check: func(t *testing.T) {
				if len(f.pinned) != 1 || f.pinned[0] != "rel-good-2" {
					t.Fatalf("expected rel-good-2 pinned, got %v", f.pinned)
				}
			},
		},
		{
			name:  "ranking_reorder",
			input: map[string]any{"profile": "Default", "ranking": reordered},
			check: func(t *testing.T) {
				if f.rankings["Default"][0] != reordered[0] {
					t.Fatalf("expected reordered ranking, got %v", f.rankings["Default"])
				}
			},
		},
		{
			name: "rule_add",
			input: map[string]any{
				"profile": "Default",
				"name":    "Avoid BADGRP",
				"action":  "avoid",
				"match":   "all",
				"conditions": []any{
					map[string]any{"field": "group", "operator": "equals", "value": "BADGRP"},
				},
			},
			check: func(t *testing.T) {
				if len(f.rules["Default"]) != 1 || f.rules["Default"][0].Name != "Avoid BADGRP" {
					t.Fatalf("expected rule added, got %+v", f.rules["Default"])
				}
			},
		},
		{
			name:  "source_add_m3u",
			input: map[string]any{"name": "Antenna", "playlistUrl": "https://tv.example/lineup.m3u", "guideUrl": "https://tv.example/epg.xml"},
			check: func(t *testing.T) {
				if len(f.m3uSources) != 1 || f.m3uSources[0].Name != "Antenna" {
					t.Fatalf("expected m3u source imported, got %+v", f.m3uSources)
				}
			},
		},
		{
			name:  "frontend_enable",
			input: map[string]any{"frontendId": "jellyfin", "enabled": true},
			check: func(t *testing.T) {
				if !f.frontends["jellyfin"] {
					t.Fatal("expected jellyfin enabled")
				}
			},
		},
		{
			name:  "plugin_enable",
			input: map[string]any{"pluginId": "subtitles-pro", "enabled": false},
			check: func(t *testing.T) {
				if f.plugins["subtitles-pro"] {
					t.Fatal("expected subtitles-pro disabled")
				}
			},
		},
		{
			name:  "server_restart",
			input: map[string]any{"whenIdle": true},
			check: func(t *testing.T) {
				if len(f.restarted) != 1 || !f.restarted[0] {
					t.Fatalf("expected restart(whenIdle=true), got %v", f.restarted)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			beforeWrites := f.writes
			res, err := callTool(t, cat, ctx, p, tc.name, tc.input)
			if err != nil {
				t.Fatalf("callTool(%s) failed: %v", tc.name, err)
			}
			if res.Proposal == nil {
				t.Fatalf("callTool(%s) did not return a Proposal", tc.name)
			}
			if f.writes != beforeWrites {
				t.Fatalf("callTool(%s) mutated state before approval", tc.name)
			}
			applied, err := res.Proposal.Apply(ctx)
			if err != nil {
				t.Fatalf("Proposal.Apply(%s) failed: %v", tc.name, err)
			}
			if applied.Data == nil {
				t.Fatalf("Proposal.Apply(%s) returned nil Data", tc.name)
			}
			if f.writes != beforeWrites+1 {
				t.Fatalf("Proposal.Apply(%s) did not record 1 write (got %d -> %d)", tc.name, beforeWrites, f.writes)
			}
			tc.check(t)
		})
	}

	// Verify collection_create_from_prompt re-execution from stored Proposal.Input does not call Generate again.
	t.Run("collection_create_from_prompt_stored_spec", func(t *testing.T) {
		res, err := callTool(t, cat, ctx, p, "collection_create_from_prompt", map[string]any{"prompt": "90s action"})
		if err != nil {
			t.Fatal(err)
		}
		genBefore := f.genCalls
		res2, err := callTool(t, cat, ctx, p, "collection_create_from_prompt", res.Proposal.Input)
		if err != nil {
			t.Fatal(err)
		}
		if f.genCalls != genBefore {
			t.Fatalf("expected stored spec re-execution not to call Generate again (%d -> %d)", genBefore, f.genCalls)
		}
		if res2.Proposal == nil {
			t.Fatal("expected proposal from stored spec")
		}
	})

	// Verify source_add_stremio uses SecretRequest and never mutates until Secret.Store is called.
	t.Run("source_add_stremio", func(t *testing.T) {
		beforeWrites := f.writes
		res, err := callTool(t, cat, ctx, p, "source_add_stremio", map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		if res.Secret == nil {
			t.Fatal("expected SecretRequest from source_add_stremio")
		}
		if f.writes != beforeWrites {
			t.Fatal("source_add_stremio mutated state before Secret.Store")
		}
		if err := res.Secret.Store(ctx, "not-a-url"); err == nil {
			t.Fatal("expected invalid URL error")
		}
		if err := res.Secret.Store(ctx, "https://torrentio.example/manifest.json"); err != nil {
			t.Fatalf("Secret.Store failed: %v", err)
		}
		if len(f.stremioURLs) != 1 || f.stremioURLs[0] != "https://torrentio.example/manifest.json" {
			t.Fatalf("unexpected stremio URLs: %v", f.stremioURLs)
		}
	})
}

// The research tools never reach the owner's own network.
func TestWebFetchRefusesPrivateAddresses(t *testing.T) {
	_, d := buildDeps()
	for _, tl := range builtin.All(d) {
		if tl.Name != "web_fetch" {
			continue
		}
		for _, target := range []string{"http://127.0.0.1:32400/", "http://169.254.169.254/latest/meta-data/", "file:///etc/passwd"} {
			if _, err := tl.Execute(context.Background(), tools.Principal{IsOwner: true}, map[string]any{"url": target}); err == nil {
				t.Errorf("fetched %s", target)
			}
		}
		return
	}
	t.Fatal("web_fetch missing")
}

// External MCP apps have their own web access; the research tools are Ask
// Ether's only.
func TestWebToolsHiddenFromMCP(t *testing.T) {
	_, d := buildDeps()
	mcp := tools.Principal{IsOwner: true, Surface: "mcp", Scopes: []string{"full"}}
	console := tools.Principal{IsOwner: true, Surface: "console"}
	for _, tl := range builtin.All(d) {
		if tl.Name != "web_search" && tl.Name != "web_fetch" {
			continue
		}
		if tl.Visible == nil || tl.Visible(context.Background(), mcp) || !tl.Visible(context.Background(), console) {
			t.Errorf("%s visibility wrong", tl.Name)
		}
	}
}

func (f *fakeEnv) MatchGuides(ctx context.Context, req livetv.GuideMatchRequest) (livetv.GuideMatchResult, error) {
	f.guideRequests = append(f.guideRequests, req)
	return livetv.GuideMatchResult{AI: &livetv.GuideAIStats{Configured: true, EstimatedCalls: 1}, Changes: []livetv.GuideMatchChange{{ChannelID: "c1", ChannelName: "News", GuideName: "News HD", Method: "ai", Confidence: .98, ProposalKey: "key1"}}}, nil
}
func TestGuideMatchingApproval(t *testing.T) {
	f, d := buildDeps()
	var tl tools.Tool
	for _, v := range builtin.All(d) {
		if v.Name == "livetv_guide_match" {
			tl = v
		}
	}
	ctx := context.Background()
	r, e := tl.Execute(ctx, tools.Principal{}, map[string]any{"operation": "apply", "channelIds": []string{"c1"}, "proposalKeys": map[string]string{"c1": "key1"}})
	if e != nil {
		t.Fatal(e)
	}
	if r.Proposal == nil || len(r.Proposal.Details) != 1 {
		t.Fatal("missing concrete approval")
	}
	if len(f.guideRequests) != 1 || f.guideRequests[0].AI != "estimate" {
		t.Fatal("mapping ran before approval")
	}
	if _, e = r.Proposal.Apply(ctx); e != nil {
		t.Fatal(e)
	}
	req := f.guideRequests[1]
	if req.AI != "apply" || !req.Reviewed || req.ProposalKeys["c1"] != "key1" {
		t.Fatalf("incorrect approved request %+v", req)
	}
}
