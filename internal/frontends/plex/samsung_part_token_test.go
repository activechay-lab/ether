package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/store"
)

// samsungAuth builds a real auth.Service with the owner and one remote shared
// friend whose plex.tv share is restricted to library 1 (Movies).
func samsungAuth(t *testing.T) *auth.Service {
	t.Helper()
	db, err := store.OpenPath(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s, err := auth.New(context.Background(), db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.BootstrapOwner(ctx, config.Plex{OwnerPlexID: 42, OwnerUsername: "owner", Token: "server-token"}); err != nil {
		t.Fatal(err)
	}
	if err := s.SyncFriends(ctx, []plextv.Friend{
		{Owned: true, Token: "owner-access", PlexID: 42},
		{PlexID: 100, Username: "friend", Title: "Friend", Token: "friend-access", Sections: []string{"1"}},
	}); err != nil {
		t.Fatal(err)
	}
	return s
}

func samsungFixture(t *testing.T) *fixture {
	t.Helper()
	a := samsungAuth(t)
	return newFixture(func(d *testServerDeps) {
		d.Host.Identity = frontend.BindAuth(a)
	})
}

// samsungPartID resolves the part id Ether renders for key by reading the
// title's metadata as the given credential.
func samsungPartID(t *testing.T, f *fixture, token, key string) string {
	t.Helper()
	rec := f.request(t, http.MethodGet, "/library/metadata/"+key, map[string]string{
		"X-Plex-Token": token,
		"Accept":       "application/json",
	})
	body := rec.Body.String()
	i := strings.Index(body, "/library/parts/")
	if i < 0 {
		t.Fatalf("no part in metadata: %s", body)
	}
	rest := body[i+len("/library/parts/"):]
	id, _, _ := strings.Cut(rest, "/")
	return id
}

// TestSamsungDelegationTokenPartFetch reproduces the Tizen 10 AVPlay flow: a
// shared remote viewer obtains a delegation token from /security/token, then
// the player fetches the part URL with ONLY ?X-Plex-Token=<delegation> and no
// Plex client headers at all (AVPlay drops them). PMS serves that read for a
// viewer allowed to play the part; Ether must too. This is the v0.3.9 #424
// client-compatibility invariant.
func TestSamsungDelegationTokenPartFetch(t *testing.T) {
	f := samsungFixture(t)
	defer f.srv.Close()

	const key = "1000000000000550"
	partURL := "/library/parts/" + samsungPartID(t, f, "owner-access", key) + "/1759276800/file.mkv"

	// The TV asks for a delegation token while holding its account token.
	tokRec := f.request(t, http.MethodGet, "/security/token", map[string]string{
		"X-Plex-Token": "friend-access",
		"Accept":       "application/json",
	})
	if tokRec.Code != http.StatusOK {
		t.Fatalf("security/token: %d %s", tokRec.Code, tokRec.Body.String())
	}
	delegation := containerAttrs(t, tokRec.Body.Bytes())["token"]
	if delegation == "" {
		t.Fatalf("no delegation token: %s", tokRec.Body.String())
	}

	// The player then fetches the part with ONLY the token on the URL and no
	// client headers. GET and HEAD must both serve, for the transient
	// delegation token and for the account token alike.
	for _, token := range []string{delegation, "friend-access"} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			req := httptest.NewRequest(method, partURL+"?X-Plex-Token="+token, nil)
			req.RemoteAddr = "203.0.113.9:52000" // remote, not the LAN fixture peer
			rec := httptest.NewRecorder()
			f.srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
				t.Fatalf("%s part fetch with %q: %d %s", method, token, rec.Code, rec.Body.String())
			}
		}
	}

	// Protection is preserved: the same bare read without any token is still
	// refused.
	noToken := httptest.NewRequest(http.MethodGet, partURL, nil)
	noToken.RemoteAddr = "203.0.113.9:52000"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, noToken)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tokenless part fetch: %d", rec.Code)
	}
}
