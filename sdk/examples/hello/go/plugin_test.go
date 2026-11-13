package hello_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/plugins"
	"github.com/braedonsaunders/ether/internal/plugins/wasm"
	"github.com/braedonsaunders/ether/internal/viewspec"
	hello "github.com/braedonsaunders/ether/sdk/examples/hello/go"
	etherplugin "github.com/braedonsaunders/ether/sdk/go"
)

// noopHost answers the host calls this example makes; any other Host method
// panics through the nil embedded interface.
type noopHost struct{ plugins.Host }

type parityHost struct {
	noopHost
	settings map[string]any
}

func (h *parityHost) Settings(context.Context) (map[string]any, error) { return h.settings, nil }

func (noopHost) KVGet(context.Context, string) ([]byte, bool, error) { return nil, false, nil }
func (noopHost) KVPut(context.Context, string, []byte) error         { return nil }
func (noopHost) KVDelete(context.Context, string) error              { return nil }
func (noopHost) KVList(context.Context, string) ([]string, error)    { return nil, nil }
func (noopHost) SecretGet(context.Context, string) (string, error) {
	return "", plugins.ErrPermissionDenied
}
func (noopHost) HTTPFetch(context.Context, plugins.FetchRequest) (plugins.FetchResponse, error) {
	return plugins.FetchResponse{}, plugins.ErrPermissionDenied
}
func (noopHost) CatalogSearch(context.Context, string, int, int) (json.RawMessage, error) {
	return json.RawMessage(`{"titles":[]}`), nil
}
func (noopHost) CatalogGet(context.Context, string) (json.RawMessage, error) { return nil, nil }
func (noopHost) ViewerGranted(context.Context) (json.RawMessage, error) {
	return json.RawMessage(`[]`), nil
}
func (noopHost) ViewerHistory(context.Context, int64, int, int) (json.RawMessage, error) {
	return nil, plugins.ErrPermissionDenied
}
func (noopHost) SetWatchState(context.Context, int64, string, bool) error {
	return plugins.ErrPermissionDenied
}
func (noopHost) CollectionsUpsert(context.Context, json.RawMessage) error {
	return plugins.ErrPermissionDenied
}
func (noopHost) FoldersList(context.Context, string, string) ([]plugins.FolderEntry, error) {
	return nil, plugins.ErrPermissionDenied
}
func (noopHost) FoldersStat(context.Context, string, string) (plugins.FolderEntry, error) {
	return plugins.FolderEntry{}, plugins.ErrPermissionDenied
}
func (noopHost) Settings(context.Context) (map[string]any, error)          { return map[string]any{}, nil }
func (noopHost) NotifyOwner(context.Context, string) error                 { return nil }
func (noopHost) Log(context.Context, string, string, map[string]any) error { return nil }

func sdkRootDir(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func loadTypeScriptHelloFiles(t *testing.T) map[string]string {
	t.Helper()
	sdkRoot := sdkRootDir(t)
	files := make(map[string]string)

	tsSrcDir := filepath.Join(sdkRoot, "typescript", "src")
	entries, err := os.ReadDir(tsSrcDir)
	if err != nil {
		t.Fatalf("read typescript/src: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".ts") || strings.HasSuffix(entry.Name(), ".test.ts") || strings.HasSuffix(entry.Name(), ".d.ts") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(tsSrcDir, entry.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", entry.Name(), err)
		}
		files["typescript/src/"+entry.Name()] = string(raw)
	}

	helloTSPath := filepath.Join(sdkRoot, "examples", "hello", "typescript", "plugin.ts")
	helloTSRaw, err := os.ReadFile(helloTSPath)
	if err != nil {
		t.Fatalf("read examples/hello/typescript/plugin.ts: %v", err)
	}
	files["examples/hello/typescript/plugin.ts"] = string(helloTSRaw)
	return files
}

