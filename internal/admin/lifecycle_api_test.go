package admin_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/store"
)

func lifecycleDirectory(users *auth.Service) func(context.Context) ([]admin.ViewerRecord, error) {
	return func(ctx context.Context) ([]admin.ViewerRecord, error) {
		stored, err := users.AllUsers(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]admin.ViewerRecord, 0, len(stored))
		for _, u := range stored {
			out = append(out, admin.ViewerRecord{User: u.User, Active: u.Active, Origin: u.Origin, Home: u.Home, OwnerEnabled: u.OwnerEnabled, PlexShared: u.PlexShared, HasPlex: u.HasPlex})
		}
		return out, nil
	}
}

type lifecycleJSON struct {
	Source     string `json:"source"`
	Paused     bool   `json:"paused"`
	PlexShared bool   `json:"plexShared"`
	CanRemove  bool   `json:"canRemove"`
	CanMerge   bool   `json:"canMerge"`
	CanPause   bool   `json:"canPause"`
	ManagedBy  string `json:"managedBy"`
}

type userJSON struct {
	ID        int64         `json:"id"`
	Username  string        `json:"username"`
	Lifecycle lifecycleJSON `json:"lifecycle"`
}

func TestViewerLifecycleAPI(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	owner, err := local.CreateOwner(ctx, "owner", "secret-pass1")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := local.CreateViewer(ctx, "bob", "secret-pass1")
	if err != nil {
		t.Fatal(err)
	}
	amy, err := local.CreateViewer(ctx, "amy", "secret-pass1")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.SyncFriends(ctx, []plextv.Friend{
		{PlexID: 42, Username: "friend", Token: "tok-friend"},
		{PlexID: 43, Username: "homekid", Token: "tok-home", Home: true},
	}); err != nil {
		t.Fatal(err)
	}
	var plex, plexHome int64
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE plex_id=42`).Scan(&plex); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT id FROM auth_users WHERE plex_id=43`).Scan(&plexHome); err != nil {
		t.Fatal(err)
	}
	if err := users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	limiter, _ := identity.NewLoginLimiter(db, time.Minute, 100, 100, nil)
	ownerToken, err := grants.Issue(ctx, owner, nil, "owner", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := newStore(t, func(c *config.Config) { c.SetupComplete = true })
	h := admin.NewHandler(admin.Deps{
		Config: cfg, LocalIdentity: local, IdentityGrants: grants, IdentityLimiter: limiter,
		RefreshViewers: users.RefreshViewers, Lifecycle: users, Users: users,
		ViewerDirectory: lifecycleDirectory(users), Personalization: &fakeEngine{},
	})
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := req(method, path, body)
		r.Header.Set("Authorization", "Bearer "+ownerToken)
		return serve(h, r)
	}

	// Lifecycle fields on the user list and detail.
	res := do("GET", "/ether/api/users", "")
	if res.Code != 200 {
		t.Fatalf("list users: %d %s", res.Code, res.Body)
	}
	var list struct {
		Users []userJSON `json:"users"`
	}
	decode(t, res, &list)
	byID := map[int64]userJSON{}
	for _, u := range list.Users {
		byID[u.ID] = u
	}
	if lc := byID[owner].Lifecycle; lc.Source != "owner" || lc.CanRemove || lc.CanMerge || lc.CanPause || lc.ManagedBy != "" {
		t.Fatalf("owner lifecycle %+v", lc)
	}
	if lc := byID[bob].Lifecycle; lc.Source != "local" || !lc.CanRemove || !lc.CanMerge || !lc.CanPause || lc.Paused || lc.ManagedBy != "" {
		t.Fatalf("local lifecycle %+v", lc)
	}
	if lc := byID[plex].Lifecycle; lc.Source != "plex" || !lc.PlexShared || lc.CanRemove || lc.CanMerge || lc.CanPause || lc.ManagedBy != "plex" {
		t.Fatalf("plex lifecycle %+v", lc)
	}
	if lc := byID[plexHome].Lifecycle; lc.Source != "plexHome" || lc.ManagedBy != "plex" || lc.CanRemove {
		t.Fatalf("plexHome lifecycle %+v", lc)
	}
	res = do("GET", fmt.Sprintf("/ether/api/users/%d", bob), "")
	if res.Code != 200 {
		t.Fatalf("get user: %d %s", res.Code, res.Body)
	}
	var one struct {
		User userJSON `json:"user"`
	}
	decode(t, res, &one)
	if one.User.ID != bob || one.User.Lifecycle.Source != "local" {
		t.Fatalf("get user %+v", one.User)
	}
	if res = do("GET", "/ether/api/users/99999", ""); res.Code != 404 {
		t.Fatalf("get missing user: %d", res.Code)
	}

	// The same lifecycle rides on viewer rows.
	res = do("GET", "/ether/api/viewers", "")
	if res.Code != 200 {
		t.Fatalf("list viewers: %d %s", res.Code, res.Body)
	}
	var viewers struct {
		Viewers []userJSON `json:"viewers"`
	}
	decode(t, res, &viewers)
	foundPlex := false
	for _, v := range viewers.Viewers {
		if v.ID == plex {
			foundPlex = true
			if v.Lifecycle.ManagedBy != "plex" || v.Lifecycle.CanPause {
				t.Fatalf("plex viewer lifecycle %+v", v.Lifecycle)
			}
		}
	}
	if !foundPlex {
		t.Fatal("plex viewer missing from viewers list")
	}

	// Plex viewers and the owner refuse pause/resume/remove/merge.
	for _, tc := range []struct {
		method, path string
		body         string
		want         int
	}{
		{"POST", fmt.Sprintf("/ether/api/users/%d/pause", plex), `{}`, 409},
		{"POST", fmt.Sprintf("/ether/api/users/%d/resume", plex), `{}`, 409},
		{"POST", fmt.Sprintf("/ether/api/users/%d/pause", plexHome), `{}`, 409},
		{"DELETE", fmt.Sprintf("/ether/api/users/%d", plex), ``, 409},
		{"DELETE", fmt.Sprintf("/ether/api/users/%d", plexHome), ``, 409},
		{"DELETE", fmt.Sprintf("/ether/api/users/%d", owner), ``, 403},
		{"POST", fmt.Sprintf("/ether/api/users/%d/pause", owner), `{}`, 403},
		{"POST", fmt.Sprintf("/ether/api/users/%d/merge", bob), fmt.Sprintf(`{"into":%d}`, plex), 409},
		{"POST", fmt.Sprintf("/ether/api/users/%d/merge", plex), fmt.Sprintf(`{"into":%d}`, bob), 409},
		{"POST", fmt.Sprintf("/ether/api/users/%d/merge", bob), fmt.Sprintf(`{"into":%d}`, owner), 403},
		{"POST", fmt.Sprintf("/ether/api/users/%d/merge", bob), fmt.Sprintf(`{"into":%d}`, bob), 400},
		{"POST", fmt.Sprintf("/ether/api/users/%d/merge", bob), `{"into":99999}`, 404},
		{"DELETE", "/ether/api/users/99999", ``, 404},
	} {
		if res := do(tc.method, tc.path, tc.body); res.Code != tc.want {
			t.Fatalf("%s %s: %d %s, want %d", tc.method, tc.path, res.Code, res.Body, tc.want)
		}
	}

	// Sign-out is allowed for a Plex viewer (Ether-side grants only).
	plexToken, err := grants.Issue(ctx, plex, nil, "jellyfin", core.Device{})
	if err != nil {
		t.Fatal(err)
	}
	res = do("POST", fmt.Sprintf("/ether/api/users/%d/signout", plex), `{}`)
	if res.Code != 200 {
		t.Fatalf("signout plex: %d %s", res.Code, res.Body)
	}
	var signed struct {
		Revoked int `json:"revoked"`
	}
	decode(t, res, &signed)
	if signed.Revoked < 1 {
		t.Fatalf("signout revoked=%d, want >=1", signed.Revoked)
	}
	if _, err := grants.Resolve(ctx, "jellyfin", plexToken); err == nil {
		t.Fatal("signed-out plex grant still resolves")
	}

	// The old membership route is gone.
	r := req("PATCH", fmt.Sprintf("/ether/api/users/%d/membership", bob), `{"active":false}`)
	r.Header.Set("Authorization", "Bearer "+ownerToken)
	if rec := serve(h, r); rec.Code != 404 {
		t.Fatalf("membership route still live: %d", rec.Code)
	}

	// Merge bob into amy, then remove amy: the full lifecycle over HTTP.
	res = do("POST", fmt.Sprintf("/ether/api/users/%d/merge", bob), fmt.Sprintf(`{"into":%d}`, amy))
	if res.Code != 200 {
		t.Fatalf("merge: %d %s", res.Code, res.Body)
	}
	var merged struct {
		Merged bool  `json:"merged"`
		Into   int64 `json:"into"`
	}
	decode(t, res, &merged)
	if !merged.Merged || merged.Into != amy {
		t.Fatalf("merge response %+v", merged)
	}
	if res = do("GET", fmt.Sprintf("/ether/api/users/%d", bob), ""); res.Code != 404 {
		t.Fatalf("merged-away user still visible: %d", res.Code)
	}
	res = do("DELETE", fmt.Sprintf("/ether/api/users/%d", amy), "")
	if res.Code != 200 {
		t.Fatalf("remove: %d %s", res.Code, res.Body)
	}
	var removed struct {
		Removed bool `json:"removed"`
	}
	decode(t, res, &removed)
	if !removed.Removed {
		t.Fatalf("remove response %+v", removed)
	}
	if res = do("GET", fmt.Sprintf("/ether/api/users/%d", amy), ""); res.Code != 404 {
		t.Fatalf("removed user still visible: %d", res.Code)
	}
}
