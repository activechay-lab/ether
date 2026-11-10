package examples_test

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/blobcache"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/kindstream"
	"github.com/braedonsaunders/ether/internal/pluginitems"
	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/kit"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/store"
)

// The kind-library proof (spec §13): everything that works on slices
// A/B/C/E runs for real here — pack + conformance, hosted execution,
// pull sync into the plugin-item store, store browse/search, track 2
// opened through the container pattern byte-for-byte, a pinned manual
// match, and the store purge. The parts that need slice D (Manager
// install creating the library, Experience/client-surface browse,
// removal through the Manager) are in TestKindLibraryEndToEndSliceD,
// skipped and labelled until D lands.

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
	// Fix the declared zip size to the fixture's real size via hints.
	var hints map[string]any
	if err := json.Unmarshal(fog.Hints, &hints); err != nil {
		t.Fatal(err)
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

// TestKindLibraryEndToEndSliceD holds the assertions that need slice D
// (kind adapters + Experience dispatch): installing the example into a
// test Ether creates its library, client surfaces browse it from the
// store with the plugin stopped, and removing the plugin purges the
// library, its items and its blob cache. Un-skip when D lands.
func TestKindLibraryEndToEndSliceD(t *testing.T) {
	t.Skip("slice D (adapters + Experience dispatch) has not landed: Manager install → library, MediaBrowser browse, removal purge")
}