func TestHelloManifestsAndViewSpecValidate(t *testing.T) {
	sdkRoot := sdkRootDir(t)
	for _, lang := range []string{"typescript", "python", "go"} {
		manifestPath := filepath.Join(sdkRoot, "examples", "hello", lang, "manifest.json")
		raw, err := os.ReadFile(manifestPath)
		if err != nil {
			t.Fatalf("read %s manifest: %v", lang, err)
		}
		var m plugins.Manifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal %s manifest: %v", lang, err)
		}
		if err := m.Validate(); err != nil {
			t.Fatalf("%s manifest.Validate() failed: %v", lang, err)
		}
		if m.Ether == nil || m.Ether.Settings == nil {
			t.Fatalf("%s manifest missing ether.settings", lang)
		}
		if diags := plugins.LintSettingsSchema(m.Ether.Settings); diags != nil {
			t.Fatalf("%s LintSettingsSchema diagnostics: %+v", lang, diags)
		}
	}

	p := hello.NewHelloPlugin()
	h := etherplugin.NewTestHarness(p, nil)
	viewRes := h.Request(etherplugin.RawRequest{Method: "GET", Path: "/ether/view/home"})
	if viewRes.Status != http.StatusOK {
		t.Fatalf("GET /ether/view/home status = %d, body = %s", viewRes.Status, viewRes.Body)
	}
	var payload struct {
		Spec json.RawMessage `json:"spec"`
	}
	if err := json.Unmarshal(viewRes.Body, &payload); err != nil {
		t.Fatalf("unmarshal view response: %v", err)
	}
	if result := viewspec.ValidatePlugin(payload.Spec); !result.OK {
		t.Fatalf("viewspec.ValidatePlugin failed: %v", result.Errors)
	}
}

