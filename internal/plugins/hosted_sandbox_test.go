package plugins

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"

	"github.com/braedonsaunders/ether/internal/plugins/wasm"
)

func TestHostedFactorySandbox(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("ETHER_SANDBOX_CANARY", "secret-env")
	for _, name := range []string{"config.json", "ether.db"} {
		if err := os.WriteFile(name, []byte("secret-file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := `globalThis.handle = async () => { const result = {}; for (const name of ['qjs:std','qjs:os','std','os','./config.json','/ether.db']) { try { await import(name); result[name] = true; } catch (_) { result[name] = false; } } result.globalStd = typeof globalThis.std; result.globalOs = typeof globalThis.os; return {status:200, body:result}; };`
	eng := wasm.NewEngine(slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer eng.Close()
	rt, err := HostedRuntimeFactory(eng)(Installed{ID: "test.sandbox", Manifest: Manifest{Version: "1", Ether: &EtherBlock{Runtime: RuntimeDecl{Kind: "hosted", Entry: "plugin.js"}}}, Files: func(string) ([]byte, bool) { return []byte(source), true }, Host: &memHost{kv: map[string][]byte{}}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	resp, err := rt.Call(context.Background(), Request{})
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"qjs:std", "qjs:os", "std", "os", "./config.json", "/ether.db"} {
		if result[name] != false {
			t.Errorf("import %s: %v", name, result[name])
		}
	}
	if result["globalStd"] != "undefined" || result["globalOs"] != "undefined" {
		t.Errorf("globals: %v", result)
	}
	for _, name := range []string{"config.json", "ether.db"} {
		b, err := os.ReadFile(name)
		if err != nil || string(b) != "secret-file" {
			t.Errorf("canary %s changed: %q %v", name, b, err)
		}
	}
}
