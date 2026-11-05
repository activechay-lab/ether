package wasm

import (
	"context"
	"encoding/json"
	"io"

	"github.com/fastschema/qjs"
	"os"
	"path/filepath"
	"testing"
)

// Exercise the built-in modules directly: the security boundary must hold
// even when guest code bypasses the TypeScript bundler.
const sandboxProbe = `
globalThis.handle = async function() {
 const result = {};
 try {
   const std = await import('qjs:std');
   result.config = std.loadFile('./config.json');
   result.db = std.loadFile('/ether.db');
   const f = std.open('/escape.txt', 'w');
   result.write = !!f; if (f) { f.puts('escape'); f.close(); }
   result.env = std.getenv('ETHER_SANDBOX_CANARY');
 } catch (e) { result.stdError = String(e); }
 try { const os = await import('qjs:os'); result.list = os.readdir('/'); } catch (e) { result.osError = String(e); }
 try { await import('std'); result.bareStd = true; } catch (_) { result.bareStd = false; }
 try { await import('os'); result.bareOs = true; } catch (_) { result.bareOs = false; }
 result.globalStd = typeof globalThis.std; result.globalOs = typeof globalThis.os;
 return {status: 200, body: result};
};`

func TestJSSandboxNoHostFilesystem(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("ETHER_SANDBOX_CANARY", "secret-env")
	for _, name := range []string{"config.json", "ether.db"} {
		if err := os.WriteFile(name, []byte("secret-file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	e := testEngine(t)
	p, err := e.LoadJS("sandbox", "1", sandboxProbe, nil, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	resp, err := p.Call(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("probe: %s", resp.Body)
	for _, key := range []string{"config", "db", "env"} {
		if result[key] != nil {
			t.Errorf("%s leaked: %v", key, result[key])
		}
	}
	if result["globalStd"] != "undefined" || result["globalOs"] != "undefined" {
		t.Errorf("built-in globals exposed: %v", result)
	}
	if result["stdError"] == nil || result["osError"] == nil {
		t.Errorf("built-in modules loaded: %v", result)
	}
	if result["write"] == true || result["bareStd"] == true || result["bareOs"] == true {
		t.Errorf("unexpected capabilities: %v", result)
	}
	for _, name := range []string{"config.json", "ether.db"} {
		b, err := os.ReadFile(name)
		if err != nil || string(b) != "secret-file" {
			t.Errorf("canary %s changed: %q %v", name, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "escape.txt")); !os.IsNotExist(err) {
		t.Errorf("write escaped: %v", err)
	}
}

// Run upstream QuickJS with its std/os modules intact to independently test
// the WASI boundary, even if the module-registration defense regresses.
func TestQJSNoPreopenedFilesystem(t *testing.T) {
	original, err := os.ReadFile("testdata/qjs-upstream-v0.0.6.wasm")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("ETHER_SANDBOX_CANARY", "secret-env")
	for _, name := range []string{"config.json", "ether.db"} {
		if err := os.WriteFile(name, []byte("secret-file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rt, err := qjs.New(qjs.Option{Context: context.Background(), QuickJSWasmBytes: original, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	v, err := rt.Context().Eval("probe.js", qjs.Code(`JSON.stringify({config:std.loadFile('/config.json'),db:std.loadFile('./ether.db'),list:os.readdir('/'),write:(()=>{const f=std.open('/escape.txt','w'); if(f) f.close(); return !!f})(),env:std.getenv('ETHER_SANDBOX_CANARY')})`))
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Config *string           `json:"config"`
		DB     *string           `json:"db"`
		List   []json.RawMessage `json:"list"`
		Write  bool              `json:"write"`
		Env    *string           `json:"env"`
	}
	if err := json.Unmarshal([]byte(v.String()), &result); err != nil {
		t.Fatal(err)
	}
	if result.Config != nil || result.DB != nil || result.Env != nil || result.Write || len(result.List) != 2 || string(result.List[0]) != "[]" || string(result.List[1]) == "0" {
		t.Fatalf("WASI access: %s", v.String())
	}
	for _, name := range []string{"config.json", "ether.db"} {
		b, err := os.ReadFile(name)
		if err != nil || string(b) != "secret-file" {
			t.Fatalf("canary %s changed: %q %v", name, b, err)
		}
	}
	if _, err := os.Stat("escape.txt"); !os.IsNotExist(err) {
		t.Fatalf("write escaped: %v", err)
	}
}