func TestTypeScriptHelloCompiledWithWasmCompileInHostedRuntime(t *testing.T) {
	files := loadTypeScriptHelloFiles(t)
	compiledJS, sourcemap, diags := wasm.Compile(files, "examples/hello/typescript/plugin.ts")
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("wasm.Compile diagnostic error: %+v", d)
		}
	}
	if len(compiledJS) == 0 {
		t.Fatal("wasm.Compile returned empty JS bundle")
	}
	if len(sourcemap) == 0 {
		t.Fatal("wasm.Compile returned empty sourcemap")
	}

	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })

	sdkRoot := sdkRootDir(t)
	manifestRaw, err := os.ReadFile(filepath.Join(sdkRoot, "examples", "hello", "typescript", "manifest.json"))
	if err != nil {
		t.Fatalf("read typescript manifest.json: %v", err)
	}
	var m plugins.Manifest
	if err := json.Unmarshal(manifestRaw, &m); err != nil {
		t.Fatalf("unmarshal typescript manifest.json: %v", err)
	}

	inst := plugins.Installed{
		ID:       m.ID,
		Manifest: m,
		Files: func(name string) ([]byte, bool) {
			if name == "plugin.ts" || name == "plugin.js" {
				return compiledJS, true
			}
			return nil, false
		},
		Host: noopHost{},
	}

	rt, err := plugins.HostedRuntimeFactory(eng)(inst)
	if err != nil {
		t.Fatalf("HostedRuntimeFactory failed: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	ctx := context.Background()

	// 1. /ether/health
	healthResp, err := rt.Call(ctx, plugins.Request{Method: "GET", Path: "/ether/health"})
	if err != nil {
		t.Fatalf("Call /ether/health: %v", err)
	}
	if healthResp.Status != http.StatusOK {
		t.Fatalf("/ether/health status = %d, body = %s", healthResp.Status, healthResp.Body)
	}

	// 2. /ether/test
	testResp, err := rt.Call(ctx, plugins.Request{Method: "POST", Path: "/ether/test"})
	if err != nil {
		t.Fatalf("Call /ether/test: %v", err)
	}
	if testResp.Status != http.StatusOK {
		t.Fatalf("/ether/test status = %d, body = %s", testResp.Status, testResp.Body)
	}

	// 3. /catalog/movie/hello.json
	catResp, err := rt.Call(ctx, plugins.Request{Method: "GET", Path: "/catalog/movie/hello.json"})
	if err != nil {
		t.Fatalf("Call /catalog/movie/hello.json: %v", err)
	}
	if catResp.Status != http.StatusOK || !strings.Contains(string(catResp.Body), "Hello from Ether") {
		t.Fatalf("/catalog/movie/hello.json status = %d, body = %s", catResp.Status, catResp.Body)
	}

	// 4. /subtitles/movie/tt0133093.json without and with X-Ether-Pick
	subDenied, err := rt.Call(ctx, plugins.Request{Method: "GET", Path: "/subtitles/movie/tt0133093.json"})
	if err != nil {
		t.Fatalf("Call /subtitles without pick: %v", err)
	}
	if subDenied.Status != http.StatusOK {
		t.Fatalf("expected listing without X-Ether-Pick, got %d (%s)", subDenied.Status, subDenied.Body)
	}
	denied, err := rt.Call(ctx, plugins.Request{Method: "POST", Path: "/ether/subtitles/download", Body: json.RawMessage(`{"id":"sub","lang":"en","format":"srt"}`)})
	if err != nil || denied.Status != http.StatusForbidden {
		t.Fatalf("download without pick: %+v %v", denied, err)
	}
	subOK, err := rt.Call(ctx, plugins.Request{
		Method:  "GET",
		Path:    "/subtitles/movie/tt0133093.json",
		Headers: map[string]string{"X-Ether-Pick": "pick-123"},
	})
	if err != nil {
		t.Fatalf("Call /subtitles with pick: %v", err)
	}
	if subOK.Status != http.StatusOK {
		t.Fatalf("/subtitles with pick status = %d, body = %s", subOK.Status, subOK.Body)
	}

	// 5. Hot path: RankBatch and CondMatch
	hot, ok := rt.(plugins.HotRuntime)
	if !ok || !hot.HasRankKey() || !hot.HasCondMatch() {
		t.Fatalf("expected HotRuntime with rank_key and cond_match, got %T", rt)
	}

}

func startPythonHelloSidecar(t *testing.T) string {
	t.Helper()
	pythonBin, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not found on PATH")
	}
	sdkRoot := sdkRootDir(t)
	pluginPy := filepath.Join(sdkRoot, "examples", "hello", "python", "plugin.py")

	script := `
import importlib.util, sys
spec = importlib.util.spec_from_file_location("hello_plugin_mod", sys.argv[1])
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)
from ether_plugin import serve
srv = serve(mod.hello_plugin, host="127.0.0.1", port=0)
print(srv.url, flush=True)
sys.stdin.read()
srv.close()
`
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, pythonBin, "-c", script, pluginPy)
	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdin pipe: %v", err)
	}
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("stdout pipe: %v", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start python sidecar: %v", err)
	}
	t.Cleanup(func() {
		_ = stdinPipe.Close()
		cancel()
		_ = cmd.Wait()
	})

	lineCh := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		if scanner.Scan() {
			lineCh <- strings.TrimSpace(scanner.Text())
		}
	}()

	select {
	case url := <-lineCh:
		if url == "" {
			t.Fatalf("python sidecar printed empty URL; stderr: %s", stderrBuf.String())
		}
		return url
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for python sidecar startup; stderr: %s", stderrBuf.String())
		return ""
	}
}

