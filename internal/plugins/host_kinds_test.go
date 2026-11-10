package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func installContent(t *testing.T, env *testEnv) Record {
	t.Helper()
	man := contentManifest()
	man.Ether.Contributes.MediaKinds[0].Sync = KindSyncDecl{Mode: KindSyncPush}
	raw, err := json.Marshal(man)
	if err != nil {
		t.Fatal(err)
	}
	p, err := env.m.PreviewInstall(context.Background(), Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"dist/plugin.js": []byte("// library")}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(context.Background(), p, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestHostKindServices(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := installContent(t, env)
	h := env.m.HostFor(rec.ID)

	if got, want := core.PluginLibraryKey(rec.ID, "recording"), "pk-community-example-library-recording"; got != want {
		t.Fatalf("PluginLibraryKey(%q, %q) = %q, want %q", rec.ID, "recording", got, want)
	}

	// No backend wired yet: granted calls surface ErrUnavailable, not a panic.
	item := core.KindItem{ID: "trk-1", Level: "track", ParentID: "alb-1", Title: "Dawn chorus", Playable: true}
	if _, err := h.KindUpsert(ctx, "recording", []core.KindItem{item}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("upsert without backend: %v", err)
	}
	// An undeclared kind and a bad envelope are refused before any backend.
	if _, err := h.KindUpsert(ctx, "book", []core.KindItem{item}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("undeclared kind: %v", err)
	}
	bad := item
	bad.ID = "has:colon"
	if _, err := h.KindUpsert(ctx, "recording", []core.KindItem{bad}); err == nil {
		t.Fatal("bad external id accepted")
	}
	bad = item
	bad.ParentID = ""
	if _, err := h.KindUpsert(ctx, "recording", []core.KindItem{bad}); err == nil {
		t.Fatal("leaf without parent accepted")
	}

	var gotItems []core.KindItem
	env.m.SetKindBackends(KindBackends{
		KindUpsert: func(_ context.Context, pluginID, kindKey string, items []core.KindItem) (int, error) {
			if pluginID != rec.ID || kindKey != "recording" {
				t.Errorf("backend scope: %s %s", pluginID, kindKey)
			}
			gotItems = items
			return len(items), nil
		},
		KindItems: func(_ context.Context, _, _ string, _ string, limit int) (core.KindPage, error) {
			return core.KindPage{Items: gotItems}, nil
		},
		ResolveSources: func(_ context.Context, _ string, q core.SourceQuery) ([]core.SourceCandidate, error) {
			return []core.SourceCandidate{{Title: q.Query, Cached: true}}, nil
		},
		CachePut: func(_ context.Context, _, libraryKey, _ string, r io.Reader) (string, error) {
			b, err := io.ReadAll(r)
			return "blob:" + strings.Repeat("a", 64), errFrom(b, err)
		},
	})
	if n, err := h.KindUpsert(ctx, "recording", []core.KindItem{item}); err != nil || n != 1 {
		t.Fatalf("push upsert: %d %v", n, err)
	}
	if page, err := h.KindItems(ctx, "recording", "", 0); err != nil || len(page.Items) != 1 {
		t.Fatalf("read back: %+v %v", page, err)
	}
	// sources: search is granted, acquire is not.
	if _, err := h.ResolveSources(ctx, core.SourceQuery{Kind: "indexer_search", Query: "dawn"}); err != nil {
		t.Fatalf("source search: %v", err)
	}
	if _, err := h.ResolveSources(ctx, core.SourceQuery{Kind: "debrid_acquire", Magnet: "magnet:?xt=x"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("acquire beyond the search grant: %v", err)
	}
	// The cache namespace must be one of this plugin's own libraries.
	if _, err := h.CachePut(ctx, "pk-community-example-other-book", "cover", strings.NewReader("x")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("foreign cache namespace: %v", err)
	}
	if ref, err := h.CachePut(ctx, core.PluginLibraryKey(rec.ID, "recording"), "cover", strings.NewReader("x")); err != nil || !strings.HasPrefix(ref, "blob:") {
		t.Fatalf("own cache put: %q %v", ref, err)
	}
	if _, err := h.CacheOpen(ctx, "blob:not-a-hash"); err == nil {
		t.Fatal("malformed blob reference accepted")
	}
	// Switching the resolver role off denies source resolution.
	if _, err := env.m.SetRoles(ctx, rec.ID, []Capability{CapKindResolver}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ResolveSources(ctx, core.SourceQuery{Kind: "indexer_search", Query: "dawn"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("role off should deny: %v", err)
	}
}

func TestKindJournalAndUndo(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := installContent(t, env)
	h := env.m.HostFor(rec.ID)

	store := map[string]core.KindItem{}
	env.m.SetKindBackends(KindBackends{
		KindUpsert: func(_ context.Context, _, _ string, items []core.KindItem) (int, error) {
			for _, it := range items {
				store[it.ID] = it
			}
			return len(items), nil
		},
		KindDelete: func(_ context.Context, _, _ string, ids []string) error {
			for _, id := range ids {
				delete(store, id)
			}
			return nil
		},
		KindItems: func(_ context.Context, _, _, _ string, _ int) (core.KindPage, error) {
			var out []core.KindItem
			for _, it := range store {
				out = append(out, it)
			}
			return core.KindPage{Items: out}, nil
		},
	})

	item1 := core.KindItem{ID: "trk-1", Level: "track", ParentID: "alb-1", Title: "Dawn chorus", Playable: true}
	item2 := core.KindItem{ID: "trk-2", Level: "track", ParentID: "alb-1", Title: "Evening rain", Playable: true}
	if _, err := h.KindUpsert(ctx, "recording", []core.KindItem{item1, item2}); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	// Update item1 and delete item2.
	item1Updated := item1
	item1Updated.Title = "Dawn chorus (remastered)"
	if _, err := h.KindUpsert(ctx, "recording", []core.KindItem{item1Updated}); err != nil {
		t.Fatalf("update upsert: %v", err)
	}
	if err := h.KindDelete(ctx, "recording", []string{"trk-2"}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if len(store) != 1 || store["trk-1"].Title != "Dawn chorus (remastered)" {
		t.Fatalf("store before undo: %+v", store)
	}

	// UndoAll applies inverses in reverse order: first restores trk-2, then
	// reverts trk-1 to its original title, then deletes both newly created items.
	undone, err := env.m.UndoAll(ctx, rec.ID)
	if err != nil || undone != 3 {
		t.Fatalf("UndoAll = %d, err = %v", undone, err)
	}
	if len(store) != 0 {
		t.Fatalf("store after UndoAll must be empty, got %+v", store)
	}
}

func TestInstallMediaKindsUpgradeGateAndPurgeSetup(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := installContent(t, env)

	// Record a setup step row so Remove can verify plugins_setup purge.
	env.fake.routes["/ether/v1/setup/"] = func(req Request) (Response, error) {
		return Response{Status: 200, Body: json.RawMessage(`{"ok":true}`)}, nil
	}
	if _, err := env.m.TestSetupStep(ctx, rec.ID, "connect"); err != nil {
		t.Fatalf("TestSetupStep: %v", err)
	}

	// Non-additive upgrade (removing a field) is refused on update unless ReplaceLibraries is set.
	shrunk := contentManifest()
	shrunk.Version = "1.1.0"
	shrunk.Ether.Contributes.MediaKinds[0].Sync = KindSyncDecl{Mode: KindSyncPush}
	shrunk.Ether.Contributes.MediaKinds[0].Fields = shrunk.Ether.Contributes.MediaKinds[0].Fields[:2]
	rawShrunk, _ := json.Marshal(shrunk)
	pShrunk, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: rawShrunk, Files: map[string][]byte{"dist/plugin.js": []byte("// v1.1")}})
	if err != nil {
		t.Fatalf("PreviewInstall shrunk: %v", err)
	}
	if _, err := env.m.Install(ctx, pShrunk, InstallOptions{AcceptAddedPermissions: true}); err == nil {
		t.Fatal("expected non-additive kind upgrade to be refused without ReplaceLibraries")
	}
	if _, err := env.m.Install(ctx, pShrunk, InstallOptions{AcceptAddedPermissions: true, ReplaceLibraries: true}); err != nil {
		t.Fatalf("upgrade with ReplaceLibraries: %v", err)
	}

	// Purge removes plugins_setup rows.
	if err := env.m.Remove(ctx, rec.ID, true); err != nil {
		t.Fatalf("Remove purge: %v", err)
	}
	state, err := env.m.store.setupState(ctx, rec.ID)
	if err != nil || len(state) != 0 {
		t.Fatalf("setup rows after purge: %+v %v", state, err)
	}
}

func TestHostedRuntimeKindAndCacheBridges(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	eng := wasm.NewEngine(nil)
	t.Cleanup(func() { _ = eng.Close() })
	env.m.RegisterRuntime("hosted", HostedRuntimeFactory(eng))

	var upserted []core.KindItem
	var deleted []string
	blobs := map[string][]byte{}
	env.m.SetKindBackends(KindBackends{
		KindUpsert: func(_ context.Context, _, _ string, items []core.KindItem) (int, error) {
			upserted = append(upserted, items...)
			return len(items), nil
		},
		KindDelete: func(_ context.Context, _, _ string, ids []string) error {
			deleted = append(deleted, ids...)
			return nil
		},
		KindItems: func(_ context.Context, _, _, _ string, _ int) (core.KindPage, error) {
			return core.KindPage{Items: upserted}, nil
		},
		ResolveSources: func(_ context.Context, _ string, q core.SourceQuery) ([]core.SourceCandidate, error) {
			return []core.SourceCandidate{{InfoHash: "c1", Title: q.Query, Cached: true}}, nil
		},
		CachePut: func(_ context.Context, _, _, _ string, r io.Reader) (string, error) {
			b, _ := io.ReadAll(r)
			ref := "blob:" + strings.Repeat("b", 64)
			blobs[ref] = b
			return ref, nil
		},
		CacheOpen: func(_ context.Context, _, ref string) (io.ReadSeekCloser, error) {
			return struct {
				*bytes.Reader
				io.Closer
			}{Reader: bytes.NewReader(blobs[ref]), Closer: io.NopCloser(nil)}, nil
		},
	})

	man := contentManifest()
	man.Ether.Contributes.MediaKinds[0].Sync = KindSyncDecl{Mode: KindSyncPush}
	man.Ether.Contributes.Setup = nil
	raw, _ := json.Marshal(man)
	js := `
function handle(req) {
  const up = ether.kinds.upsert("recording", [{ id: "trk-1", level: "track", parentId: "alb-1", title: "Chorus", playable: true }]);
  const page = ether.kinds.items("recording", "", 10);
  ether.kinds.delete("recording", ["trk-1"]);
  const src = ether.sources.resolve({ kind: "indexer_search", query: "chorus" });
  const put = ether.cache.put("pk-community-example-library-recording", "note.txt", "hello-blob");
  const opened = ether.cache.open(put.blob);
  return { status: 200, body: JSON.stringify({ accepted: up.accepted, count: page.items.length, cand: src.candidates[0].title, body: opened.body }) };
}
`
	p, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"dist/plugin.js": []byte(js)}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, p, InstallOptions{AcceptAddedPermissions: true})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := env.m.Call(ctx, rec.ID, Request{Method: "POST", Path: "/ether/v1/kinds/recording/sync"})
	if err != nil || resp.Status != 200 {
		t.Fatalf("hosted call: %+v %v", resp, err)
	}
	var body struct {
		Accepted int    `json:"accepted"`
		Count    int    `json:"count"`
		Cand     string `json:"cand"`
		Body     string `json:"body"`
	}
	var rawStr string
	if json.Unmarshal(resp.Body, &rawStr) == nil {
		_ = json.Unmarshal([]byte(rawStr), &body)
	} else {
		_ = json.Unmarshal(resp.Body, &body)
	}
	if body.Accepted != 1 || body.Count != 1 || body.Cand != "chorus" || body.Body != "hello-blob" || len(deleted) != 1 {
		t.Fatalf("unexpected hosted kind bridge output: %+v deleted=%v raw=%s", body, deleted, string(resp.Body))
	}
}

// The kind host methods on the shared in-memory test host
// (hosted_test.go) are unavailable, matching a server with no kind
// backends wired.
func (h *memHost) KindUpsert(context.Context, string, []core.KindItem) (int, error) {
	return 0, ErrUnavailable
}
func (h *memHost) KindDelete(context.Context, string, []string) error { return ErrUnavailable }
func (h *memHost) KindItems(context.Context, string, string, int) (core.KindPage, error) {
	return core.KindPage{}, ErrUnavailable
}
func (h *memHost) ResolveSources(context.Context, core.SourceQuery) ([]core.SourceCandidate, error) {
	return nil, ErrUnavailable
}
func (h *memHost) CachePut(context.Context, string, string, io.Reader) (string, error) {
	return "", ErrUnavailable
}
func (h *memHost) CacheOpen(context.Context, string) (io.ReadSeekCloser, error) {
	return nil, ErrUnavailable
}

func errFrom(b []byte, err error) error {
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return errors.New("empty blob")
	}
	return nil
}

func TestSetupStepGate(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := installContent(t, env)

	blocked, err := env.m.SetupBlockers(ctx, rec.ID)
	if err != nil || len(blocked) != 1 || blocked[0] != "connect" {
		t.Fatalf("fresh install must be blocked on setup: %v %v", blocked, err)
	}
	env.fake.routes["/ether/v1/setup/"] = func(req Request) (Response, error) {
		var body struct {
			Fields map[string]any `json:"fields"`
		}
		_ = json.Unmarshal(req.Body, &body)
		if body.Fields["endpoint"] == "https://example.test" {
			return Response{Status: 200, Body: json.RawMessage(`{"ok":true}`)}, nil
		}
		return Response{Status: 200, Body: json.RawMessage(`{"ok":false,"detail":"No answer from the endpoint"}`)}, nil
	}
	res, err := env.m.TestSetupStep(ctx, rec.ID, "connect")
	if err != nil || res.OK {
		t.Fatalf("failed test must record a failure: %+v %v", res, err)
	}
	if blocked, _ := env.m.SetupBlockers(ctx, rec.ID); len(blocked) != 1 {
		t.Fatalf("failed step must keep blocking: %v", blocked)
	}
	if _, err := env.m.Configure(ctx, rec.ID, map[string]any{"endpoint": "https://example.test", "api_key": "k"}); err != nil {
		t.Fatal(err)
	}
	res, err = env.m.TestSetupStep(ctx, rec.ID, "connect")
	if err != nil || !res.OK {
		t.Fatalf("configured test: %+v %v", res, err)
	}
	if blocked, _ := env.m.SetupBlockers(ctx, rec.ID); len(blocked) != 0 {
		t.Fatalf("passing step must unblock: %v", blocked)
	}
	state, err := env.m.SetupState(ctx, rec.ID)
	if err != nil || len(state) != 1 || state[0].Status != SetupOK {
		t.Fatalf("setup state: %+v %v", state, err)
	}
}
