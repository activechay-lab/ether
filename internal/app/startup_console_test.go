package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/startup"
)

// Exercise the disk-settings -> bound front door -> startup gate path without
// opening a database or wiring the live config/admin handlers.
func TestStartupConsoleNetworkPolicy(t *testing.T) {
	for _, setting := range []struct {
		name, doc string
		remote    bool
	}{
		{"enabled", `{"server":{"allowRemoteConsole":true,"trustedProxyCIDRs":["203.0.113.2/32"]}}`, true},
		{"disabled", `{"server":{"allowRemoteConsole":false,"trustedProxyCIDRs":["203.0.113.2/32"]}}`, false},
		{"omitted", `{"server":{"trustedProxyCIDRs":["203.0.113.2/32"]}}`, false},
		{"invalid", `{"server":{"allowRemoteConsole":true},`, false},
	} {
		t.Run(setting.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(setting.doc), 0o600); err != nil {
				t.Fatal(err)
			}
			facts := readNodeFacts(dir, nil)
			front := newFrontDoor(startup.NewTracker("0.3.5", nil), quietLog())
			// Keep this policy test independent of a generated web build.
			front.gate.Shell = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("startup shell"))
			})
			// Bind an ephemeral loopback port; requests below supply the peer
			// addresses a real listener would receive.
			server := facts.Server
			server.Port, server.BindAddress = 0, "127.0.0.1"
			if err := front.bind(server, false); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(front.close)
			front.tracker.Set(startup.PhaseMigrating)
			for _, client := range []struct {
				name, peer, forwarded string
				local                 bool
			}{
				{"LAN", "192.168.1.20:1234", "", true},
				{"outside", "203.0.113.9:1234", "", false},
				{"loopback proxy outside", "127.0.0.1:1234", "203.0.113.9", false},
				{"trusted proxy LAN", "203.0.113.2:1234", "192.168.1.20", facts.Readable},
				{"trusted proxy outside", "203.0.113.2:1234", "203.0.113.9", false},
				{"untrusted spoofed LAN", "203.0.113.3:1234", "192.168.1.20", false},
			} {
				t.Run(client.name, func(t *testing.T) {
					for _, route := range []struct {
						method, path string
						code         int
					}{
						{"GET", "/ether/", http.StatusOK},
						{"GET", startup.StatusPath, http.StatusOK},
						{"POST", startup.StatusPath, http.StatusMethodNotAllowed},
						{"GET", "/ether/api/config", http.StatusServiceUnavailable},
						{"POST", "/ether/api/setup", http.StatusServiceUnavailable},
						{"POST", startup.RecoveryPrefix + "restore", http.StatusServiceUnavailable},
					} {
						r := httptest.NewRequest(route.method, "http://ether.example"+route.path, nil)
						r.RemoteAddr = client.peer
						if client.forwarded != "" {
							r.Header.Set("X-Forwarded-For", client.forwarded)
						}
						w := httptest.NewRecorder()
						front.gate.ServeHTTP(w, r)
						want := route.code
						if !setting.remote && !client.local {
							want = http.StatusForbidden
						}
						if w.Code != want {
							t.Fatalf("%s %s: %d, want %d: %s", route.method, route.path, w.Code, want, w.Body)
						}
						if want == http.StatusForbidden && !strings.Contains(w.Body.String(), "only available on your home network") {
							t.Fatalf("missing LAN-only message: %s", w.Body)
						}
						if route.path == "/ether/" && want == http.StatusOK && w.Body.String() != "startup shell" {
							t.Fatalf("missing startup shell: %s", w.Body)
						}
						if route.path == startup.StatusPath && want == http.StatusOK {
							var status startup.Status
							if err := json.Unmarshal(w.Body.Bytes(), &status); err != nil || status.Phase != startup.PhaseMigrating || status.Ready {
								t.Fatalf("migration status: %+v, %v", status, err)
							}
						}
					}
				})
			}
		})
	}
}