func TestCrossLanguageHelloParity(t *testing.T) {
	// 1. Hosted TypeScript runtime
	files := loadTypeScriptHelloFiles(t)
	compiledJS, _, diags := wasm.Compile(files, "examples/hello/typescript/plugin.ts")
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("wasm.Compile error: %+v", d)
		}
	}
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { _ = eng.Close() })

	hostSettings := &parityHost{settings: map[string]any{}}
	inst := plugins.Installed{
		ID: "org.ether.examples.hello",
		Manifest: plugins.Manifest{
			ID:      "org.ether.examples.hello",
			Version: "0.1.0",
			Ether:   &plugins.EtherBlock{Runtime: plugins.RuntimeDecl{Kind: "hosted", Entry: "plugin.js"}},
		},
		Files: func(name string) ([]byte, bool) {
			if name == "plugin.js" {
				return compiledJS, true
			}
			return nil, false
		},
		Host: hostSettings,
	}
	hostedRT, err := plugins.HostedRuntimeFactory(eng)(inst)
	if err != nil {
		t.Fatalf("HostedRuntimeFactory: %v", err)
	}
	t.Cleanup(func() { _ = hostedRT.Close() })

	// 2. Go HTTP sidecar
	goPlugin := hello.NewHelloPlugin()
	goSrv := httptest.NewServer(goPlugin)
	t.Cleanup(goSrv.Close)

	// 3. Python HTTP sidecar
	pyURL := startPythonHelloSidecar(t)

	type testCase struct {
		name       string
		method     string
		path       string
		headers    map[string]string
		body       string
		wantStatus int
	}

	cases := []testCase{
		{
			name:       "health",
			method:     "GET",
			path:       "/ether/health",
			wantStatus: http.StatusOK,
		},
		{
			name:       "test",
			method:     "POST",
			path:       "/ether/test",
			wantStatus: http.StatusOK,
		},
		{
			name:       "catalog default",
			method:     "GET",
			path:       "/catalog/movie/hello.json",
			wantStatus: http.StatusOK,
		},
		{
			name:       "catalog search",
			method:     "GET",
			path:       "/catalog/movie/hello/search=World.json",
			wantStatus: http.StatusOK,
		},
		{
			name:       "catalog search with escaped ampersand",
			method:     "GET",
			path:       "/catalog/movie/hello/search=fish%26chips.json",
			wantStatus: http.StatusOK,
		},
		{
			name:       "meta",
			method:     "GET",
			path:       "/meta/movie/tt0133093.json",
			wantStatus: http.StatusOK,
		},
		{
			name:       "subtitles without pick",
			method:     "GET",
			path:       "/subtitles/movie/tt0133093.json",
			wantStatus: http.StatusOK,
		},
		{
			name:       "subtitles with pick",
			method:     "GET",
			path:       "/subtitles/movie/tt0133093.json",
			headers:    map[string]string{"X-Ether-Pick": "pick-token-42"},
			wantStatus: http.StatusOK,
		},
		{
			name:       "view home",
			method:     "GET",
			path:       "/ether/view/home",
			wantStatus: http.StatusOK,
		},
		{
			name:       "action greet",
			method:     "POST",
			path:       "/ether/action/greet",
			body:       `{}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "configure greeting",
			method:     "POST",
			path:       "/ether/configure",
			body:       `{"settings":{"greeting":"Welcome"}}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "catalog after configure",
			method:     "GET",
			path:       "/catalog/movie/hello.json",
			wantStatus: http.StatusOK,
		},
		{
			name:       "rank-key cached",
			method:     "POST",
			path:       "/ether/rank-key",
			body:       `{"candidate":{"releaseId":"r1","cached":true}}`,
			wantStatus: http.StatusOK,
		},
		{
			name:       "cond-match cached",
			method:     "POST",
			path:       "/ether/cond-match",
			body:       `{"candidate":{"releaseId":"r1","cached":true},"operator":"eq","value":true}`,
			wantStatus: http.StatusOK,
		},
	}

	callHTTP := func(baseURL string, tc testCase) (int, any) {
		var reqBody io.Reader
		if tc.body != "" {
			reqBody = strings.NewReader(tc.body)
		}
		req, err := http.NewRequest(tc.method, baseURL+tc.path, reqBody)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		for k, v := range tc.headers {
			req.Header.Set(k, v)
		}
		if tc.body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("http call %s%s: %v", baseURL, tc.path, err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		var parsed any
		if err := json.Unmarshal(raw, &parsed); err != nil {
			t.Fatalf("unmarshal json from %s (%s): %v", baseURL, string(raw), err)
		}
		return resp.StatusCode, parsed
	}

	callHosted := func(tc testCase) (int, any) {
		if tc.path == "/ether/configure" {
			hostSettings.settings = map[string]any{"greeting": "Welcome"}
		}
		var rawBody json.RawMessage
		if tc.body != "" {
			rawBody = json.RawMessage(tc.body)
		}
		resp, err := hostedRT.Call(context.Background(), plugins.Request{
			Method:  tc.method,
			Path:    tc.path,
			Headers: tc.headers,
			Body:    rawBody,
		})
		if err != nil {
			t.Fatalf("hosted call %s: %v", tc.path, err)
		}
		var parsed any
		if err := json.Unmarshal(resp.Body, &parsed); err != nil {
			t.Fatalf("unmarshal hosted json (%s): %v", string(resp.Body), err)
		}
		return resp.Status, parsed
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tsStatus, tsBody := callHosted(tc)
			goStatus, goBody := callHTTP(goSrv.URL, tc)
			pyStatus, pyBody := callHTTP(pyURL, tc)

			if tsStatus != tc.wantStatus || goStatus != tc.wantStatus || pyStatus != tc.wantStatus {
				t.Fatalf("status mismatch: ts=%d go=%d py=%d (want %d)", tsStatus, goStatus, pyStatus, tc.wantStatus)
			}
			if tc.name == "catalog search with escaped ampersand" {
				encoded, _ := json.Marshal(tsBody)
				if !strings.Contains(string(encoded), "fish\\u0026chips") {
					t.Fatalf("escaped search was not preserved: %s", encoded)
				}
			}
			if !reflect.DeepEqual(tsBody, goBody) {
				tsJSON, _ := json.Marshal(tsBody)
				goJSON, _ := json.Marshal(goBody)
				t.Fatalf("TS vs Go body mismatch:\nTS: %s\nGo: %s", tsJSON, goJSON)
			}
			if !reflect.DeepEqual(tsBody, pyBody) {
				tsJSON, _ := json.Marshal(tsBody)
				pyJSON, _ := json.Marshal(pyBody)
				t.Fatalf("TS vs Python body mismatch:\nTS: %s\nPy: %s", tsJSON, pyJSON)
			}
		})
	}
}

