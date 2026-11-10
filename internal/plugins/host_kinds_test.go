package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
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

	// No backend wired yet: granted calls surface ErrUnavailable, not a panic.
	item := KindItem{ID: "trk-1", Level: "track", ParentID: "alb-1", Title: "Dawn chorus", Playable: true}
	if _, err := h.KindUpsert(ctx, "recording", []KindItem{item}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("upsert without backend: %v", err)
	}
	// An undeclared kind and a bad envelope are refused before any backend.
	if _, err := h.KindUpsert(ctx, "book", []KindItem{item}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("undeclared kind: %v", err)
	}
	bad := item
	bad.ID = "has:colon"
	if _, err := h.KindUpsert(ctx, "recording", []KindItem{bad}); err == nil {
		t.Fatal("bad external id accepted")
	}
	bad = item
	bad.ParentID = ""
	if _, err := h.KindUpsert(ctx, "recording", []KindItem{bad}); err == nil {
		t.Fatal("leaf without parent accepted")
	}

	var gotItems []KindItem
	env.m.SetKindBackends(KindBackends{
		KindUpsert: func(_ context.Context, pluginID, kindKey string, items []KindItem) (int, error) {
			if pluginID != rec.ID || kindKey != "recording" {
				t.Errorf("backend scope: %s %s", pluginID, kindKey)
			}
			gotItems = items
			return len(items), nil
		},
		KindItems: func(_ context.Context, _, _ string, _ string, limit int) (KindPage, error) {
			return KindPage{Items: gotItems}, nil
		},
		ResolveSources: func(_ context.Context, _ string, q SourceQuery) ([]SourceCandidate, error) {
			return []SourceCandidate{{Title: q.Query, Cached: true}}, nil
		},
		CachePut: func(_ context.Context, _, libraryKey, _ string, r io.Reader) (string, error) {
			b, err := io.ReadAll(r)
			return "blob:" + strings.Repeat("a", 64), errFrom(b, err)
		},
	})
	if n, err := h.KindUpsert(ctx, "recording", []KindItem{item}); err != nil || n != 1 {
		t.Fatalf("push upsert: %d %v", n, err)
	}
	if page, err := h.KindItems(ctx, "recording", "", 0); err != nil || len(page.Items) != 1 {
		t.Fatalf("read back: %+v %v", page, err)
	}
	// sources: search is granted, acquire is not.
	if _, err := h.ResolveSources(ctx, SourceQuery{Kind: "indexer_search", Query: "dawn"}); err != nil {
		t.Fatalf("source search: %v", err)
	}
	if _, err := h.ResolveSources(ctx, SourceQuery{Kind: "debrid_acquire", Magnet: "magnet:?xt=x"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("acquire beyond the search grant: %v", err)
	}
	// The cache namespace must be one of this plugin's own libraries.
	if _, err := h.CachePut(ctx, "pk-community.example.other-book", "cover", strings.NewReader("x")); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("foreign cache namespace: %v", err)
	}
	if ref, err := h.CachePut(ctx, KindLibraryKey(rec.ID, "recording"), "cover", strings.NewReader("x")); err != nil || !strings.HasPrefix(ref, "blob:") {
		t.Fatalf("own cache put: %q %v", ref, err)
	}
	if _, err := h.CacheOpen(ctx, "blob:not-a-hash"); err == nil {
		t.Fatal("malformed blob reference accepted")
	}
	// Switching the resolver role off denies source resolution.
	if _, err := env.m.SetRoles(ctx, rec.ID, []Capability{CapKindResolver}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.ResolveSources(ctx, SourceQuery{Kind: "indexer_search", Query: "dawn"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("role off should deny: %v", err)
	}
}

// The kind host methods on the shared in-memory test host
// (hosted_test.go) are unavailable, matching a server with no kind
// backends wired.
func (h *memHost) KindUpsert(context.Context, string, []KindItem) (int, error) {
	return 0, ErrUnavailable
}
func (h *memHost) KindDelete(context.Context, string, []string) error { return ErrUnavailable }
func (h *memHost) KindItems(context.Context, string, string, int) (KindPage, error) {
	return KindPage{}, ErrUnavailable
}
func (h *memHost) ResolveSources(context.Context, SourceQuery) ([]SourceCandidate, error) {
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
