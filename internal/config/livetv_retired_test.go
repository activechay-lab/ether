package config

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetiredLiveTVSettingsResaved(t *testing.T) {
	dir := t.TempDir()
	raw := []byte(`{"liveTV":{"defaultStyleId":"decades","screens":{"offAir":"bars","noSignal":"static","stationId":true,"applyTo":"all"},"logoIndexUrl":"https://example.com/logos"}}`)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"defaultStyleId", `"screens"`} {
		if strings.Contains(string(saved), key) {
			t.Fatalf("retired key persisted: %s", key)
		}
	}
	if store.Get().LiveTV.LogoIndexURL != "https://example.com/logos" {
		t.Fatal("unrelated option lost")
	}
	if _, _, err := decodeOverDefaults(Defaults(dir), raw); err != nil {
		t.Fatal(err)
	}
	shared, err := MergeShared(Defaults(dir), raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(SharedJSON(shared)), "defaultStyleId") || strings.Contains(string(SharedJSON(shared)), `"screens"`) {
		t.Fatal("retired shared settings persisted")
	}
	backend := &memShared{found: true, doc: SharedDoc{Doc: string(raw), Version: 1, KeyFingerprint: store.KeyFingerprint()}}
	if err := store.AttachShared(context.Background(), backend, SharedOptions{PollInterval: -1}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if strings.Contains(backend.doc.Doc, "defaultStyleId") || strings.Contains(backend.doc.Doc, `"screens"`) {
		t.Fatal("stored cluster document retained retired keys")
	}

}
