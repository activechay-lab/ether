package adapters_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/braedonsaunders/ether/internal/newznab"
	"github.com/braedonsaunders/ether/internal/plugins/remotetest"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/debrid"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/plugins/remote"
	"github.com/braedonsaunders/ether/internal/resolver"
	"github.com/braedonsaunders/ether/internal/store"
	sdk "github.com/braedonsaunders/ether/sdk/go"
)

type fakePluginRuntime struct {
	mu      sync.Mutex
	calls   []plugins.Request
	handler func(ctx context.Context, req plugins.Request) (plugins.Response, error)
}

func (f *fakePluginRuntime) Call(ctx context.Context, req plugins.Request) (plugins.Response, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	h := f.handler
	f.mu.Unlock()
	if h != nil {
		return h(ctx, req)
	}
	return plugins.Response{Status: 200, Body: json.RawMessage(`{}`)}, nil
}

func (f *fakePluginRuntime) Close() error { return nil }

type streamTestEnv struct {
	t       *testing.T
	mgr     *plugins.Manager
	reg     *extend.Registry
	sources *adapters.StreamSources
	engine  *adapters.Engine
	media   *debrid.Engine
	debSvc  *debrid.Service
	debIdx  *debrid.Indexer
	res     *resolver.Service
	rts     map[string]*fakePluginRuntime
	mu      sync.Mutex
	nzb     *newznab.Manager
}

// syncRows keeps one add-on indexer row per stream-capable plugin, as the
// app does on every plugin sync.
func (e *streamTestEnv) syncRows(ctx context.Context) {
	recs, err := e.mgr.List(ctx)
	if err != nil {
		return
	}
	var rows []config.Indexer
	for _, r := range recs {
		stream := r.Manifest.Ether != nil && r.Manifest.HasCapability(plugins.CapStreamSource)
		for _, c := range r.Manifest.AddonRoles() {
			stream = stream || c == plugins.CapStreamSource
		}
		if stream {
			rows = append(rows, config.Indexer{ID: "addon-" + r.ID, Name: r.Name, Kind: config.IndexerKindAddon, Plugin: r.ID, Enabled: true})
		}
	}
	e.nzb.Reload(adapters.ExpandAddonIndexers(ctx, e.mgr, rows))
}

func newStreamTestEnv(t *testing.T) *streamTestEnv {
	t.Helper()
	db := store.OpenTest(t)
	reg := extend.New()
	cat := tools.NewCatalogue()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	env := &streamTestEnv{
		t:   t,
		reg: reg,
		rts: map[string]*fakePluginRuntime{},
	}

	mgr, err := plugins.New(plugins.Deps{
		DB:       db,
		Registry: reg,
		Tools:    cat,
		Logger:   log,
	})
	if err != nil {
		t.Fatal(err)
	}
	factory := func(inst plugins.Installed) (plugins.Runtime, error) {
		env.mu.Lock()
		rt := env.rts[inst.ID]
		if rt == nil {
			rt = &fakePluginRuntime{}
			env.rts[inst.ID] = rt
		}
		env.mu.Unlock()
		return rt, nil
	}
	mgr.RegisterRuntime("hosted", factory)
	mgr.RegisterRuntime("remote", factory)
	if err := mgr.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	env.mgr = mgr

	cfg := config.Config{
		Quality: config.Quality{
			Default: "4K",
			Profiles: []core.QualityProfile{
				{
					Name:             "4K",
					MaxHeight:        2160,
					MinHeight:        720,
					AllowHDR:         true,
					AllowDolbyVision: true,
					QualityOrder:     config.ResolutionGroups(2160, 720),
				},
			},
		},
	}
	debSvc := debrid.NewService(cfg.Debrid, debrid.Options{DB: db, Logger: log})
	// Add-on streams are indexer rows in the search manager, as in the app.
	var ss *adapters.StreamSources
	nzb := newznab.NewManager(db, nil, newznab.Options{Logger: log, Backend: func(ix config.Indexer) (newznab.Backend, error) {
		return ss.Backend(ix)
	}})
	ss = adapters.NewStreamSources(nzb)
	ss.Bind(mgr, debSvc)
	env.nzb = nzb
	mgr.OnSync(func(ctx context.Context, _ plugins.Record) { env.syncRows(ctx) })
	mgr.OnRemove(func(ctx context.Context, _ string) { env.syncRows(ctx) })
	eng := ss.WrapEngine(nil)
	media := debrid.NewEngine(debSvc, debrid.EngineOptions{
		Inner:  eng,
		Health: eng,
		Logger: log,
	})
	t.Cleanup(func() { media.Close() })

	debIdx := debrid.NewIndexer(ss, debSvc)
	debIdx.SetUsenetReady(func() bool {
		return ss.HasEnabledPlugins(context.Background())
	})
	res := resolver.New(debIdx, media, db, cfg, resolver.Options{Logger: log})
	t.Cleanup(func() { res.Close() })

	env.sources = ss
	env.engine = eng
	env.media = media
	env.debSvc = debSvc
	env.debIdx = debIdx
	env.res = res
	return env
}

