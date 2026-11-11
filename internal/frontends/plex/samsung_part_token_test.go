package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func samsungIdentity(t *testing.T) func(*testServerDeps) {
	a := samsungAuth(t)
	return func(d *testServerDeps) { d.Host.Identity = frontend.BindAuth(a) }
}

const samsungFriendToken = "friend-access"

// samsungDelegation fetches a transient delegation token the way Plex TV apps
// do before putting it on media URLs.
func samsungDelegation(t *testing.T, f *fixture) string {
	t.Helper()
	rec := f.request(t, http.MethodGet, "/security/token", map[string]string{
		"X-Plex-Token": samsungFriendToken,
		"Accept":       "application/json",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("security/token: %d %s", rec.Code, rec.Body.String())
	}
	tok := containerAttrs(t, rec.Body.Bytes())["token"]
	if tok == "" {
		t.Fatalf("no delegation token: %s", rec.Body.String())
	}
	return tok
}

// samsungPartPath is the part URL Ether renders for the movie.
func samsungPartPath() string { return "/library/parts/1000000000000550/1759276800/file.mkv" }

// TestSamsungDelegationTokenPartFetch reproduces the Tizen 10 AVPlay flow: a
// shared remote viewer obtains a delegation token from /security/token, makes
// the decision with its Plex client headers (opening the session), then the
// player fetches the part URL with ONLY ?X-Plex-Token and no client headers at
// all (AVPlay drops them). PMS serves that read for a viewer allowed to play
// the part; Ether must too, by attaching the read to the live session and
// never opening another one. This is the v0.3.9 #424 client-compatibility
// invariant.
func TestSamsungDelegationTokenPartFetch(t *testing.T) {
	f, m := issue94Fixture(t, true, "open", samsungIdentity(t))
	defer f.srv.Close()
	partURL := samsungPartPath()
	delegation := samsungDelegation(t, f)

	// The decision carries the TV's client headers and opens the session.
	q := issue94Query("samsung-sess", 2000)
	dec := f.request(t, http.MethodGet, "/video/:/transcode/universal/decision?"+q, map[string]string{
		"X-Plex-Token":              samsungFriendToken,
		"X-Plex-Client-Identifier":  "samsung-tv",
		"X-Plex-Product":            "Plex for Samsung",
		"X-Plex-Session-Identifier": "samsung-sess",
		"Accept":                    "application/json",
	})
	if dec.Code != http.StatusOK {
		t.Fatalf("decision: %d %s", dec.Code, dec.Body.String())
	}
	before := len(m.Sessions())
	if before == 0 {
		t.Fatal("decision opened no playback session")
	}

	// The player then fetches the part with ONLY the token on the URL and no
	// client headers. GET and HEAD must both serve from the live session, for
	// the transient delegation token and for the account token alike, without
	// opening another session (so no stream-limit bypass).
	for _, token := range []string{delegation, samsungFriendToken} {
		for _, method := range []string{http.MethodGet, http.MethodHead} {
			req := httptest.NewRequest(method, partURL+"?X-Plex-Token="+token, nil)
			req.RemoteAddr = "203.0.113.9:52000" // remote, not the LAN fixture peer
			rec := httptest.NewRecorder()
			f.srv.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
				t.Fatalf("%s part fetch with %q: %d %s", method, token, rec.Code, rec.Body.String())
			}
		}
		if got := len(m.Sessions()); got != before {
			t.Fatalf("headerless read opened a session: %d -> %d", before, got)
		}
	}

	// Protection is preserved: the same bare read without any token is refused.
	noToken := httptest.NewRequest(http.MethodGet, partURL, nil)
	noToken.RemoteAddr = "203.0.113.9:52000"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, noToken)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("tokenless part fetch: %d", rec.Code)
	}
}

// TestSamsungHeaderlessPartWithoutSessionRefused asserts a clientless part
// read cannot mint a new stream outside the user's limits: with no live
// session to continue, it is refused.
func TestSamsungHeaderlessPartWithoutSessionRefused(t *testing.T) {
	f, m := issue94Fixture(t, true, "open", samsungIdentity(t))
	defer f.srv.Close()
	delegation := samsungDelegation(t, f)

	req := httptest.NewRequest(http.MethodGet, samsungPartPath()+"?X-Plex-Token="+delegation, nil)
	req.RemoteAddr = "203.0.113.9:52000"
	rec := httptest.NewRecorder()
	f.srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("headerless part with no session: %d %s", rec.Code, rec.Body.String())
	}
	if got := len(m.Sessions()); got != 0 {
		t.Fatalf("refused read opened %d sessions", got)
	}
}
