package etherplugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPairingExchangeSavesCredential0600(t *testing.T) {
	dir := t.TempDir()
	var gotCode string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		var in struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		gotCode = in.Code
		_ = json.NewEncoder(w).Encode(map[string]string{"credential": "cred-1", "pluginId": "p"})
	}))
	defer srv.Close()

	inputs := PairingInputs{URL: srv.URL, Code: "CODE-123", StateDir: dir}
	cred, fromFile, err := resolveCredential(context.Background(), inputs, "community.example.p")
	if err != nil {
		t.Fatal(err)
	}
	if cred != "cred-1" || !fromFile {
		t.Fatalf("cred=%q fromFile=%v", cred, fromFile)
	}
	if gotCode != "CODE-123" {
		t.Fatalf("server saw code %q", gotCode)
	}
	if gotPath != PairExchangePath {
		t.Fatalf("server path %q", gotPath)
	}
	path := filepath.Join(dir, "credential")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Windows has no Unix permission bits; the user profile's ACLs protect
	// the file there.
	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("credential mode %o, want 600", info.Mode().Perm())
		}
		if info, err := os.Stat(dir); err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("state dir mode %v %v, want 700", info, err)
		}
	}
}

func TestPairingRestartUsesSavedFileWithoutCode(t *testing.T) {
	dir := t.TempDir()
	exchanges := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		exchanges++
		_ = json.NewEncoder(w).Encode(map[string]string{"credential": "cred-1", "pluginId": "p"})
	}))
	defer srv.Close()
	inputs := PairingInputs{URL: srv.URL, Code: "CODE-123", StateDir: dir}
	if _, _, err := resolveCredential(context.Background(), inputs, "p"); err != nil {
		t.Fatal(err)
	}
	// A restart with no code or URL finds the saved file and makes no exchange.
	restart := PairingInputs{StateDir: dir}
	cred, fromFile, err := resolveCredential(context.Background(), restart, "p")
	if err != nil {
		t.Fatal(err)
	}
	if cred != "cred-1" || !fromFile {
		t.Fatalf("cred=%q fromFile=%v", cred, fromFile)
	}
	if exchanges != 1 {
		t.Fatalf("exchanges=%d, want 1", exchanges)
	}
}

func TestPairingExplicitCredentialWins(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called when a credential is explicit")
	}))
	defer srv.Close()
	// A saved file exists, but the explicit credential must still win.
	if err := writeSavedCredential(filepath.Join(dir, "credential"), dir, savedCredential{Credential: "saved", URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	inputs := PairingInputs{URL: srv.URL, Code: "CODE-123", Credential: "explicit", StateDir: dir}
	cred, fromFile, err := resolveCredential(context.Background(), inputs, "p")
	if err != nil {
		t.Fatal(err)
	}
	if cred != "explicit" || fromFile {
		t.Fatalf("cred=%q fromFile=%v", cred, fromFile)
	}
}

func TestPairingUnauthorizedSavedCredentialClearsFile(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	if err := writeSavedCredential(filepath.Join(dir, "credential"), dir, savedCredential{Credential: "stale", URL: srv.URL}); err != nil {
		t.Fatal(err)
	}
	p := New(Definition{Manifest: Manifest{ID: "p"}})
	_, err := p.Start(context.Background(), ServeOptions{
		Mode: "tunnel", TunnelURL: srv.URL, StateDir: dir,
	})
	if err == nil {
		t.Fatal("expected error for rejected credential")
	}
	// The credential came from the file, so the stale file is gone.
	if _, err := os.Stat(filepath.Join(dir, "credential")); !os.IsNotExist(err) {
		t.Fatalf("credential file should be removed, stat err=%v", err)
	}
}

func TestPairingNoCredentialGivesClearLine(t *testing.T) {
	dir := t.TempDir()
	_, _, err := resolveCredential(context.Background(), PairingInputs{StateDir: dir}, "p")
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := err.Error(); got != noCredentialError().Error() {
		t.Fatalf("error %q", got)
	}
}

func TestTunnelURLDerivation(t *testing.T) {
	for _, tc := range []struct{ base, want string }{
		{"http://host:32450", "ws://host:32450/ether/plugins/tunnel"},
		{"https://host/", "wss://host/ether/plugins/tunnel"},
		{"https://host/sub", "wss://host/ether/plugins/tunnel"},
	} {
		got, err := TunnelURL(tc.base)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("TunnelURL(%q)=%q, want %q", tc.base, got, tc.want)
		}
	}
	if _, err := TunnelURL("ftp://host"); err == nil {
		t.Fatal("expected error for non-http scheme")
	}
}
