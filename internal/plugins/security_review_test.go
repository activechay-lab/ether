package plugins

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/agent/tools"
	"github.com/braedonsaunders/ether/internal/config"
)

func TestHostFetchRejectsRedirectOutsideNetworkGrant(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://not-granted.invalid/secret", http.StatusFound)
	}))
	defer server.Close()
	man := baseManifest()
	man.Ether.Permissions.Network = []string{"127.0.0.1"}
	raw, _ := json.Marshal(man)
	p, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"dist/plugin.js": []byte("// x")}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, p, InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	env.m.client = server.Client()
	_, err = env.m.HostFor(rec.ID).HTTPFetch(ctx, FetchRequest{URL: server.URL})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("redirect outside grant: %v", err)
	}
}

func TestFolderHostRejectsSymlinkEscape(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	root, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "private"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	man := baseManifest()
	man.Ether.Permissions.Folders = "read"
	raw, _ := json.Marshal(man)
	p, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"dist/plugin.js": []byte("// x")}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, p, InstallOptions{Grants: Grants{Folders: []string{root}}})
	if err != nil {
		t.Fatal(err)
	}
	h := env.m.HostFor(rec.ID)
	if err := os.Mkdir(filepath.Join(root, "inside"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside", filepath.Join(root, "safe")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.FoldersList(ctx, root, "safe"); err != nil {
		t.Fatalf("internal symlink: %v", err)
	}
	if _, err := h.FoldersList(ctx, root, "escape"); err == nil {
		t.Error("listed outside root through symlink")
	}
	for _, path := range []string{"escape/private", "../private", "inside/../private"} {
		if _, err := h.FoldersHash(ctx, root, path); err == nil {
			t.Errorf("hashed escaping path %q", path)
		}
	}
	if _, err := h.FoldersStat(ctx, root, "escape/private"); err == nil {
		t.Error("stat outside root through symlink")
	}
}