func (e *streamTestEnv) installSourcePlugin(
	id, name, class string,
	foldersPerm string,
	limits map[string]any,
	handler func(ctx context.Context, req plugins.Request) (plugins.Response, error),
) plugins.Record {
	e.t.Helper()
	e.mu.Lock()
	e.rts[id] = &fakePluginRuntime{handler: handler}
	e.mu.Unlock()

	srcMeta := map[string]any{"class": class}
	if limits != nil {
		srcMeta["limits"] = limits
	}
	perms := map[string]any{}
	if foldersPerm != "" {
		perms["folders"] = foldersPerm
	}
	man := map[string]any{
		"id":      id,
		"version": "1.0.0",
		"name":    name,
		"ether": map[string]any{
			"protocol":     1,
			"kind":         "source",
			"summary":      "Provides streams for titles",
			"capabilities": []string{"stream_source"},
			"runtime":      map[string]any{"kind": "hosted", "entry": "dist/plugin.js"},
			"permissions":  perms,
			"contributes": map[string]any{
				"source": srcMeta,
			},
		},
	}
	raw, err := json.Marshal(man)
	if err != nil {
		e.t.Fatal(err)
	}
	prev, err := e.mgr.PreviewInstall(context.Background(), plugins.Source{
		Kind:     "studio",
		Manifest: raw,
		Files:    map[string][]byte{"dist/plugin.js": []byte("// plugin")},
	})
	if err != nil {
		e.t.Fatal(err)
	}
	rec, err := e.mgr.Install(context.Background(), prev, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		e.t.Fatal(err)
	}
	return rec
}