func TestHostedTypeScriptAsyncHandler(t *testing.T) {
	files := loadTypeScriptHelloFiles(t)
	const entry = "examples/hello/typescript/plugin.ts"
	const before = "catalog({ type, search }, ctx) {"
	const after = "async catalog({ type, search }, ctx) { await Promise.resolve();"
	if !strings.Contains(files[entry], before) {
		t.Fatal("catalog handler not found")
	}
	files[entry] = strings.Replace(files[entry], before, after, 1)
	compiled, _, diags := wasm.Compile(files, entry)
	for _, d := range diags {
		if d.Severity == "error" {
			t.Fatalf("compile: %+v", d)
		}
	}
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer eng.Close()
	p, err := eng.LoadJS("hello.async", "1", string(compiled), wasm.HostFunc(func(_ context.Context, name string, _ json.RawMessage) (json.RawMessage, error) {
		if name == "settings.get" {
			return json.RawMessage(`{}`), nil
		}
		return nil, fmt.Errorf("unexpected host call %s", name)
	}), wasm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	resp, err := p.Call(context.Background(), wasm.Request{Method: "GET", Path: "/catalog/movie/hello.json"})
	if err != nil || resp.Status != http.StatusOK || !strings.Contains(string(resp.Body), "Hello from Ether") {
		t.Fatalf("async hosted response: %d %s %v", resp.Status, resp.Body, err)
	}
}

func (noopHost) FoldersHash(context.Context, string, string) (plugins.FolderHash, error) {
	return plugins.FolderHash{}, nil
}

func (noopHost) FoldersHashStart(context.Context, string, string) (plugins.FolderHashJob, error) {
	return plugins.FolderHashJob{}, nil
}

func (noopHost) FoldersHashPoll(context.Context, string) (plugins.FolderHashJob, error) {
	return plugins.FolderHashJob{}, nil
}

func (noopHost) FoldersHashCancel(context.Context, string) (plugins.FolderHashJob, error) {
	return plugins.FolderHashJob{}, nil
}
