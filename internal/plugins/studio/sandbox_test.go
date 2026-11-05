package studio

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPreviewHasNoHostFilesystem(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("ETHER_SANDBOX_CANARY", "secret-env")
	for _, name := range []string{"config.json", "ether.db"} {
		if err := os.WriteFile(name, []byte("secret-file"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	st, _, _ := newTestStudio(t)
	ctx := context.Background()
	p, err := st.CreateProject(ctx, CreateProjectOptions{Template: "list", Name: "Sandbox test"})
	if err != nil {
		t.Fatal(err)
	}
	source := `export async function handle() {
 const result: any = {}; const name = 'qjs:' + 'std'; const osname = 'qjs:' + 'os';
 try { const std = await import(name); result.config = std.loadFile('./config.json'); result.db = std.loadFile('/ether.db'); const f = std.open('/escape.txt', 'w'); result.write = !!f; if (f) { f.puts('escape'); f.close(); } result.env = std.getenv('ETHER_SANDBOX_CANARY'); } catch (e) { result.error = String(e); }
 try { const os = await import(osname); result.list = os.readdir('/'); } catch (e) { result.osError = String(e); }
 return {status:200, body: result};
 }`
	_, compiled, err := st.SaveFiles(ctx, p.ID, map[string]string{"src/plugin.ts": source}, nil, nil)
	if err != nil || !compiled.OK {
		t.Fatalf("save: %v %+v", err, compiled)
	}
	result, err := st.RunProject(ctx, p.ID, RunRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(result.Body, &body); err != nil {
		t.Fatal(err)
	}
	t.Logf("probe: %s", result.Body)
	if body["error"] == nil || body["osError"] == nil {
		t.Errorf("built-in modules loaded: %v", body)
	}
	for _, key := range []string{"config", "db", "env"} {
		if body[key] != nil {
			t.Errorf("%s leaked: %v", key, body[key])
		}
	}
	if body["write"] == true {
		t.Errorf("write permitted: %v", body)
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