func TestStreamSourceSearchRanksByOwnerLadderAndPlays(t *testing.T) {
	env := newStreamTestEnv(t)
	ctx := context.Background()

	// Create a real local file to back the lazy /ether/open resolution so we
	// can verify the winning release both ranks by Ether's ladder and opens.
	mediaDir := t.TempDir()
	winPath := filepath.Join(mediaDir, "Arrival.2016.2160p.UHD.BluRay.REMUX.HEVC.TrueHD.7.1.Atmos-GRP.mkv")
	payloadBytes := bytesRepeat("ETHER-VIDEO-STREAM-2160P-", 1024)
	if err := os.WriteFile(winPath, payloadBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	// The plugin deliberately returns the 720p release FIRST and the 2160p
	// Remux SECOND. Ether must ignore the plugin's ordering, parse both via
	// releaseparse, and rank the 2160p Remux first on a 4K-capable client.
	rec := env.installSourcePlugin(
		"community.example.fixture",
		"Fixture Source",
		"direct",
		"read",
		nil,
		func(ctx context.Context, req plugins.Request) (plugins.Response, error) {
			switch {
			case strings.HasPrefix(req.Path, "/stream/movie/"):
				body, _ := json.Marshal(map[string]any{
					"streams": []map[string]any{
						{
							"name": "Fixture 720p",
							"ether": map[string]any{
								"id":          "low-720p",
								"releaseName": "Arrival.2016.720p.WEBRip.x264.AAC-LOW",
								"size":        int64(2_500_000_000),
								"languages":   []string{"en"},
								"lazy":        true,
							},
						},
						{
							"name": "Fixture 4K",
							"ether": map[string]any{
								"id":          "uhd-2160p",
								"releaseName": "Arrival.2016.2160p.UHD.BluRay.REMUX.HEVC.TrueHD.7.1.Atmos-GRP",
								"size":        int64(45_000_000_000),
								"languages":   []string{"en", "fr"},
								"lazy":        true,
							},
						},
					},
				})
				return plugins.Response{Status: 200, Body: body}, nil
			case req.Path == "/ether/open/uhd-2160p":
				body, _ := json.Marshal(map[string]any{
					"file": winPath,
				})
				return plugins.Response{Status: 200, Body: body}, nil
			case req.Path == "/ether/open/low-720p":
				body, _ := json.Marshal(map[string]any{
					"file": winPath,
				})
				return plugins.Response{Status: 200, Body: body}, nil
			default:
				return plugins.Response{Status: 404}, nil
			}
		},
	)
	if err := env.mgr.SetGrants(ctx, rec.ID, plugins.Grants{Folders: []string{mediaDir}}); err != nil {
		t.Fatal(err)
	}

	title := &core.Title{
		Key:     core.MovieKey(329865),
		Type:    core.TypeMovie,
		Title:   "Arrival",
		Year:    2016,
		Runtime: 116 * time.Minute,
		IDs:     core.IDs{IMDB: "tt2543164", TMDB: 329865},
	}

	res, err := env.res.Resolve(ctx, core.ResolveRequest{
		Title: title,
		Client: core.ClientCaps{
			MaxHeight:   2160,
			HDR10:       true,
			DolbyVision: true,
			VideoCodecs: []string{"h264", "hevc"},
			AudioCodecs: []string{"truehd", "eac3", "ac3", "aac"},
		},
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if !strings.Contains(res.Release.Name, "2160p") {
		t.Fatalf("expected 2160p release to win Ether's ladder, got %q", res.Release.Name)
	}
	if len(res.Release.Info.Languages) < 2 {
		t.Fatalf("expected ether.languages merged into ReleaseInfo, got %v", res.Release.Info.Languages)
	}

	src, err := env.media.Open(ctx, res.Release.ID, res.NZB)
	if err != nil {
		t.Fatalf("Open winning release failed: %v", err)
	}
	defer src.Close() //nolint:errcheck

	buf := make([]byte, len(payloadBytes))
	n, err := src.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("ReadAt failed: %v", err)
	}
	if string(buf[:n]) != string(payloadBytes) {
		t.Fatalf("unexpected played bytes: got %d bytes", n)
	}
}

func TestLazyStreamTimeoutFallsBackToNextCandidate(t *testing.T) {
	env := newStreamTestEnv(t)
	ctx := context.Background()

	mediaDir := t.TempDir()
	backupPath := filepath.Join(mediaDir, "Dune.2021.1080p.BluRay.x264.DTS-BACKUP.mkv")
	if err := os.WriteFile(backupPath, bytesRepeat("BACKUP-STREAM-", 512), 0o600); err != nil {
		t.Fatal(err)
	}

	rec := env.installSourcePlugin(
		"community.example.lazy",
		"Lazy Source",
		"direct",
		"read",
		nil,
		func(ctx context.Context, req plugins.Request) (plugins.Response, error) {
			switch {
			case strings.HasPrefix(req.Path, "/stream/movie/"):
				body, _ := json.Marshal(map[string]any{
					"streams": []map[string]any{
						{
							"name": "Stalling 4K",
							"ether": map[string]any{
								"id":          "stall-4k",
								"releaseName": "Dune.2021.2160p.UHD.BluRay.REMUX.HEVC.TrueHD.7.1.Atmos-STALL",
								"size":        int64(50_000_000_000),
								"lazy":        true,
							},
						},
						{
							"name": "Working 1080p",
							"ether": map[string]any{
								"id":          "fast-1080p",
								"releaseName": "Dune.2021.1080p.BluRay.x264.DTS-BACKUP",
								"size":        int64(12_000_000_000),
								"lazy":        true,
							},
						},
					},
				})
				return plugins.Response{Status: 200, Body: body}, nil
			case req.Path == "/ether/open/stall-4k":
				// Block until the 3s /ether/open deadline expires.
				<-ctx.Done()
				return plugins.Response{}, ctx.Err()
			case req.Path == "/ether/open/fast-1080p":
				body, _ := json.Marshal(map[string]any{"file": backupPath})
				return plugins.Response{Status: 200, Body: body}, nil
			default:
				return plugins.Response{Status: 404}, nil
			}
		},
	)
	if err := env.mgr.SetGrants(ctx, rec.ID, plugins.Grants{Folders: []string{mediaDir}}); err != nil {
		t.Fatal(err)
	}

	title := &core.Title{
		Key:     core.MovieKey(438631),
		Type:    core.TypeMovie,
		Title:   "Dune",
		Year:    2021,
		Runtime: 155 * time.Minute,
		IDs:     core.IDs{IMDB: "tt1160419", TMDB: 438631},
	}

	res, err := env.res.Resolve(ctx, core.ResolveRequest{
		Title: title,
		Client: core.ClientCaps{
			MaxHeight:   2160,
			VideoCodecs: []string{"h264", "hevc"},
		},
	})
	if err != nil {
		t.Fatalf("expected fallback candidate to succeed after lazy timeout, got: %v", err)
	}
	if !strings.Contains(res.Release.Name, "1080p") {
		t.Fatalf("expected 1080p fallback release to win after 4K lazy open timed out, got %q", res.Release.Name)
	}
}

func TestFoldersPluginLocalFilePlaysAsTopReleaseAndEnforcesContainment(t *testing.T) {
	env := newStreamTestEnv(t)
	ctx := context.Background()

	grantedDir := t.TempDir()
	outsideDir := t.TempDir()

	localMovie := filepath.Join(grantedDir, "Blade Runner 2049 (2017).mkv")
	if err := os.WriteFile(localMovie, bytesRepeat("LOCAL-FOLDER-MOVIE-", 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	outsideFile := filepath.Join(outsideDir, "secret.mkv")
	if err := os.WriteFile(outsideFile, bytesRepeat("OUTSIDE-", 512), 0o600); err != nil {
		t.Fatal(err)
	}
	escapeLink := filepath.Join(grantedDir, "escape.mkv")
	_ = os.Symlink(outsideFile, escapeLink)

	// Install a remote source plugin that offers a 1080p WEB-DL, and a local
	// folders plugin that offers the local file on disk.
	env.installSourcePlugin(
		"community.example.remote",
		"Remote Indexer",
		"indexer",
		"",
		nil,
		func(ctx context.Context, req plugins.Request) (plugins.Response, error) {
			body, _ := json.Marshal(map[string]any{
				"streams": []map[string]any{
					{
						"name": "Remote 1080p",
						"url":  "https://cdn.example.com/blade.mkv",
						"ether": map[string]any{
							"id":          "remote-1",
							"releaseName": "Blade.Runner.2049.2017.1080p.WEB-DL.DDP5.1.H.264-REMOTE",
							"size":        int64(8_000_000_000),
						},
					},
				},
			})
			return plugins.Response{Status: 200, Body: body}, nil
		},
	)

	foldersRec := env.installSourcePlugin(
		"community.example.folders",
		"Local Folders",
		"direct",
		"read",
		nil,
		func(ctx context.Context, req plugins.Request) (plugins.Response, error) {
			body, _ := json.Marshal(map[string]any{
				"streams": []map[string]any{
					{
						"name": "Outside traversal",
						"ether": map[string]any{
							"id":          "bad-dots",
							"releaseName": "Blade.Runner.2049.2017.2160p.REMUX.mkv",
							"file":        "../outside/secret.mkv",
						},
					},
					{
						"name": "Outside symlink",
						"ether": map[string]any{
							"id":          "bad-symlink",
							"releaseName": "Blade.Runner.2049.2017.2160p.REMUX.mkv",
							"file":        escapeLink,
						},
					},
					{
						"name": "Local file",
						"ether": map[string]any{
							"id":          "local-1",
							"releaseName": "Blade Runner 2049 (2017).mkv",
							"file":        localMovie,
						},
					},
				},
			})
			return plugins.Response{Status: 200, Body: body}, nil
		},
	)
	if err := env.mgr.SetGrants(ctx, foldersRec.ID, plugins.Grants{Folders: []string{grantedDir}}); err != nil {
		t.Fatal(err)
	}

	title := &core.Title{
		Key:     core.MovieKey(335984),
		Type:    core.TypeMovie,
		Title:   "Blade Runner 2049",
		Year:    2017,
		Runtime: 164 * time.Minute,
		IDs:     core.IDs{IMDB: "tt1856101", TMDB: 335984},
	}

	res, err := env.res.Resolve(ctx, core.ResolveRequest{
		Title: title,
		Client: core.ClientCaps{
			MaxHeight:   2160,
			VideoCodecs: []string{"h264", "hevc"},
		},
	})
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}
	if res.Release.Indexer != "Local Folders" {
		t.Fatalf("expected folders plugin local file to play as top release, got indexer=%q name=%q", res.Release.Indexer, res.Release.Name)
	}

	src, err := env.media.Open(ctx, res.Release.ID, res.NZB)
	if err != nil {
		t.Fatalf("Open local file failed: %v", err)
	}
	defer src.Close() //nolint:errcheck

	lfs, ok := src.(core.LocalFileSource)
	if !ok || lfs.LocalFile() == nil {
		t.Fatalf("expected MediaSource to implement core.LocalFileSource, got %T", src)
	}
	session := &core.PlaySession{Key: title.Key, Source: src}
	if rev := core.SourceRevision(session); !strings.HasPrefix(rev, "src-") {
		t.Fatalf("expected valid SourceRevision from LocalFileSource, got %q", rev)
	}

	// Revoking the folders grant must immediately block opening or health-checking the payload.
	if err := env.mgr.SetGrants(ctx, foldersRec.ID, plugins.Grants{
		Folders: []string{grantedDir},
		Revoked: []string{"folders"},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := env.media.Open(ctx, res.Release.ID, res.NZB); !errors.Is(err, plugins.ErrPermissionDenied) {
		t.Fatalf("expected ErrPermissionDenied after revoking folders grant, got %v", err)
	}
}

func TestDynamicEnableDisableAndBudgets(t *testing.T) {
	env := newStreamTestEnv(t)
	ctx := context.Background()

	rec := env.installSourcePlugin(
		"community.example.limited",
		"Limited Source",
		"indexer",
		"",
		map[string]any{"searchPerHour": 1},
		func(ctx context.Context, req plugins.Request) (plugins.Response, error) {
			body, _ := json.Marshal(map[string]any{
				"streams": []map[string]any{
					{
						"name":     "Limited 1080p",
						"infoHash": "0123456789abcdef0123456789abcdef01234567",
						"ether": map[string]any{
							"id":          "lim-1",
							"releaseName": "Heat.1995.1080p.BluRay.x264-GRP",
							"size":        int64(10_000_000_000),
						},
					},
				},
			})
			return plugins.Response{Status: 200, Body: body}, nil
		},
	)

	sreq := core.SearchRequest{
		Type:  core.TypeMovie,
		Title: "Heat",
		Year:  1995,
		IDs:   core.IDs{IMDB: "tt0113277", TMDB: 949},
	}

	rels, err := env.sources.Search(ctx, sreq)
	if err != nil || len(rels) != 1 {
		t.Fatalf("first search failed: rels=%d err=%v", len(rels), err)
	}
	if rels[0].Protocol != core.ProtocolTorrent || rels[0].InfoHash != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("expected torrent release, got protocol=%q hash=%q", rels[0].Protocol, rels[0].InfoHash)
	}

	// The same search again is answered from the search cache; another
	// title within the hour hits the plugin's searchPerHour=1 limit.
	if again, err := env.sources.Search(ctx, sreq); err != nil || len(again) != 1 {
		t.Fatalf("cached search: rels=%d err=%v", len(again), err)
	}
	other := sreq
	other.Title, other.IDs = "Ronin", core.IDs{IMDB: "tt0122690", TMDB: 8195}
	if _, err := env.sources.Search(ctx, other); !errors.Is(err, core.ErrBudgetExhausted) {
		t.Fatalf("expected ErrBudgetExhausted on second search, got %v", err)
	}

	// Disabling the plugin removes it from active stream sources immediately.
	if err := env.mgr.SetEnabled(ctx, rec.ID, false); err != nil {
		t.Fatal(err)
	}
	if env.sources.HasEnabledPlugins(ctx) {
		t.Fatal("expected no enabled stream_source plugins after SetEnabled(false)")
	}
}

func bytesRepeat(s string, count int) []byte {
	return []byte(strings.Repeat(s, count))
}

func TestRemoteFileRangePlaybackAndGrantRevocation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env := newStreamTestEnv(t)
	dir := t.TempDir()
	data := bytes.Repeat([]byte("0123456789abcdef"), 16384)
	if err := os.WriteFile(filepath.Join(dir, "movie.mkv"), data, 0600); err != nil {
		t.Fatal(err)
	}
	tun := remote.NewTunnelServer(remote.TunnelOptions{Auth: remotetest.Credentials{"credential": "example.remote"}})
	defer tun.Close()
	server := httptest.NewServer(tun.Handler())
	defer server.Close()
	sidecar := sdk.New(sdk.Definition{Manifest: sdk.Manifest{ID: "example.remote"}, Routes: sdk.Routes{
		Stream: func(_ *sdk.Context, _ sdk.StreamRequest) (sdk.StreamResponse, error) {
			body, _ := json.Marshal(map[string]any{"streams": []any{map[string]any{"ether": map[string]any{"id": "file1", "file": filepath.Join(dir, "movie.mkv"), "releaseName": "Arrival.2016.2160p.REMUX.mkv", "size": 1}}}})
			var response sdk.StreamResponse
			err := json.Unmarshal(body, &response)
			return response, err
		},
		File: func(_ *sdk.Context, req sdk.FileRequest) (sdk.FileRangeResponse, error) {
			method := "GET"
			if req.HeadOnly {
				method = "HEAD"
			}
			request := httptest.NewRequest(method, "/movie.mkv", nil)
			request.Header.Set("Range", req.Range)
			response := httptest.NewRecorder()
			http.ServeContent(response, request, "movie.mkv", time.Time{}, bytes.NewReader(data))
			return sdk.FileRangeResponse{Status: response.Code, ContentType: response.Header().Get("Content-Type"), ContentRange: response.Header().Get("Content-Range"), Body: response.Body.Bytes()}, nil
		},
	}})
	handle, err := sidecar.Start(ctx, sdk.ServeOptions{Mode: "tunnel", TunnelURL: server.URL, Credential: "credential"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = handle.Close(); <-handle.Done }()
	env.mgr.RegisterRuntime("remote", (&remote.Factory{Tunnel: tun}).RuntimeFactory())
	manifest := []byte(`{"id":"example.remote","version":"1.0.0","name":"Sidecar","ether":{"protocol":1,"kind":"source","summary":"Provides streams for titles","capabilities":["stream_source"],"runtime":{"kind":"remote"},"permissions":{"folders":"read"},"contributes":{"source":{"class":"direct"}}}}`)
	preview, err := env.mgr.PreviewInstall(ctx, plugins.Source{Kind: "studio", Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = env.mgr.SetGrants(ctx, rec.ID, plugins.Grants{Folders: []string{dir}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for !tun.IsConnected(rec.ID) {
		select {
		case <-deadline.C:
			t.Fatal("sidecar did not connect")
		case <-ticker.C:
		}
	}
	releases, err := env.sources.Search(ctx, core.SearchRequest{Title: "Arrival", Year: 2016, Type: core.TypeMovie, IDs: core.IDs{IMDB: "tt2543164"}})
	if err != nil || len(releases) != 1 {
		t.Fatalf("search %v %v", releases, err)
	}
	payload, err := env.sources.FetchNZB(ctx, releases[0])
	if err != nil {
		t.Fatal(err)
	}
	source, err := env.engine.Open(ctx, releases[0].ID, payload)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if source.Size() != int64(len(data)) {
		t.Fatalf("size %d", source.Size())
	}
	for _, bad := range []string{dir + "/../secret", dir + "/%2e%2e/secret", "/outside/secret"} {
		forged := adapters.LocalPayload{PluginID: rec.ID, FilePath: bad, Remote: true}.Encode()
		if _, err := env.engine.Open(ctx, "", forged); !errors.Is(err, plugins.ErrPermissionDenied) {
			t.Fatalf("ungranted path %q: %v", bad, err)
		}
	}
	for _, off := range []int64{30000, 0, int64(len(data) - 17)} {
		buf := make([]byte, 50)
		n, err := source.ReadAt(buf, off)
		if err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		if !bytes.Equal(buf[:n], data[int(off):int(off)+n]) {
			t.Fatalf("bytes at %d", off)
		}
	}
	// Read across multiple SDK chunks as well as the short probes/seeks above.
	// No reconnect wait or retry may hide a tunnel lost to probe cancellation.
	full := make([]byte, len(data))
	if n, err := source.ReadAt(full, 0); n != len(data) || err != nil || !bytes.Equal(full, data) {
		t.Fatalf("full remote range: n=%d err=%v", n, err)
	}
	if err = env.mgr.SetGrants(ctx, rec.ID, plugins.Grants{Folders: []string{dir}, Revoked: []string{"folders"}}); err != nil {
		t.Fatal(err)
	}
	if _, err = source.ReadAt(make([]byte, 10), 0); !errors.Is(err, plugins.ErrPermissionDenied) {
		t.Fatalf("revoked read %v", err)
	}
	forged := adapters.LocalPayload{PluginID: rec.ID, FilePath: dir + "/../secret", Remote: true}.Encode()
	if _, err = env.engine.Open(ctx, "", forged); !errors.Is(err, plugins.ErrPermissionDenied) {
		t.Fatalf("traversal open %v", err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := source.ReadAt(make([]byte, 1), 0); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("closed read %v", err)
	}
}

func TestManagerRejectsStreamingOnNonStreamingRuntime(t *testing.T) {
	env := newStreamTestEnv(t)
	rec := env.installSourcePlugin("example.hosted", "Hosted", "direct", "read", nil, nil)
	_, body, err := env.mgr.CallStream(context.Background(), rec.ID, plugins.Request{Path: "/ether/file/movie.mkv"})
	if body != nil {
		body.Close()
	}
	if !errors.Is(err, plugins.ErrStreamUnsupported) {
		t.Fatalf("streaming error = %v", err)
	}
}

// An ordinary manifest uses the same source registry and release contract as a
// native plugin; these are offline adapter checks, not upstream/playback proof.
func TestOrdinaryStremioStreamsUseSharedSources(t *testing.T) {
	env := newStreamTestEnv(t)
	ctx := context.Background()
	id := "community.ordinary.streams"
	calls := 0
	env.rts[id] = &fakePluginRuntime{handler: func(_ context.Context, req plugins.Request) (plugins.Response, error) {
		calls++
		if req.Path != "/stream/movie/tt0017136.json" || len(req.Query) != 0 {
			t.Errorf("ordinary request contains non-protocol hints: %+v", req)
		}
		return plugins.Response{Status: 200, Body: json.RawMessage(`{"streams":[{"name":"Metropolis 1080p","url":"https://example.test/metropolis.mp4"},{"name":"Metropolis 720p","infoHash":"0123456789abcdef0123456789abcdef01234567","fileIdx":2}]}`)}, nil
	}}
	raw := json.RawMessage(`{"id":"community.ordinary.streams","version":"1.0.0","name":"Public film sources","description":"A foreign description!","types":["movie"],"resources":[{"name":"stream","types":["movie"],"idPrefixes":["tt"]}]}`)
	preview, err := env.mgr.PreviewInstall(ctx, plugins.Source{Kind: "studio", Manifest: raw})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.mgr.Install(ctx, preview, plugins.InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	descriptor := extend.PluginID(id, "stream")
	if _, ok := env.reg.Get(extend.KindSourceKind, descriptor); !ok {
		t.Fatal("missing shared source descriptor")
	}
	req := core.SearchRequest{Type: core.TypeMovie, Title: "Metropolis", Year: 1927, IDs: core.IDs{IMDB: "tt0017136", TMDB: 19}}
	releases, err := env.sources.Search(ctx, req)
	if err != nil || len(releases) != 2 {
		t.Fatalf("mapped releases %d: %v", len(releases), err)
	}
	ids := map[string]bool{}
	protocols := map[string]bool{}
	for _, r := range releases {
		if r.ID == "" || r.Indexer != rec.Name || r.TitleIDs.IMDB != req.IDs.IMDB {
			t.Fatalf("invalid shared release: %+v", r)
		}
		if ids[r.ID] {
			t.Fatal("release IDs collide")
		}
		ids[r.ID] = true
		protocols[r.Protocol] = true
		switch r.Protocol {
		case core.ProtocolHTTP:
			if r.StreamURL != "https://example.test/metropolis.mp4" {
				t.Fatalf("direct URL not retained: %+v", r)
			}
		case core.ProtocolTorrent:
			if r.InfoHash != "0123456789abcdef0123456789abcdef01234567" || r.GUID != "btih:0123456789abcdef0123456789abcdef01234567/2" || !strings.Contains(r.MagnetURL, "0123456789abcdef0123456789abcdef01234567") {
				t.Fatalf("torrent identity/file selection lost: %+v", r)
			}
		default:
			t.Fatalf("unexpected protocol %q", r.Protocol)
		}
	}
	if !protocols[core.ProtocolTorrent] || !protocols[core.ProtocolHTTP] {
		t.Fatal("direct or torrent form missing")
	}
	if err := env.mgr.SetEnabled(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.reg.Get(extend.KindSourceKind, descriptor); ok {
		t.Fatal("disabled contribution remains")
	}
	got, err := env.sources.Search(ctx, req)
	if err != nil || len(got) != 0 || calls != 1 {
		t.Fatalf("disabled source called: %d calls=%d err=%v", len(got), calls, err)
	}
	if err := env.mgr.SetEnabled(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	got, err = env.sources.Search(ctx, req)
	if err != nil || len(got) != 2 {
		t.Fatalf("re-enabled source: %v", err)
	}
	for _, r := range got {
		if !ids[r.ID] {
			t.Fatal("release identity changed after re-enable")
		}
	}
	// Re-enabled, the same search is answered from the search cache.
	unsupported := req
	unsupported.IDs = core.IDs{TMDB: 19}
	if got, err = env.sources.Search(ctx, unsupported); err != nil || len(got) != 0 || calls != 1 {
		t.Fatalf("undeclared identity was requested: calls=%d err=%v", calls, err)
	}
	if err := env.mgr.Remove(ctx, id, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := env.reg.Get(extend.KindSourceKind, descriptor); ok {
		t.Fatal("uninstalled contribution remains")
	}
	if got, err = env.sources.Search(ctx, req); err != nil || len(got) != 0 || calls != 1 {
		t.Fatalf("uninstalled source called: got=%d calls=%d err=%v", len(got), calls, err)
	}
}
