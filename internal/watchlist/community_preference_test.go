package watchlist

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestCommunityPreferenceRoutesArePerViewer(t *testing.T) {
	s, _, _, _, _, _ := setup(t)
	s.o.Users = func(context.Context) ([]core.User, error) {
		return []core.User{{ID: 11}, {ID: 12}}, nil
	}
	router := s.Router()
	req := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder) map[string]bool {
		t.Helper()
		var got map[string]bool
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode response %q: %v", w.Body.String(), err)
		}
		return got
	}

	if w := req("GET", "/users/11/community", ""); w.Code != 200 || decode(w)["syncCommunity"] {
		t.Fatalf("default GET: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := req("PUT", "/users/11/community", `{"syncCommunity":true}`); w.Code != 200 || !decode(w)["syncCommunity"] {
		t.Fatalf("PUT opt-in: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := req("GET", "/users/11/community", ""); w.Code != 200 || !decode(w)["syncCommunity"] {
		t.Fatalf("persisted GET: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := req("GET", "/users/12/community", ""); w.Code != 200 || decode(w)["syncCommunity"] {
		t.Fatalf("other viewer GET: status=%d body=%s", w.Code, w.Body.String())
	}
	if w := req("PUT", "/users/11/community", `{"syncCommunity":true} trailing`); w.Code != 400 {
		t.Fatalf("malformed body status=%d body=%s, want 400", w.Code, w.Body.String())
	}
}

func TestCommunitySyncPreferenceOnlyCallsCommunityForOptedInViewer(t *testing.T) {
	s, _, _, _, _, _ := setup(t)
	ctx := context.Background()
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Plex: true, CheckHours: 6}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommunitySyncEnabled(ctx, 21, true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.o.CommunityEligible = func(*core.User) bool { return true }
	s.o.Users = func(context.Context) ([]core.User, error) { return []core.User{{ID: 1, IsOwner: true}}, nil }
	s.o.Tokens = func(_ context.Context, id int64) (string, error) {
		if id == 1 {
			return "owner-token", nil
		}
		return "", core.ErrUnauthorized
	}
	s.o.CommunityBaseURL = "http://community.invalid"
	s.o.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 200, Body: ioNopCloser{strings.NewReader(`{"data":{"allFriendsV2":[{"user":{"id":"viewer-a"}}]}}`)}, Header: make(http.Header)}, nil
	})}
	for _, u := range []*core.User{{ID: 21, PlexID: 7, UUID: "viewer-a"}, {ID: 22, PlexID: 8, UUID: "viewer-b"}} {
		// Viewer credential lookup is expected to fail in this isolated test;
		// it is separate from the Community request being counted.
		_ = s.Sync(ctx, u)
	}
	if calls != 2 { // One owner friend-list check and one watchlist page for viewer 21.
		t.Fatalf("Community HTTP calls=%d, want 2 for opted-in viewer only", calls)
	}
}
