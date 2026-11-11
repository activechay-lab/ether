package admin_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/trakt"
)

type testTraktAdapter struct {
	client *trakt.Client
}

func toAdminTraktStatus(st trakt.UserStatus) admin.TraktStatus {
	out := admin.TraktStatus{
		Configured: st.Configured,
		Connected:  st.Connected,
		Username:   st.Username,
		Status:     string(st.Status),
	}
	if st.Device != nil {
		out.Device = &admin.TraktDevice{
			UserCode:        st.Device.UserCode,
			VerificationURL: st.Device.VerificationURL,
			ExpiresAt:       st.Device.ExpiresAt,
			Interval:        st.Device.Interval,
		}
	}
	return out
}

func toAdminTraktErr(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, trakt.ErrNotConfigured):
		return admin.ErrTraktNotConfigured
	case errors.Is(err, trakt.ErrNoDeviceCode):
		return admin.ErrTraktNoDeviceCode
	default:
		return err
	}
}

func (a testTraktAdapter) Status(ctx context.Context, userID int64) (admin.TraktStatus, error) {
	st, err := a.client.UserStatus(ctx, userID)
	return toAdminTraktStatus(st), toAdminTraktErr(err)
}

func (a testTraktAdapter) StartDevice(ctx context.Context, userID int64) (admin.TraktStatus, error) {
	st, err := a.client.StartUserDevice(ctx, userID)
	return toAdminTraktStatus(st), toAdminTraktErr(err)
}

func (a testTraktAdapter) PollDevice(ctx context.Context, userID int64) (admin.TraktStatus, error) {
	st, err := a.client.PollUserDevice(ctx, userID)
	return toAdminTraktStatus(st), toAdminTraktErr(err)
}

func (a testTraktAdapter) Disconnect(ctx context.Context, userID int64) (admin.TraktStatus, error) {
	st, err := a.client.DisconnectUser(ctx, userID)
	return toAdminTraktStatus(st), toAdminTraktErr(err)
}

type fakeUpstreamTrakt struct {
	mu              sync.Mutex
	codeHits        int
	tokenHits       int
	verificationURL string
	tokenStatuses   []int
	username        string
}

func (f *fakeUpstreamTrakt) nextStatus() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenHits++
	if len(f.tokenStatuses) == 0 {
		return http.StatusOK
	}
	st := f.tokenStatuses[0]
	f.tokenStatuses = f.tokenStatuses[1:]
	return st
}

func (f *fakeUpstreamTrakt) hits() (codes, tokens int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.codeHits, f.tokenHits
}

func (f *fakeUpstreamTrakt) setStatuses(statuses ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokenStatuses = append([]int(nil), statuses...)
}

