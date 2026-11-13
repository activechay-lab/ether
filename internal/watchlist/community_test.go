package watchlist

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestCommunityWatchlistPagesAndValidatesFriendIdentity(t *testing.T) {
	pages := 0
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/metadata/opaqueShowA" && r.URL.Path != "/library/metadata/opaqueShowB" {
			t.Errorf("metadata path %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"type":"show","guid":"plex://show/example","Guid":[{"id":"tmdb://65804"}]}]}}`)
	}))
	defer metadata.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" || r.Header.Get("X-Plex-Token") != "owner-token" {
			t.Errorf("path/token %q %q", r.URL.Path, r.Header.Get("X-Plex-Token"))
		}
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Query == "query allFriendsV2 { allFriendsV2 { user { id username } } }" {
			fmt.Fprint(w, `{"data":{"allFriendsV2":[{"user":{"id":"friend-uuid","username":"friend"}}]}}`)
			return
		}
		pages++
		if req.Query != "query userV2($user: UserInput!, $first: PaginationInt!, $after: String) { userV2(user: $user) { ... on User { watchlist(first: $first, after: $after) { nodes { id type title } pageInfo { hasNextPage endCursor } } } } }" {
			t.Errorf("unexpected watchlist query %q", req.Query)
		}
		user, ok := req.Variables["user"].(map[string]any)
		if !ok || user["id"] != "friend-uuid" {
			t.Errorf("variables.user.id = %#v, want friend UUID", req.Variables["user"])
		}
		if pages == 1 {
			if req.Variables["first"] != float64(100) || req.Variables["after"] != nil {
				t.Errorf("vars %#v", req.Variables)
			}
			fmt.Fprint(w, `{"data":{"userV2":{"watchlist":{"nodes":[{"id":"opaqueShowA","type":"SHOW"}],"pageInfo":{"hasNextPage":true,"endCursor":"next"}}}}}`)
			return
		}
		if req.Variables["after"] != "next" {
			t.Errorf("cursor %#v", req.Variables["cursor"])
		}
		fmt.Fprint(w, `{"data":{"userV2":{"watchlist":{"nodes":[{"id":"opaqueShowB","type":"SHOW"}],"pageInfo":{"hasNextPage":false,"endCursor":null}}}}}`)
	}))
	defer server.Close()
	titles := map[core.RatingKey]core.Title{core.ShowKey(65804): {Key: core.ShowKey(65804), Type: core.TypeShow}}
	s, _, _, _, _, _ := setup(t)
	s.o.Catalog = fakeCatalog{titles: titles}
	s.o.PlexBaseURL = metadata.URL
	s.o.CommunityBaseURL = server.URL
	got, err := s.communityWatchlist(context.Background(), "owner-token", &core.User{ID: 2, PlexID: 7, UUID: "friend-uuid"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Key != core.ShowKey(65804) || got[1].Key != core.ShowKey(65804) || pages != 2 {
		t.Fatalf("got %d over %d pages", len(got), pages)
	}
	if _, err = s.communityWatchlist(context.Background(), "owner-token", &core.User{ID: 2, PlexID: 7, UUID: "different-uuid"}); err == nil {
		t.Fatal("accepted mismatched Plex ID")
	}
	if _, err = s.communityWatchlist(context.Background(), "owner-token", &core.User{ID: 2, PlexID: 7}); err == nil {
		t.Fatal("accepted missing UUID")
	}
}

func TestCommunityFailurePreservesExistingSources(t *testing.T) {
	s, _, _, _, existing, _ := setup(t)
	ctx := context.Background()
	if err := s.upsert(ctx, 1, "plex", existing.titles[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.upsert(ctx, 1, communitySource, existing.titles[1]); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"errors":[{"message":"unavailable"}]}`) }))
	defer server.Close()
	s.o.CommunityBaseURL = server.URL
	if _, err := s.communityWatchlist(ctx, "owner-token", &core.User{ID: 2, PlexID: 7, UUID: "friend"}); err == nil {
		t.Fatal("expected GraphQL failure")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT source,count(*) FROM watchlist_items GROUP BY source`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var source string
		var n int
		if err = rows.Scan(&source, &n); err != nil {
			t.Fatal(err)
		}
		counts[source] = n
	}
	if counts["plex"] != 1 || counts[communitySource] != 1 {
		t.Fatalf("source membership changed: %#v", counts)
	}
}

func TestCommunityMOVIEResponseResolvesTMDBBeforeIMDb(t *testing.T) {
	key := core.MovieKey(101)
	titles := map[core.RatingKey]core.Title{key: {Key: key, Type: core.TypeMovie}}
	s, _, _, _, _, _ := setup(t)
	s.o.Catalog = fakeCatalog{titles: titles}
	metadata := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"type":"movie","guid":"plex://movie/example","Guid":[{"id":"tmdb://101"},{"id":"imdb://tt1234567"}]}]}}`)
	}))
	defer metadata.Close()
	s.o.PlexBaseURL = metadata.URL
	got, err := s.communityNodeTitle(context.Background(), "token", "opaqueMovie", "MOVIE")
	if err != nil {
		t.Fatal(err)
	}
	if got.Key != key {
		t.Fatalf("resolved %v, want %v", got.Key, key)
	}
}

func TestCommunityPaginationAndMalformedItemsFail(t *testing.T) {
	for name, body := range map[string]string{
		"missing hasNextPage": `{"data":{"userV2":{"watchlist":{"nodes":[],"pageInfo":{"endCursor":null}}}}}`,
		"null hasNextPage":    `{"data":{"userV2":{"watchlist":{"nodes":[],"pageInfo":{"hasNextPage":null,"endCursor":null}}}}}`,
		"malformed node":      `{"data":{"userV2":{"watchlist":{"nodes":[{"id":"","type":"movie"}],"pageInfo":{"hasNextPage":false}}}}}`,
		"nil title":           `{"data":{"userV2":{"watchlist":{"nodes":[{"id":"tmdb://1","type":"movie"}],"pageInfo":{"hasNextPage":false}}}}}`,
		"mismatched title":    `{"data":{"userV2":{"watchlist":{"nodes":[{"id":"tmdb://1","type":"show"}],"pageInfo":{"hasNextPage":false}}}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			page := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req struct {
					Query string `json:"query"`
				}
				_ = json.NewDecoder(r.Body).Decode(&req)
				if strings.Contains(req.Query, "allFriendsV2") {
					fmt.Fprint(w, `{"data":{"allFriendsV2":[{"user":{"id":"friend","username":"f"}}]}}`)
					return
				}
				page = true
				fmt.Fprint(w, body)
			}))
			t.Cleanup(server.Close)
			s, _, _, _, _, _ := setup(t)
			s.o.CommunityBaseURL = server.URL
			s.o.Catalog = fakeCatalog{titles: map[core.RatingKey]core.Title{}}
			_, err := s.communityWatchlist(context.Background(), "token", &core.User{ID: 2, UUID: "friend"})
			if err == nil {
				t.Fatal("expected malformed response failure")
			}
			if !page {
				t.Fatal("watchlist page not requested")
			}
		})
	}
}

func TestSafeCommunityClientStopsCrossOriginRedirect(t *testing.T) {
	var leaked bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("X-Plex-Token") != ""
	}))
	defer destination.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL+"/steal", http.StatusFound)
	}))
	defer origin.Close()
	client := safeCommunityClient(nil, origin.URL)
	req, _ := http.NewRequest(http.MethodGet, origin.URL, nil)
	req.Header.Set("X-Plex-Token", "secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if leaked {
		t.Fatal("owner token leaked across origin redirect")
	}
}
