package mediabrowser

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
)

func TestStopEncodingUsesAuthenticatedDeviceScope(t *testing.T) {
	viewer := &frontend.Viewer{ID: 7}
	stopped := 0
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
		Sessions: frontendtest.Sessions{StopFunc: func(_ context.Context, access frontend.SessionAccess, id string) error {
			if access.Viewer != viewer || access.Device.ID != "tv" || access.Protocol != "jellyfin" || access.Owner || id != "session" {
				t.Fatalf("scope %+v id %s", access, id)
			}
			stopped++
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"?deviceId=other&playSessionId=session", 403},
		{"?deviceId=tv", 400},
		{"?deviceId=tv&playSessionId=session", 204},
	} {
		r := httptest.NewRequest("DELETE", "/jellyfin/Videos/ActiveEncodings"+tc.query, nil)
		r.Header.Set("X-Emby-Token", "token")
		r.Header.Set("X-Emby-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body)
		}
	}
	if stopped != 1 {
		t.Fatalf("stopped %d sessions", stopped)
	}
}
