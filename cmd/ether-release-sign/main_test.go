package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestKeygenNeverOverwritesAKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.txt")
	if err := os.WriteFile(path, []byte("ETHER_UPDATE_PRIVATE_KEY=existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := cmdKeygen([]string{"-out", path}); err == nil {
		t.Fatal("keygen replaced an existing key file")
	}
	if b, _ := os.ReadFile(path); string(b) != "ETHER_UPDATE_PRIVATE_KEY=existing\n" {
		t.Fatalf("existing key file changed: %q", b)
	}
}

func TestKeygenWritesOwnerOnlyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key.txt")
	if err := cmdKeygen([]string{"-out", path}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(b), "ETHER_UPDATE_PRIVATE_KEY=") {
		t.Fatalf("key file %q, %v", b, err)
	}
	if _, err := loadPrivateKey(path); err != nil {
		t.Fatalf("generated key file does not load: %v", err)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v, want 0600", st.Mode().Perm())
		}
	}
}

// The manifest carries the staged rollout: a day for stable, none for beta,
// none for a critical fix.
func TestManifestRolloutDefaults(t *testing.T) {
	// The -artifact spec is colon-separated, so a Windows path (C:\...)
	// cannot be one of its fields: work from a relative name.
	dir := t.TempDir()
	t.Chdir(dir)
	art := "ether-linux-amd64"
	if err := os.WriteFile(art, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args     []string
		hours    int
		critical bool
	}{
		{[]string{"-channel", "stable"}, 24, false},
		{[]string{"-channel", "beta"}, 0, false},
		{[]string{"-channel", "stable", "-rollout-hours", "6"}, 6, false},
		{[]string{"-channel", "stable", "-critical"}, 0, true},
	} {
		out := "m.json"
		args := append([]string{"-version", "1.2.0", "-artifact", art + ":linux:amd64:binary:https://example.invalid/ether", "-out", out}, c.args...)
		if err := cmdManifest(args); err != nil {
			t.Fatal(err)
		}
		m, _, err := readManifest(out)
		if err != nil || m.RolloutHours != c.hours || m.Critical != c.critical {
			t.Fatalf("%v: rollout=%d critical=%v err=%v", c.args, m.RolloutHours, m.Critical, err)
		}
	}
}
