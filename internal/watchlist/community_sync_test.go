package watchlist

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestCommunitySyncIsDefaultOffAndPreservesExistingMembership(t *testing.T) {
	s, _, _, _, source, _ := setup(t)
	ctx := context.Background()
	// Isolate the community flag: setup enables Trakt, which would add a
	// second source to the seeded title and mask membership preservation.
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Plex: true, CheckHours: 6}); err != nil {
		t.Fatal(err)
	}
	viewer := &core.User{ID: 2, PlexID: 7, UUID: "friend-uuid"}
	if err := s.upsert(ctx, viewer.ID, communitySource, source.titles[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Plex: true, Trakt: false, Community: false, CheckHours: 6}); err != nil {
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
	s.o.Client = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, fmt.Errorf("unexpected request")
	})}
	settings, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if settings.Community {
		t.Fatal("community sync must be disabled by default")
	}
	if err = s.Sync(ctx, viewer); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatalf("default-off sync made %d Community requests", calls)
	}
	items, err := s.Items(ctx, viewer)
	if err != nil || len(items) != 1 {
		t.Fatalf("membership changed while disabled: items=%+v err=%v", items, err)
	}
	found := false
	for _, source := range items[0].Sources {
		found = found || source == communitySource
	}
	if !found {
		t.Fatalf("community membership lost while disabled: items=%+v", items)
	}
}

func TestCommunitySyncRequiresEligibilityAndPreservesSnapshotOnFailure(t *testing.T) {
	s, _, _, _, source, _ := setup(t)
	ctx := context.Background()
	viewer := &core.User{ID: 2, PlexID: 7, UUID: "friend-uuid"}
	if err := s.SetCommunitySyncEnabled(ctx, viewer.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.upsert(ctx, viewer.ID, communitySource, source.titles[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Plex: true, Community: false, CheckHours: 6}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.o.CommunityEligible = func(u *core.User) bool { return u.ID == viewer.ID && !u.IsOwner }
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
		if r.Header.Get("X-Plex-Token") != "owner-token" {
			t.Errorf("wrong token %q", r.Header.Get("X-Plex-Token"))
		}
		body := `{"errors":[{"message":"private or unavailable"}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: ioNopCloser{strings.NewReader(body)}, Header: make(http.Header)}, nil
	})}
	if err := s.Sync(ctx, viewer); err == nil {
		t.Fatal("Community failure should be reported")
	}
	if calls != 1 {
		t.Fatalf("Community requests %d, want one friend-list request", calls)
	}
	items, err := s.Items(ctx, viewer)
	if err != nil || len(items) != 1 || len(items[0].Sources) != 1 || items[0].Sources[0] != communitySource {
		t.Fatalf("snapshot lost on Community failure: items=%+v err=%v", items, err)
	}
}

func TestCommunitySyncFailureOnLaterPagePreservesSnapshotAndOtherSources(t *testing.T) {
	s, _, _, _, source, _ := setup(t)
	ctx := context.Background()
	viewer := &core.User{ID: 2, PlexID: 7, UUID: "friend-uuid"}
	if err := s.SetCommunitySyncEnabled(ctx, viewer.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.upsert(ctx, viewer.ID, communitySource, source.titles[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.upsert(ctx, viewer.ID, "plex", source.titles[1]); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Plex: true, Community: false, CheckHours: 6}); err != nil {
		t.Fatal(err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		switch requests {
		case 1:
			fmt.Fprint(w, `{"data":{"allFriendsV2":[{"user":{"id":"friend-uuid"}}]}}`)
		case 2:
			fmt.Fprint(w, `{"data":{"userV2":{"watchlist":{"nodes":[],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}`)
		default:
			fmt.Fprint(w, `{"errors":[{"message":"second page failed"}]}`)
		}
	}))
	defer server.Close()
	s.o.CommunityEligible = func(*core.User) bool { return true }
	s.o.Users = func(context.Context) ([]core.User, error) { return []core.User{{ID: 1, IsOwner: true}}, nil }
	s.o.Tokens = func(_ context.Context, id int64) (string, error) {
		if id == 1 {
			return "owner-token", nil
		}
		return "", core.ErrUnauthorized
	}
	s.o.CommunityBaseURL = server.URL
	if err := s.Sync(ctx, viewer); err == nil {
		t.Fatal("expected second-page failure")
	}
	items, err := s.Items(ctx, viewer)
	if err != nil || len(items) != 1 {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	seen := map[string]bool{}
	for _, item := range items {
		for _, src := range item.Sources {
			seen[src] = true
		}
	}
	if !seen[communitySource] {
		t.Fatalf("snapshot/source rows lost: %+v", items)
	}
}

func TestCommunitySyncSkipsIneligibleViewers(t *testing.T) {
	s, _, _, _, _, _ := setup(t)
	ctx := context.Background()
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Plex: true, Community: false, CheckHours: 6}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCommunitySyncEnabled(ctx, 2, true); err != nil {
		t.Fatal(err)
	}
	calls := 0
	s.o.CommunityEligible = func(*core.User) bool { return false }
	s.o.Users = func(context.Context) ([]core.User, error) { return []core.User{{ID: 1, IsOwner: true}}, nil }
	s.o.Tokens = func(context.Context, int64) (string, error) { calls++; return "", core.ErrUnauthorized }
	if err := s.Sync(ctx, &core.User{ID: 2, PlexID: 7, UUID: "friend-uuid"}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 { // Existing viewer-token path may ask for the viewer credential.
		t.Fatalf("eligible owner token should not be requested for excluded viewer; calls=%d", calls)
	}
}

// roundTripFunc makes transport expectations explicit in sync-level tests.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type ioNopCloser struct{ *strings.Reader }

func (ioNopCloser) Close() error { return nil }