func TestPluginLogDoesNotExposeSavedSecrets(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")
	const secret = "private-value-472815"
	if _, err := env.m.Configure(ctx, rec.ID, map[string]any{"api_token": secret}); err != nil {
		t.Fatal(err)
	}
	if err := env.m.HostFor(rec.ID).Log(ctx, "info", "value: "+secret, nil); err != nil {
		t.Fatal(err)
	}
	logs, err := env.m.store.logs(ctx, rec.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range logs {
		if strings.Contains(line, secret) {
			t.Fatal("saved secret exposed in plugin logs")
		}
	}
}

func TestPluginInspectorAndNoticeRedactSavedSecrets(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")
	const secret = "private-value-472815"
	if _, err := env.m.Configure(ctx, rec.ID, map[string]any{"api_token": secret}); err != nil {
		t.Fatal(err)
	}
	env.fake.routes["/ether/view/test"] = func(Request) (Response, error) {
		return Response{Status: 200, Body: json.RawMessage(`{"message":"private-value-472815"}`)}, nil
	}
	if _, err := env.m.Call(ctx, rec.ID, Request{Method: "GET", Path: "/ether/view/test"}); err != nil {
		t.Fatal(err)
	}
	if err := env.m.HostFor(rec.ID).NotifyOwner(ctx, "message "+secret); err != nil {
		t.Fatal(err)
	}
	calls, err := env.m.store.calls(ctx, rec.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if strings.Contains(c.Response, secret) {
			t.Fatal("saved secret exposed in inspector")
		}
	}
	for _, notice := range env.notices {
		if strings.Contains(notice, secret) {
			t.Fatal("saved secret exposed in notice")
		}
	}
}

func TestPluginToolDoesNotSendSavedSecretToModel(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")
	const secret = "private-value-472815"
	if _, err := env.m.Configure(ctx, rec.ID, map[string]any{"api_token": secret}); err != nil {
		t.Fatal(err)
	}
	env.fake.routes["/ether/tool/leak"] = func(Request) (Response, error) {
		return Response{Status: 200, Body: json.RawMessage(`{"message":"private-value-472815"}`)}, nil
	}
	tool := env.m.pluginTool(rec, ToolMeta{Name: "leak", Title: "Leak", Description: "test", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`)})
	result, err := tool.Execute(ctx, tools.Principal{}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result.Data)
	if strings.Contains(string(raw), secret) {
		t.Fatal("saved secret exposed to model")
	}
}

func TestPluginViewsAndActionsDoNotExposeSavedSecrets(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")
	const secret = "private-value-472815"
	if _, err := env.m.Configure(ctx, rec.ID, map[string]any{"api_token": secret}); err != nil {
		t.Fatal(err)
	}
	env.fake.routes["/ether/view/home"] = func(Request) (Response, error) {
		return Response{Status: 200, Body: json.RawMessage(`{"spec":{"specVersion":1,"layout":"list","header":[],"body":[]},"data":{"message":"private-value-472815"}}`)}, nil
	}
	env.fake.routes["/ether/action/change"] = func(Request) (Response, error) {
		return Response{Status: 200, Body: json.RawMessage(`{"toast":"private-value-472815"}`)}, nil
	}
	for _, endpoint := range []struct{ method, path string }{{"GET", "/" + rec.ID + "/views/home"}, {"POST", "/" + rec.ID + "/actions/change"}} {
		w := httptest.NewRecorder()
		env.m.Handler().ServeHTTP(w, httptest.NewRequest(endpoint.method, endpoint.path, strings.NewReader(`{}`)))
		if w.Code != 200 {
			t.Fatalf("%s: status %d", endpoint.path, w.Code)
		}
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("saved secret exposed in %s", endpoint.path)
		}
	}
}

func TestKVRejectsUnboundedKeys(t *testing.T) {
	env := newTestEnv(t)
	rec := env.installHello(t, "1.0.0")
	err := env.m.HostFor(rec.ID).KVPut(context.Background(), strings.Repeat("x", 1<<20), []byte("x"))
	if err == nil {
		t.Fatal("accepted a megabyte key with no quota charge")
	}
}

func TestPluginSourceURLIsSealedAndBoundToPlugin(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	sealer, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sealer.UpgradeCredentials(ctx); err != nil {
		t.Fatal(err)
	}
	env.m.store.sealer = sealer
	rec := env.installHello(t, "1.0.0")
	const secretURL = "https://example.com/private-configuration/manifest.json"
	rec.SourceURL = secretURL
	if err := env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := env.m.store.db.QueryRowContext(ctx, `SELECT source_url FROM plugins_installed WHERE id=?`, rec.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "private-configuration") {
		t.Fatal("source URL stored unsealed")
	}
	if _, _, err := config.OpenWith(sealer, stored, "plugins_installed", "source_url", "other-plugin"); err == nil {
		t.Fatal("source URL not bound to plugin")
	}
	restored, err := env.m.store.record(ctx, rec.ID)
	if err != nil || restored.SourceURL != secretURL {
		t.Fatalf("source URL not restored: %v", err)
	}
}

func TestInstallFetchDoesNotExposeSecretURL(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	const secret = "private-config-472815"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "missing", http.StatusNotFound) }))
	defer srv.Close()
	env.m.client = srv.Client()
	_, err := env.m.fetch(ctx, srv.URL+"/"+secret+"/manifest.json", false, 1024)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("fetch error exposed URL: %v", err)
	}
}

func TestInstallRedirectDoesNotSendSourceURLAsReferer(t *testing.T) {
	env := newTestEnv(t)
	const secret = "private-config-472815"
	seen := make(chan string, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen <- r.Referer(); w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	env.m.client = source.Client()
	if _, err := env.m.fetch(context.Background(), source.URL+"/"+secret, false, 1024); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(<-seen, secret) {
		t.Fatal("source secret sent in redirect Referer")
	}
}

func TestPluginRuntimeErrorsDoNotExposeSavedSecrets(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")
	const secret = "private-value-472815"
	if _, err := env.m.Configure(ctx, rec.ID, map[string]any{"api_token": secret}); err != nil {
		t.Fatal(err)
	}
	env.fake.fail = errors.New("failure: " + secret)
	_, err := env.m.Call(ctx, rec.ID, Request{Path: "/ether/view/home"})
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("runtime error exposed saved secret: %v", err)
	}
}

type securityRuntime struct {
	call func(context.Context, Request) (Response, error)
}

func (r securityRuntime) Call(ctx context.Context, req Request) (Response, error) {
	return r.call(ctx, req)
}
func (r securityRuntime) Close() error { return nil }

func TestHostedReadOnlyToolCannotWriteCollections(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")
	rec.Manifest.Ether.Permissions.Collections = "write"
	rec.ManifestRaw, _ = json.Marshal(rec.Manifest)
	if err := env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	wrote := false
	env.m.deps.Backends.CollectionsUpsert = func(context.Context, json.RawMessage) (json.RawMessage, error) { wrote = true; return nil, nil }
	var hostErr error
	env.m.mu.Lock()
	env.m.runtimes[rec.ID] = securityRuntime{call: func(ctx context.Context, _ Request) (Response, error) {
		hostErr = env.m.HostFor(rec.ID).CollectionsUpsert(ctx, json.RawMessage(`{}`))
		return Response{Status: 200, Body: json.RawMessage(`{}`)}, nil
	}}
	env.m.mu.Unlock()
	tool := env.m.pluginTool(rec, ToolMeta{Name: "read", Title: "Read", Description: "read", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`)})
	if _, err := tool.Execute(ctx, tools.Principal{}, map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if wrote || !errors.Is(hostErr, ErrPermissionDenied) {
		t.Fatalf("read-only tool write: wrote=%v err=%v", wrote, hostErr)
	}
}

func TestRemoteReadOnlyToolRequiresApproval(t *testing.T) {
	env := newTestEnv(t)
	rec := env.installHello(t, "1.0.0")
	rec.Manifest.Ether.Runtime.Kind = "remote"
	tool := env.m.pluginTool(rec, ToolMeta{Name: "read", Title: "Read", Description: "read", ReadOnly: true, InputSchema: json.RawMessage(`{"type":"object"}`)})
	result, err := tool.Execute(context.Background(), tools.Principal{}, map[string]any{})
	if err != nil || result.Proposal == nil || tool.ReadOnly {
		t.Fatalf("remote read tool should propose: %+v %v", result, err)
	}
}

func TestPluginSourceURLDoesNotReachInspector(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	rec := env.installHello(t, "1.0.0")
	const sourceURL = "https://example.com/private-config-472815/manifest.json"
	rec.SourceURL = sourceURL
	if err := env.m.store.upsert(ctx, rec); err != nil {
		t.Fatal(err)
	}
	env.fake.routes["/ether/view/home"] = func(Request) (Response, error) {
		return Response{Status: 200, Body: json.RawMessage(`{"message":"https://example.com/private-config-472815/manifest.json"}`)}, nil
	}
	if _, err := env.m.Call(ctx, rec.ID, Request{Path: "/ether/view/home"}); err != nil {
		t.Fatal(err)
	}
	calls, err := env.m.store.calls(ctx, rec.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range calls {
		if strings.Contains(c.Response, "private-config-472815") {
			t.Fatal("source URL exposed in inspector")
		}
	}
}

func TestPackageRejectsDuplicateFiles(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for _, content := range []string{"{}", "{\"id\":\"different\"}"} {
		f, err := z.Create("manifest.json")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readPkg(buf.Bytes()); err == nil {
		t.Fatal("accepted duplicate package paths")
	}
}

func TestPackageRejectsOversizedEntry(t *testing.T) {
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	f, err := z.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.Write([]byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	pkg := buf.Bytes()
	index := bytes.LastIndex(pkg, []byte{'P', 'K', 1, 2})
	if index < 0 {
		t.Fatal("missing zip directory")
	}
	binary.LittleEndian.PutUint32(pkg[index+24:index+28], (64<<20)+1)
	if _, _, err = readPkg(pkg); err == nil {
		t.Fatal("accepted oversized entry")
	}
}
