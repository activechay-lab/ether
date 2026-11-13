package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFolderHashCacheAndCancellation(t *testing.T) {
	env := newTestEnv(t)
	ctx := context.Background()
	root := t.TempDir()
	man := baseManifest()
	man.Ether.Permissions.Folders = "read"
	raw, _ := json.Marshal(man)
	preview, err := env.m.PreviewInstall(ctx, Source{Kind: "studio", Manifest: raw, Files: map[string][]byte{"dist/plugin.js": []byte("// x")}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := env.m.Install(ctx, preview, InstallOptions{Grants: Grants{Folders: []string{root}}})
	if err != nil {
		t.Fatal(err)
	}
	h := env.m.HostFor(rec.ID)
	if err := os.WriteFile(filepath.Join(root, "small"), []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := h.FoldersHash(ctx, root, "small")
	if err != nil {
		t.Fatal(err)
	}
	if first.MD5 != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatal(first)
	}
	// A cache hit must not try to store another digest, even across host instances.
	if _, err := env.m.store.db.Exec(`CREATE TRIGGER reject_hash_write BEFORE INSERT ON plugins_hash_cache BEGIN SELECT RAISE(FAIL, 'unexpected rehash'); END`); err != nil {
		t.Fatal(err)
	}
	cached, err := env.m.HostFor(rec.ID).FoldersHash(ctx, root, "small")
	if err != nil || cached != first {
		t.Fatalf("cache: %+v %v", cached, err)
	}
	if _, err := env.m.store.db.Exec(`DROP TRIGGER reject_hash_write`); err != nil {
		t.Fatal(err)
	}
	// Changed metadata invalidates the cached result.
	if err := os.WriteFile(filepath.Join(root, "small"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := h.FoldersHash(ctx, root, "small")
	if err != nil || changed.MD5 == first.MD5 {
		t.Fatalf("invalidation: %+v %v", changed, err)
	}
	f, err := os.Create(filepath.Join(root, "large"))
	if err != nil {
		t.Fatal(err)
	}
	err = f.Truncate(257 << 20)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	job, err := h.FoldersHashStart(ctx, root, "large")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := h.FoldersHashStart(ctx, root, "large")
	if err != nil || duplicate.ID != job.ID {
		t.Fatalf("dedup: %+v %v", duplicate, err)
	}
	if _, err := h.FoldersHashCancel(ctx, job.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		status, err := h.FoldersHashPoll(ctx, job.ID)
		if err != nil {
			t.Fatal(err)
		}
		if status.State != "running" {
			if status.State != "canceled" || status.Result != nil {
				t.Fatalf("cancel: %+v", status)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancel did not stop hash")
		}
		time.Sleep(time.Millisecond)
	}
}