func newFakeTraktServer(t *testing.T, f *fakeUpstreamTrakt) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/device/code", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["client_id"] != "cid" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		f.mu.Lock()
		f.codeHits++
		vurl := f.verificationURL
		f.mu.Unlock()
		if vurl == "" {
			vurl = "https://trakt.tv/activate"
		}
		_ = json.NewEncoder(w).Encode(trakt.DeviceCode{
			DeviceCode:      "secret-device-code-xyz",
			UserCode:        "ABCD-1234",
			VerificationURL: vurl,
			ExpiresIn:       600,
			Interval:        5,
		})
	})
	mux.HandleFunc("/oauth/device/token", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["code"] != "secret-device-code-xyz" || body["client_id"] != "cid" || body["client_secret"] != "csec" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		st := f.nextStatus()
		if st != http.StatusOK {
			w.WriteHeader(st)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "secret-access-token-raw",
			"refresh_token": "secret-refresh-token-raw",
			"expires_in":    7200,
			"created_at":    1700000000,
		})
	})
	mux.HandleFunc("/users/me", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-access-token-raw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		f.mu.Lock()
		uname := f.username
		f.mu.Unlock()
		if uname == "" {
			uname = "bobtrakt"
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"username": uname})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestTraktDeviceFlowRoutes(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, err := identity.NewLocal(db)
	if err != nil {
		t.Fatal(err)
	}
	bobID, err := local.CreateViewer(ctx, "bob", "secret-pass1234")
	if err != nil {
		t.Fatal(err)
	}
	if err := users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}

	grants, err := identity.NewGrants(db, users, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	pollLimiter, err := identity.NewLoginLimiter(db, time.Minute, 100, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	bobPortalGrant, err := grants.Issue(ctx, bobID, nil, "viewer-portal", core.Device{Client: "phone"})
	if err != nil {
		t.Fatal(err)
	}
	bobDeviceGrant, err := grants.Issue(ctx, bobID, nil, "jellyfin", core.Device{Client: "tv"})
	if err != nil {
		t.Fatal(err)
	}

	var clockMu sync.Mutex
	now := time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)
	clockNow := func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}
	advance := func(d time.Duration) {
		clockMu.Lock()
		now = now.Add(d)
		clockMu.Unlock()
	}

	upstream := &fakeUpstreamTrakt{
		verificationURL: "http://evil.example/phish",
		username:        "bobtrakt",
	}
	srv := newFakeTraktServer(t, upstream)

	cfgStore := newStore(t, func(c *config.Config) {
		seededConfig(c)
		c.SetupComplete = true
		c.Trakt = config.Trakt{}
	})

	traktClient := trakt.New(trakt.Options{
		Config:          cfgStore.Get().Trakt,
		DB:              db,
		Sealer:          cfgStore,
		BaseURL:         srv.URL,
		Now:             clockNow,
		MinPostInterval: -1,
	})

	h := admin.NewHandler(admin.Deps{
		Config:         cfgStore,
		IsOwnerToken:   storedOwner(cfgStore),
		IdentityGrants: grants,
		PollLimiter:    pollLimiter,
		Trakt:          testTraktAdapter{client: traktClient},
		ViewerDirectory: func(context.Context) ([]admin.ViewerRecord, error) {
			return []admin.ViewerRecord{
				{User: core.User{ID: 1, Username: "alice", Title: "Alice", IsOwner: true}, Active: true, OwnerEnabled: true},
				{User: core.User{ID: bobID, Username: "bob", Title: "Bob"}, Active: true, OwnerEnabled: true},
			}, nil
		},
		ViewerPortalURL: func(context.Context, *http.Request) (string, error) {
			return "https://ether.example/ether/me", nil
		},
	})

	ownerReq := func(method, path string) *http.Request {
		r := req(method, path, "")
		r.Header.Set("X-Plex-Token", ownerToken)
		return r
	}
	portalReq := func(method, path string) *http.Request {
		r := req(method, path, "")
		r.Header.Set("Authorization", "Bearer "+bobPortalGrant)
		return r
	}
	assertNoSecrets := func(body string) {
		t.Helper()
		for _, secret := range []string{"secret-device-code-xyz", "secret-access-token-raw", "secret-refresh-token-raw", "csec"} {
			if strings.Contains(body, secret) {
				t.Fatalf("secret %q leaked in response body: %s", secret, body)
			}
		}
	}

	bobBase := fmt.Sprintf("/ether/api/viewers/%d/trakt", bobID)

	// 1. Auth and CSRF checks on owner and viewer-portal routes.
	if rec := serve(h, req(http.MethodGet, bobBase, "")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon owner status: got %d, want 401", rec.Code)
	}
	if rec := serve(h, portalReq(http.MethodGet, bobBase)); rec.Code != http.StatusUnauthorized {
		t.Fatalf("portal token on owner route: got %d, want 401", rec.Code)
	}
	noCSRF := ownerReq(http.MethodPost, bobBase+"/device")
	noCSRF.Header.Del("X-Ether-Request")
	if rec := serve(h, noCSRF); rec.Code != http.StatusForbidden {
		t.Fatalf("owner POST without CSRF: got %d, want 403", rec.Code)
	}
	if rec := serve(h, req(http.MethodGet, "/ether/api/me/trakt", "")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon portal status: got %d, want 401", rec.Code)
	}
	if rec := serve(h, ownerReq(http.MethodGet, "/ether/api/me/trakt")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("owner token on portal route: got %d, want 401", rec.Code)
	}
	wrongSurface := req(http.MethodGet, "/ether/api/me/trakt", "")
	wrongSurface.Header.Set("Authorization", "Bearer "+bobDeviceGrant)
	if rec := serve(h, wrongSurface); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong-surface grant on portal route: got %d, want 401", rec.Code)
	}
	portalNoCSRF := portalReq(http.MethodPost, "/ether/api/me/trakt/device")
	portalNoCSRF.Header.Del("X-Ether-Request")
	if rec := serve(h, portalNoCSRF); rec.Code != http.StatusForbidden {
		t.Fatalf("portal POST without CSRF: got %d, want 403", rec.Code)
	}

	// 2. Unconfigured Trakt reports configured=false and rejects starting a code.
	rec := serve(h, ownerReq(http.MethodGet, bobBase))
	if rec.Code != http.StatusOK {
		t.Fatalf("owner status unconfigured: %d %s", rec.Code, rec.Body)
	}
	var st admin.TraktStatus
	decode(t, rec, &st)
	if st.Configured || st.Connected || st.PortalURL != "https://ether.example/ether/me" {
		t.Fatalf("unexpected unconfigured status: %+v", st)
	}
	if rec := serve(h, ownerReq(http.MethodPost, bobBase+"/device")); rec.Code != http.StatusConflict {
		t.Fatalf("start when unconfigured: got %d, want 409", rec.Code)
	}

	// 3. Owner flow: pending -> slow_down -> authorized, server-side interval gating,
	// URL sanitization, zero secret leakage, and sealed SQLite tokens.
	traktClient.UpdateConfig(config.Trakt{Enabled: true, ClientID: "cid", ClientSecret: "csec"})
	upstream.setStatuses(http.StatusBadRequest, http.StatusTooManyRequests, http.StatusOK)

	rec = serve(h, ownerReq(http.MethodPost, bobBase+"/device"))
	if rec.Code != http.StatusOK {
		t.Fatalf("owner start device: %d %s", rec.Code, rec.Body)
	}
	assertNoSecrets(rec.Body.String())
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if !st.Configured || st.Connected || st.Status != "pending" || st.Device == nil {
		t.Fatalf("unexpected start status: %+v", st)
	}
	if st.Device.UserCode != "ABCD-1234" || st.Device.VerificationURL != "https://trakt.tv/activate" || st.Device.Interval != 5 {
		t.Fatalf("unexpected device challenge: %+v", st.Device)
	}

	// Early poll before Interval (2s < 5s) must stay server-gated and not call Trakt.
	advance(2 * time.Second)
	rec = serve(h, ownerReq(http.MethodPost, bobBase+"/poll"))
	if rec.Code != http.StatusOK {
		t.Fatalf("early poll: %d %s", rec.Code, rec.Body)
	}
	if _, tokHits := upstream.hits(); tokHits != 0 {
		t.Fatalf("expected 0 upstream token polls before interval, got %d", tokHits)
	}

	// At +5s total, first upstream poll runs and sees 400 (pending).
	advance(3 * time.Second)
	rec = serve(h, ownerReq(http.MethodPost, bobBase+"/poll"))
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if st.Status != "pending" || st.Connected {
		t.Fatalf("expected pending after first upstream poll, got %+v", st)
	}
	if _, tokHits := upstream.hits(); tokHits != 1 {
		t.Fatalf("expected 1 upstream token poll, got %d", tokHits)
	}

	// At +5s later, second upstream poll sees 429 (slow_down) and bumps interval to 10s.
	advance(5 * time.Second)
	rec = serve(h, ownerReq(http.MethodPost, bobBase+"/poll"))
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if st.Status != "slow_down" || st.Device == nil || st.Device.Interval != 10 {
		t.Fatalf("expected slow_down with interval 10, got %+v (device=%+v)", st, st.Device)
	}

	// Polling 6s after slow_down (< 10s) must still be gated server-side.
	advance(6 * time.Second)
	serve(h, ownerReq(http.MethodPost, bobBase+"/poll"))
	if _, tokHits := upstream.hits(); tokHits != 2 {
		t.Fatalf("expected slow_down interval gating (2 hits), got %d", tokHits)
	}

	// At +10s after slow_down, poll succeeds (200), fetches /users/me, and seals tokens.
	advance(4 * time.Second)
	rec = serve(h, ownerReq(http.MethodPost, bobBase+"/poll"))
	if rec.Code != http.StatusOK {
		t.Fatalf("authorizing poll: %d %s", rec.Code, rec.Body)
	}
	assertNoSecrets(rec.Body.String())
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if !st.Connected || st.Username != "bobtrakt" || st.Status != "authorized" || st.Device != nil {
		t.Fatalf("unexpected authorized status: %+v", st)
	}

	// Verify raw SQLite row is encrypted via FieldSealer and never stored plaintext.
	var rawAccess, rawRefresh string
	if err := db.QueryRow(`SELECT access_token, refresh_token FROM trakt_tokens WHERE user_id = ?`, bobID).Scan(&rawAccess, &rawRefresh); err != nil {
		t.Fatalf("query trakt_tokens: %v", err)
	}
	if rawAccess == "secret-access-token-raw" || rawRefresh == "secret-refresh-token-raw" {
		t.Fatal("trakt tokens stored unsealed in SQLite")
	}
	if !strings.HasPrefix(rawAccess, "ether-sealed:") || !strings.HasPrefix(rawRefresh, "ether-sealed:") {
		t.Fatalf("expected ether-sealed tokens, got %q / %q", rawAccess, rawRefresh)
	}

	// Owner disconnect clears stored tokens.
	rec = serve(h, ownerReq(http.MethodPost, bobBase+"/disconnect"))
	if rec.Code != http.StatusOK {
		t.Fatalf("owner disconnect: %d %s", rec.Code, rec.Body)
	}
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if st.Connected {
		t.Fatalf("expected disconnected, got %+v", st)
	}

	// 4. Viewer self-service portal (/api/me/trakt): expired (410 + deadline), denied (418), and connect + disconnect.
	upstream.verificationURL = "https://trakt.tv/activate"

	// 4a. Upstream 410 -> expired, then 410 Gone when polling with no active code.
	upstream.setStatuses(http.StatusGone)
	rec = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/device"))
	if rec.Code != http.StatusOK {
		t.Fatalf("portal start: %d %s", rec.Code, rec.Body)
	}
	advance(5 * time.Second)
	rec = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/poll"))
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if st.Status != "expired" || st.Connected || st.Device != nil {
		t.Fatalf("expected expired status, got %+v", st)
	}
	if rec := serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/poll")); rec.Code != http.StatusGone {
		t.Fatalf("poll with no pending code: got %d, want 410", rec.Code)
	}

	// 4b. Server-side ExpiresIn deadline expiry without calling Trakt.
	_, hitsBefore := upstream.hits()
	_ = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/device"))
	advance(601 * time.Second)
	rec = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/poll"))
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if st.Status != "expired" {
		t.Fatalf("expected deadline expired status, got %+v", st)
	}
	if _, hitsAfter := upstream.hits(); hitsAfter != hitsBefore {
		t.Fatalf("expired flow should not poll upstream: before=%d after=%d", hitsBefore, hitsAfter)
	}

	// 4c. Upstream 418 -> denied.
	upstream.setStatuses(http.StatusTeapot)
	_ = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/device"))
	advance(5 * time.Second)
	rec = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/poll"))
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if st.Status != "denied" || st.Connected || st.Device != nil {
		t.Fatalf("expected denied status, got %+v", st)
	}

	// 4d. Portal pending -> authorized -> status -> disconnect.
	upstream.setStatuses(http.StatusBadRequest, http.StatusOK)
	_ = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/device"))
	advance(5 * time.Second)
	rec = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/poll"))
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if st.Status != "pending" {
		t.Fatalf("expected portal pending, got %+v", st)
	}
	advance(5 * time.Second)
	rec = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/poll"))
	assertNoSecrets(rec.Body.String())
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if !st.Connected || st.Username != "bobtrakt" || st.Status != "authorized" {
		t.Fatalf("expected portal authorized, got %+v", st)
	}

	rec = serve(h, portalReq(http.MethodGet, "/ether/api/me/trakt"))
	assertNoSecrets(rec.Body.String())
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if !st.Connected || st.Username != "bobtrakt" {
		t.Fatalf("expected portal status connected, got %+v", st)
	}

	rec = serve(h, portalReq(http.MethodPost, "/ether/api/me/trakt/disconnect"))
	st = admin.TraktStatus{}
	decode(t, rec, &st)
	if st.Connected {
		t.Fatalf("expected portal disconnected, got %+v", st)
	}
}
