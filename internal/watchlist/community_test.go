package watchlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestCommunityFriendsPagedSnapshots(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	us := []core.User{{ID: 1, PlexID: 77, Username: "viewer"}}
	calls := 0
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "owner" {
			t.Error("owner token missing")
		}
		var body struct {
			Query     string `json:"query"`
			Variables struct {
				User struct {
					ID string `json:"id"`
				} `json:"user"`
				After *string `json:"after"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Variables.User.ID == "" {
			fmt.Fprint(w, `{"data":{"allFriendsV2":[{"user":{"id":"77","username":"viewer"}},{"user":{"id":"uuid-other","username":"other"}}]}}`)
			return
		}
		calls++
		if fail {
			fmt.Fprint(w, `{"errors":[{"message":"private"}]}`)
			return
		}
		id := "tmdb://101"
		next := true
		cursor := "second"
		if body.Variables.After != nil {
			id = "tmdb://102"
			next = false
			cursor = ""
		}
		fmt.Fprintf(w, `{"data":{"userV2":{"watchlist":{"nodes":[{"id":%q,"type":"movie"}],"pageInfo":{"hasNextPage":%t,"endCursor":%q}}}}}`, id, next, cursor)
	}))
	defer server.Close()
	titles := map[core.RatingKey]core.Title{}
	for _, id := range []int64{101, 102} {
		titles[core.MovieKey(id)] = core.Title{Key: core.MovieKey(id), Type: core.TypeMovie, Title: "Movie"}
	}
	s := New(db, Options{Users: func(context.Context) ([]core.User, error) { return us, nil }, Catalog: fakeCatalog{titles: titles}, Community: &plextv.CommunityClient{Client: server.Client(), URL: server.URL}, OwnerToken: func(context.Context) (string, error) { return "owner", nil }, Tokens: func(context.Context, int64) (string, error) { return "", core.ErrUnauthorized }})
	if err := s.SaveSettings(ctx, Settings{Enabled: true, Plex: true, CheckHours: 6}); err != nil {
		t.Fatal(err)
	}
	if err := s.syncCommunityFriends(ctx, us, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("watchlist pages=%d", calls)
	}
	for _, uid := range []int64{0, 1} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM watchlist_items WHERE user_id=?`, uid).Scan(&count); err != nil || count != 2 {
			t.Fatalf("user %d: count=%d err=%v", uid, count, err)
		}
	}
	// A later failed page must not erase either successful snapshot.
	fail = true
	if err := s.Sync(ctx, &us[0]); err == nil {
		t.Fatal("expected upstream failure")
	}
	var count int
	_ = db.QueryRow(`SELECT COUNT(*) FROM watchlist_items WHERE user_id=1`).Scan(&count)
	if count != 2 {
		t.Fatalf("lost snapshot: %d", count)
	}
	statuses := s.friendStatuses(ctx)
	if len(statuses) != 2 || statuses[0].Error == "" {
		t.Fatalf("missing per-friend status: %+v", statuses)
	}
}
