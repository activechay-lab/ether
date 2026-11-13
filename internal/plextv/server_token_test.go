package plextv

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Legacy account credentials must be exchanged under the machine identity,
// persisted, and used to import client tokens on the very same sync attempt.
func TestRepairLegacyServerToken(t *testing.T) {
	f := newFakePlexTV(t, testMachineID)
	f.addUser("account-token", owner())
	f.accessXML = `<tokens><token token="client-device-token" userID="42" /></tokens>`
	rejected, accepted := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v2/server/access_tokens" {
			if req.URL.Query().Get("auth_token") == "account-token" {
				rejected++
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprint(w, `{"errors":[{"code":1080,"message":"A valid server token is required to perform this action"}]}`)
				return
			}
			if req.URL.Query().Get("auth_token") != "server-token-42" {
				t.Error("unexpected credential")
			}
			accepted++
		}
		f.handle(w, req)
	}))
	defer srv.Close()
	cfg := testConfig()
	cfg.Plex.Token, cfg.Plex.OwnerPlexID, cfg.Plex.Claimed = "account-token", 42, true
	store := &memConfig{cfg: cfg}
	users := &fakeUsers{}
	client, err := New(Options{BaseURL: srv.URL, Identity: ServerIdentity(cfg, ""), RetryBackoff: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistrar(RegistrarOptions{Config: store, Client: client, Users: users})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SyncFriendsNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	if users.token != "server-token-42" || len(users.friends) != 1 || users.friends[0].Token != "client-device-token" {
		t.Fatal("repaired owner and client tokens were not installed")
	}
	if rejected != 1 || accepted != 1 {
		t.Fatalf("requests rejected=%d accepted=%d", rejected, accepted)
	}
	if got := store.Get().Plex.Token; got != "server-token-42" {
		t.Fatalf("persisted token was not repaired")
	}
	if st := r.Status(); st.TokenRejected || st.ServerTokenRequired || st.FriendSyncError != "" {
		t.Fatalf("status: %+v", st)
	}
	if f.count("GET /api/servers/"+testMachineID+"/shared_servers") != 0 {
		t.Fatal("1080 must not fall back to shared_servers")
	}
}
