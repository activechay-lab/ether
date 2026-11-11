package examples_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/extend"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/libraries"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/adapters"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

// The kind-library proof (spec §13): TestKindLibraryExample exercises
// pack + conformance, hosted execution, pull sync, store browse/search,
// container range reads, manual match pinning, and store purge directly;
// TestKindLibraryEndToEndSliceD exercises the full Manager → KindCatalog
// → KindResolver → Experience client-surface flow through install, setup,
// offline browse, container playback, and Manager.Remove purge; and
// TestNonKindPluginCreatesNoLibrary proves non-kind plugins (hello) never
// create a library or touch the libraries store.

type runtimeCaller struct{ rt plugins.Runtime }

func (c runtimeCaller) Call(ctx context.Context, _ string, req plugins.Request) (plugins.Response, error) {
	return c.rt.Call(ctx, req)
}

func TestKindLibraryExample(t *testing.T) {
	ctx := context.Background()

	// The album zip the resolver points at, served with Range support.
	track2 := []byte("fog horn fixture bytes\n")
	var zipBuf bytes.Buffer
	zw := zip.NewWriter(&zipBuf)
	for name, data := range map[string][]byte{"01-gulls.flac": []byte("gulls fixture bytes\n"), "02-fog-horn.flac": track2} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zipBytes := zipBuf.Bytes()
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/harbour.zip" {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "harbour.zip", time.Time{}, bytes.NewReader(zipBytes))
	}))
	defer media.Close()

	// Pack and conformance-check the example like any catalogue plugin.
	pkg, err := kit.LoadDir("kind-library")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := kit.Pack(pkg)
	if err != nil {
		t.Fatal(err)
	}
	packed, err := kit.LoadPkgBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	if report := kit.Check(ctx, packed); !report.OK {
		t.Fatalf("kit check: %+v", report.Errors)
	}
	decls := packed.Manifest.EffectiveEther().Contributes.MediaKinds
	if len(decls) != 1 {
		t.Fatalf("expected one kind, got %+v", decls)
	}
	decl := decls[0]
	if !decl.Browse.LiveSearch {
		t.Fatal("example must declare live search")
	}

	// Run it hosted and point it at the fixture media server.
	engine := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer engine.Close()
	host := &fixtureHost{kv: map[string][]byte{}, settings: map[string]any{"sourceUrl": media.URL}}
	rt, err := plugins.HostedRuntimeFactory(engine)(plugins.Installed{
		ID: packed.Manifest.ID, Manifest: packed.Manifest, Host: host,
		Files: func(p string) ([]byte, bool) { v, ok := packed.Files[p]; return v, ok },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	call := func(method, path, body string) plugins.Response {
		t.Helper()
		r, e := rt.Call(ctx, plugins.Request{Method: method, Path: path, Body: json.RawMessage(body)})
		if e != nil || r.Status != 200 {
			t.Fatalf("%s %s: %d %s %v", method, path, r.Status, r.Body, e)
		}
		return r
	}
	// The example's zip hints carry the fixture's real size.
	call("POST", "/ether/configure", `{"sourceUrl":"`+media.URL+`"}`)

	// Setup step accepts the saved values.
	setup := call("POST", "/ether/v1/setup/account/test", `{"step":"account","fields":{"sourceUrl":"`+media.URL+`","apiKey":"fixture-key"}}`)
	if !strings.Contains(string(setup.Body), `"ok":true`) {
		t.Fatalf("setup: %s", setup.Body)
	}

	// Kit provider conformance over the live hosted routes.
	caller := runtimeCaller{rt: rt}
	if issues := kit.CheckKindProvider(ctx, decl, true, func(c context.Context, req plugins.Request) (plugins.Response, error) {
		return caller.Call(c, packed.Manifest.ID, req)
	}); len(issues) > 0 {
		t.Fatalf("provider conformance: %+v", issues)
	}

	// Sync into the plugin-item store and browse from the store.
	db := store.OpenTest(t)
	svc, err := pluginitems.New(ctx, db, pluginitems.Options{Caller: caller})
	if err != nil {
		t.Fatal(err)
	}
	defer svc.Close()
	const libraryID = core.LibraryID(42)
	if _, err := svc.UpsertKind(ctx, packed.Manifest.ID, libraryID, decl, false); err != nil {
		t.Fatal(err)
	}
	// Hints must carry the configured source; patch the stored decl's
	// sample size by re-syncing after configure (already configured).
	if _, err := svc.SyncKind(ctx, packed.Manifest.ID, decl.Key, pluginitems.SyncOptions{}); err != nil {
		t.Fatal(err)
	}
	roots, err := svc.Browse(ctx, pluginitems.BrowseQuery{LibraryID: libraryID, Level: "collection", Limit: 10})
	if err != nil || len(roots.Items) != 1 || roots.Items[0].Title != "Coastal" {
		t.Fatalf("roots: %+v %v", roots, err)
	}
	tracks, err := svc.Browse(ctx, pluginitems.BrowseQuery{
		LibraryID: libraryID, Level: "track",
		ParentRef: core.NewItemRef(packed.Manifest.ID, decl.Key, "harbour"),
		Sort:      "index", Limit: 10,
	})
	if err != nil || len(tracks.Items) != 2 {
		t.Fatalf("tracks: %+v %v", tracks, err)
	}
	fog := tracks.Items[1]
	if fog.Title != "Fog Horn" || !fog.Playable {
		t.Fatalf("track 2: %+v", fog)
	}
	// Local (synced) search finds it; live search route answers too.
	found, err := svc.Search(ctx, pluginitems.SearchQuery{LibraryID: libraryID, Query: "fog", Limit: 10})
	if err != nil || len(found.Items) != 1 || found.Items[0].ID != "harbour-2" {
		t.Fatalf("store search: %+v %v", found, err)
	}
	live := call("POST", "/ether/v1/kinds/recordings/search", `{"query":"fog","limit":10}`)
	if !strings.Contains(string(live.Body), "harbour-2") {
		t.Fatalf("live search: %s", live.Body)
	}

	// Resolve track 2 and open its bytes through the container pattern.
	itemJSON, _ := json.Marshal(fog)
	resolved := call("POST", "/ether/v1/kinds/recordings/resolve", `{"libraryKey":"pk-example-recordings","item":`+string(itemJSON)+`}`)
	var res core.KindResolveResponse
	if err := json.Unmarshal(resolved.Body, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Sources) == 0 || res.Sources[0].Pattern != "container" {
		t.Fatalf("resolve: %+v", res.Sources)
	}
	// The example declares a nominal zip size; the fixture's is real.
	res.Sources[0].Container.Volumes[0].Size = int64(len(zipBytes))
	cache, err := blobcache.Open(t.TempDir(), blobcache.Options{SweepPeriod: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer cache.Close()
	src, _, err := kindstream.OpenSources(ctx, res.Sources[:1], kindstream.Deps{
		HTTP: media.Client(), ItemIndex: fog.Index,
		Cache: cache.Namespace("pk-example-recordings", blobcache.Quota{MaxBytes: 32 << 20}),
	})
	if err != nil {
		t.Fatalf("open container: %v", err)
	}
	got := make([]byte, src.Size())
	if _, err := src.ReadAt(got, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	_ = src.Close()
	if !bytes.Equal(got, track2) {
		t.Fatalf("track bytes %q, want %q", got, track2)
	}

	// Matcher scores persist; a manual pin wins over the auto match.
	candidates := []core.SourceCandidate{
		{InfoHash: "ABC123", Title: "Fog Horn (field recording)", Size: 1000, Seeders: 4},
		{InfoHash: "DEF456", Title: "City Traffic", Size: 900, Seeders: 9},
	}
	if _, err := svc.MatchItem(ctx, caller, packed.Manifest.ID, decl.Key, fog, candidates, nil, 0.8); err != nil {
		t.Fatal(err)
	}
	if err := svc.PinMatch(ctx, fog.Ref, "infohash:def456", []string{"Owner picked this one"}); err != nil {
		t.Fatal(err)
	}
	matches, err := svc.ListMatches(ctx, fog.Ref)
	if err != nil {
		t.Fatal(err)
	}
	pinned := false
	for _, m := range matches {
		if m.Chosen && m.Source == "manual" && m.CandidateRef == "infohash:def456" {
			pinned = true
		}
	}
	if !pinned {
		t.Fatalf("manual pin not chosen: %+v", matches)
	}

	// The OPDS-ish frontend adapter lists playable tracks.
	feed := call("GET", "/ether/v1/frontend/feed/", "")
	if !strings.Contains(string(feed.Body), "Field Recordings") || !strings.Contains(string(feed.Body), "kind-media/harbour-2") {
		t.Fatalf("feed: %s", feed.Body)
	}

	// Purge removes the kind's items from the store.
	if err := svc.PurgePlugin(ctx, packed.Manifest.ID); err != nil {
		t.Fatal(err)
	}
	after, err := svc.Browse(ctx, pluginitems.BrowseQuery{LibraryID: libraryID, Level: "collection", Limit: 10})
	if err != nil || len(after.Items) != 0 {
		t.Fatalf("after purge: %+v %v", after, err)
	}
}

type mediaExampleTransport struct {
	target string
	base   http.RoundTripper
}

func (t mediaExampleTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Hostname() == "media.example" {
		u, err := url.Parse(t.target)
		if err != nil {
			return nil, err
		}
		clone := req.Clone(req.Context())
		clone.URL.Scheme = u.Scheme
		clone.URL.Host = u.Host
		rt := t.base
		if rt == nil {
			rt = http.DefaultTransport
		}
		return rt.RoundTrip(clone)
	}
	return nil, errors.New("unexpected network host: " + req.URL.Hostname())
}

type faultableRuntime struct {
	inner plugins.Runtime
	fail  *atomic.Bool
}

func (r faultableRuntime) Call(ctx context.Context, req plugins.Request) (plugins.Response, error) {
	if r.fail.Load() {
		return plugins.Response{}, errors.New("runtime offline")
	}
	return r.inner.Call(ctx, req)
}

func (r faultableRuntime) Close() error { return r.inner.Close() }

func buildFixedSizeHarbourZip(t *testing.T, totalSize int, track1, track2 []byte) []byte {
	t.Helper()
	write := func(comment string) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for _, entry := range []struct {
			name string
			data []byte
		}{
			{"01-gulls.flac", track1},
			{"02-fog-horn.flac", track2},
		} {
			hdr := &zip.FileHeader{Name: entry.name, Method: zip.Store}
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write(entry.data); err != nil {
				t.Fatal(err)
			}
		}
		if comment != "" {
			if err := zw.SetComment(comment); err != nil {
				t.Fatal(err)
			}
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	base := write("")
	if len(base) > totalSize {
		t.Fatalf("base zip %d exceeds target %d", len(base), totalSize)
	}
	padded := write(strings.Repeat("p", totalSize-len(base)))
	if len(padded) != totalSize {
		t.Fatalf("padded zip length = %d, want %d", len(padded), totalSize)
	}
	return padded
}

func TestKindLibraryEndToEndSliceD(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	track1 := []byte("gulls fixture bytes\n")
	track2 := []byte("fog horn fixture bytes\n")
	// kind-library's plugin.ts declares zipSize: 4096 on /harbour.zip.
	zipBytes := buildFixedSizeHarbourZip(t, 4096, track1, track2)
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/harbour.zip" {
			http.NotFound(w, r)
			return
		}
		http.ServeContent(w, r, "harbour.zip", time.Time{}, bytes.NewReader(zipBytes))
	}))
	defer media.Close()

	dataDir := t.TempDir()
	cfg, err := config.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	libs, err := libraries.New(ctx, db, libraries.Options{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	st := state.New(db, state.Options{Logger: log})

	engine := wasm.NewEngine(log)
	defer engine.Close()

	var offline atomic.Bool
	baseFactory := plugins.HostedRuntimeFactory(engine)

	mgr, err := plugins.New(plugins.Deps{
		DB:           db,
		Sealer:       cfg,
		CheckPackage: kit.CheckInstall,
		Registry:     extend.New(),
		Tools:        tools.NewCatalogue(),
		Logger:       log,
		IsLeader:     func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", func(inst plugins.Installed) (plugins.Runtime, error) {
		rt, err := baseFactory(inst)
		if err != nil {
			return nil, err
		}
		return faultableRuntime{inner: rt, fail: &offline}, nil
	})

	itemsSvc, err := pluginitems.New(ctx, db, pluginitems.Options{
		Caller:   mgr,
		Health:   mgr,
		Logger:   log,
		IsLeader: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer itemsSvc.Close()

	blobDir := filepath.Join(dataDir, "blobcache")
	blobs, err := blobcache.Open(blobDir, blobcache.Options{Logger: log, SweepPeriod: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()

	kc := adapters.AttachKindCatalog(ctx, adapters.KindCatalogDeps{
		Manager:   mgr,
		Libraries: libs,
		Items:     itemsSvc,
		Blobs:     blobs,
		BlobDir:   blobDir,
		Logger:    log,
	})
	kr := adapters.NewKindResolver(adapters.KindResolverDeps{
		Manager:   mgr,
		Libraries: libs,
		Items:     itemsSvc,
		Blobs:     blobs,
		Catalog:   kc,
		HTTP:      &http.Client{Transport: mediaExampleTransport{target: media.URL}},
		Logger:    log,
	})
	cachePut, cacheOpen := blobs.HostBackends(kc.QuotaFor)
	mgr.SetKindBackends(plugins.KindBackends{
		KindUpsert:     itemsSvc.KindUpsert,
		KindDelete:     itemsSvc.KindDelete,
		KindItems:      itemsSvc.KindItems,
		ResolveSources: kr.ResolveSources,
		CachePut:       cachePut,
		CacheOpen:      cacheOpen,
	})
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	owner := &core.User{ID: 1, Username: "owner", IsOwner: true}
	builtins := []experience.Library{
		{ID: int(core.LibraryMovies), Name: "Movies", Type: core.TypeMovie, Virtual: true},
		{ID: int(core.LibraryShows), Name: "TV Shows", Type: core.TypeShow, Virtual: true},
	}
	vis := experience.BindVisibility(nil, func(context.Context, core.RatingKey) (*core.Title, error) {
		return nil, core.ErrNotFound
	}, nil, nil, builtins)
	exp, err := experience.NewService(experience.ServiceDeps{
		Active: func(_ context.Context, id int64) (*core.User, error) {
			if id == 1 {
				return owner, nil
			}
			return nil, core.ErrNotFound
		},
		Libraries:       builtins,
		Visibility:      vis,
		State:           st,
		Rows:            &experience.RowsService{},
		Protocol:        "jellyfin",
		PluginLibraries: kc,
		Get:             func(context.Context, *core.User, core.RatingKey) (*core.Title, error) { return nil, core.ErrNotFound },
		Children:        func(context.Context, *core.User, core.RatingKey) ([]core.Title, error) { return nil, nil },
		Browse:          func(context.Context, core.BrowseQuery) (core.Page, error) { return core.Page{}, nil },
		Search: experience.Search{
			Catalog: func(context.Context, string, []core.MediaType, int) ([]core.Title, error) { return nil, nil },
		},
		IsFavorite:          func(context.Context, *core.User, core.RatingKey) (bool, error) { return false, nil },
		IsFavoriteMany:      func(context.Context, *core.User, []core.RatingKey) (map[core.RatingKey]bool, error) { return nil, nil },
		IsNotInterested:     func(context.Context, *core.User, core.RatingKey) (bool, error) { return false, nil },
		IsNotInterestedMany: func(context.Context, *core.User, []core.RatingKey) (map[core.RatingKey]bool, error) { return nil, nil },
		IsWatchlisted:       func(context.Context, *core.User, core.RatingKey) (bool, error) { return false, nil },
		IsWatchlistedMany:   func(context.Context, int64, []core.RatingKey) (map[core.RatingKey]bool, error) { return nil, nil },
		Enrich: func(_ context.Context, _ *core.User, _ core.Device, title *core.Title) (frontend.ItemDetail, error) {
			return frontend.ItemDetail{Title: *title}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Pack and install kind-library through Manager.
	pkg, err := kit.LoadDir("kind-library")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := kit.Pack(pkg)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "pkg", Pkg: raw})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	// Required setup step "account" gates client surface visibility until completed.
	viewsBefore, err := exp.Views(ctx, owner)
	if err != nil || len(viewsBefore) != 2 {
		t.Fatalf("expected 2 builtin views before setup completed, got %+v %v", viewsBefore, err)
	}

	if _, err := mgr.Configure(ctx, rec.ID, map[string]any{
		"sourceUrl": "https://media.example",
		"apiKey":    "fixture-key",
	}); err != nil {
		t.Fatal(err)
	}
	stepRes, err := mgr.TestSetupStep(ctx, rec.ID, "account")
	if err != nil || !stepRes.OK {
		t.Fatalf("TestSetupStep(account): %+v %v", stepRes, err)
	}

	// Wait for initial pull sync to populate the Field Recordings library.
	var recView frontend.View
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		views, _ := exp.Views(ctx, owner)
		for _, v := range views {
			if v.PluginID == rec.ID {
				recView = v
			}
		}
		if recView.ID != "" {
			if cnt, err := exp.LibraryCount(ctx, owner, recView.ID); err == nil && cnt == 1 {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if recView.ID == "" || recView.Name != "Field Recordings" || recView.Native != "music" {
		t.Fatalf("expected MediaBrowser music view for Field Recordings, got %+v", recView)
	}

	// Resolve and stream track 2 byte-for-byte through KindResolver + container zip + blobcache.
	roots, err := exp.LibraryItems(ctx, owner, core.Device{}, recView.ID, frontend.ItemQuery{Limit: 10})
	if err != nil || len(roots.Items) != 1 || roots.Items[0].Title != "Coastal" {
		t.Fatalf("LibraryItems roots: %+v %v", roots, err)
	}
	albums, err := exp.Children(ctx, owner, core.Device{}, roots.Items[0].Key, frontend.ItemQuery{Limit: 10})
	if err != nil || len(albums.Items) != 1 || albums.Items[0].Title != "Harbour at Dawn" {
		t.Fatalf("Children albums: %+v %v", albums, err)
	}
	tracks, err := exp.Children(ctx, owner, core.Device{}, albums.Items[0].Key, frontend.ItemQuery{Limit: 10})
	if err != nil || len(tracks.Items) != 2 || tracks.Items[1].Title != "Fog Horn" {
		t.Fatalf("Children tracks: %+v %v", tracks, err)
	}
	fogKey := tracks.Items[1].Key

	km, err := kr.OpenKind(ctx, owner, fogKey, nil)
	if err != nil {
		t.Fatalf("OpenKind(Fog Horn): %v", err)
	}
	gotBytes := make([]byte, km.Size)
	if _, err := km.ReadAt(gotBytes, 0); err != nil && err != io.EOF {
		t.Fatal(err)
	}
	_ = km.Close()
	if !bytes.Equal(gotBytes, track2) {
		t.Fatalf("OpenKind track2 bytes = %q, want %q", gotBytes, track2)
	}

	libRow, err := libs.Get(ctx, recView.LibraryID)
	if err != nil {
		t.Fatal(err)
	}
	nsDir := filepath.Join(blobDir, libRow.Key)
	if _, err := os.Stat(nsDir); err != nil {
		t.Fatalf("expected zip directory cached in blobcache namespace %s: %v", nsDir, err)
	}

	// Stop/fault the plugin runtime: client surfaces continue browsing from the local store.
	offline.Store(true)
	offlineRoots, err := exp.LibraryItems(ctx, owner, core.Device{}, recView.ID, frontend.ItemQuery{Limit: 10})
	if err != nil || len(offlineRoots.Items) != 1 || offlineRoots.Items[0].Title != "Coastal" {
		t.Fatalf("offline LibraryItems failed when plugin stopped: %+v %v", offlineRoots, err)
	}
	offlineDetail, err := exp.Item(ctx, frontend.ItemRequest{Viewer: owner, Key: fogKey})
	if err != nil || offlineDetail.Title.Title != "Fog Horn" {
		t.Fatalf("offline Item failed when plugin stopped: %+v %v", offlineDetail, err)
	}

	// Removing the plugin purges pluginitems, libraries, and blobcache.
	if err := mgr.Remove(ctx, rec.ID, false); err != nil {
		t.Fatalf("mgr.Remove: %v", err)
	}
	remainingLibs, err := libs.List(ctx)
	if err != nil || len(remainingLibs) != 2 {
		t.Fatalf("expected only Movies and TV Shows after Remove, got %+v %v", remainingLibs, err)
	}
	if kinds, err := itemsSvc.ListKinds(ctx, rec.ID); err != nil || len(kinds) != 0 {
		t.Fatalf("expected 0 plugin kinds after Remove, got %+v %v", kinds, err)
	}
	if _, err := os.Stat(nsDir); !os.IsNotExist(err) {
		t.Fatalf("expected blobcache namespace %s removed on Remove, stat err = %v", nsDir, err)
	}
}

func TestNonKindPluginCreatesNoLibrary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dataDir := t.TempDir()
	cfg, err := config.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	db := store.OpenTest(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	libs, err := libraries.New(ctx, db, libraries.Options{Logger: log})
	if err != nil {
		t.Fatal(err)
	}
	genBefore := libs.Generation()

	engine := wasm.NewEngine(log)
	defer engine.Close()

	mgr, err := plugins.New(plugins.Deps{
		DB:           db,
		Sealer:       cfg,
		CheckPackage: kit.CheckInstall,
		Registry:     extend.New(),
		Tools:        tools.NewCatalogue(),
		Logger:       log,
		IsLeader:     func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	mgr.RegisterRuntime("hosted", plugins.HostedRuntimeFactory(engine))

	itemsSvc, err := pluginitems.New(ctx, db, pluginitems.Options{
		Caller:   mgr,
		Health:   mgr,
		Logger:   log,
		IsLeader: func() bool { return true },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer itemsSvc.Close()

	blobDir := filepath.Join(dataDir, "blobcache")
	blobs, err := blobcache.Open(blobDir, blobcache.Options{Logger: log, SweepPeriod: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer blobs.Close()

	adapters.AttachKindCatalog(ctx, adapters.KindCatalogDeps{
		Manager:   mgr,
		Libraries: libs,
		Items:     itemsSvc,
		Blobs:     blobs,
		BlobDir:   blobDir,
		Logger:    log,
	})
	if err := mgr.Start(ctx); err != nil {
		t.Fatal(err)
	}

	pkg, err := kit.LoadDir(filepath.Join("hello", "typescript"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := kit.Pack(pkg)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := mgr.PreviewInstall(ctx, plugins.Source{Kind: "pkg", Pkg: raw})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := mgr.Install(ctx, preview, plugins.InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}

	if libs.Generation() != genBefore {
		t.Fatalf("installing non-kind hello plugin touched libraries store: gen %d != %d", libs.Generation(), genBefore)
	}
	allLibs, err := libs.List(ctx)
	if err != nil || len(allLibs) != 2 {
		t.Fatalf("expected only 2 builtin libraries after installing hello, got %+v %v", allLibs, err)
	}
	if kinds, err := itemsSvc.ListKinds(ctx, rec.ID); err != nil || len(kinds) != 0 {
		t.Fatalf("expected 0 plugin kinds for hello plugin, got %+v %v", kinds, err)
	}
	if entries, err := os.ReadDir(blobDir); err != nil || len(entries) != 0 {
		t.Fatalf("expected 0 blobcache namespace dirs for hello plugin, got %v %v", entries, err)
	}
}
