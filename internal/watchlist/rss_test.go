package watchlist

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
)

type rssFakeCatalog struct {
	core.Catalog
	titles map[core.RatingKey]core.Title
	imdb   map[string]core.Title
	tvdb   map[string]core.Title
}

func (c rssFakeCatalog) Get(_ context.Context, k core.RatingKey) (*core.Title, error) {
	t, ok := c.titles[k]
	if !ok {
		return nil, core.ErrNotFound
	}
	return &t, nil
}

func (c rssFakeCatalog) ByExternalID(_ context.Context, source, id string) (*core.Title, error) {
	switch source {
	case "imdb":
		if t, ok := c.imdb[id]; ok {
			return &t, nil
		}
	case "tvdb":
		if t, ok := c.tvdb[id]; ok {
			return &t, nil
		}
	}
	return nil, core.ErrNotFound
}

type rssFakeTrakt struct {
	mu     sync.Mutex
	linked map[int64]bool
	pushed []string
}

func (f *rssFakeTrakt) Watchlist(context.Context, int64, int) ([]core.Title, error) {
	return nil, nil
}

func (f *rssFakeTrakt) SetWatchlist(_ context.Context, userID int64, title core.Title, add bool) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.linked[userID] {
		return false, nil
	}
	f.pushed = append(f.pushed, fmt.Sprintf("%d:%d:%t", userID, title.Key, add))
	return true, nil
}

func TestPlexRSSFeedImportLifecycle(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	sealer, err := config.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := sealer.UpgradeCredentials(ctx); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 10, 15, 0, 0, 0, time.UTC)
	movie1 := core.Title{Key: core.MovieKey(101), Type: core.TypeMovie, Title: "Dune", ContentRating: "PG-13", Released: now.Add(-48 * time.Hour), IDs: core.IDs{TMDB: 101, IMDB: "tt1160419"}}
	movie2 := core.Title{Key: core.MovieKey(102), Type: core.TypeMovie, Title: "Arrival", ContentRating: "PG-13", Released: now.Add(-48 * time.Hour), IDs: core.IDs{TMDB: 102, IMDB: "tt2543164"}}
	show1 := core.Title{Key: core.ShowKey(201), Type: core.TypeShow, Title: "Severance", ContentRating: "TV-MA", Released: now.Add(-48 * time.Hour), IDs: core.IDs{TMDB: 201, TVDB: 371980}}
	movieOwner := core.Title{Key: core.MovieKey(103), Type: core.TypeMovie, Title: "Blade Runner 2049", ContentRating: "R", Released: now.Add(-48 * time.Hour), IDs: core.IDs{TMDB: 103, IMDB: "tt1856101"}}

	cat := rssFakeCatalog{
		titles: map[core.RatingKey]core.Title{
			movie1.Key:     movie1,
			movie2.Key:     movie2,
			show1.Key:      show1,
			movieOwner.Key: movieOwner,
		},
		imdb: map[string]core.Title{
			"tt1160419": movie1,
			"tt2543164": movie2,
			"tt1856101": movieOwner,
		},
		tvdb: map[string]core.Title{
			"371980": show1,
		},
	}

	users := []core.User{
		{ID: 1, PlexID: 42, Username: "owner", Title: "Owner", IsOwner: true},
		{ID: 2, PlexID: 1001, Username: "alice", Title: "Alice", Email: "alice@example.com"},
		{ID: 3, PlexID: 1002, Username: "bob", Title: "Bob", Email: "bob@example.com"},
		{ID: 4, PlexID: 1003, Username: "carol", Title: "Carol", Email: "carol@example.com"},
	}

	var mu sync.Mutex
	etag := `"feed-v1"`
	lastMod := "Tue, 10 Nov 2026 15:00:00 GMT"
	friendsXML := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:dc="http://purl.org/dc/elements/1.1/">
  <channel>
    <title>Friends Watchlist</title>
    <item>
      <title>Dune (2021)</title>
      <dc:creator>alice</dc:creator>
      <category>movie</category>
      <guid isPermaLink="false">imdb://tt1160419</guid>
    </item>
    <item>
      <title>Arrival (2016)</title>
      <dc:creator>alice</dc:creator>
      <category>movie</category>
      <guid isPermaLink="false">tmdb://102</guid>
    </item>
    <item>
      <title>Severance (2022)</title>
      <author>1002</author>
      <category>show</category>
      <guid isPermaLink="false">tvdb://371980</guid>
    </item>
    <item>
      <title>Dune (2021)</title>
      <category domain="friend">carol@example.com</category>
      <category>movie</category>
      <link>https://www.themoviedb.org/movie/101-dune</link>
    </item>
    <item>
      <title>Mystery Friend Pick</title>
      <dc:creator>not-a-viewer</dc:creator>
      <category>movie</category>
      <guid isPermaLink="false">imdb://tt1160419</guid>
    </item>
    <item>
      <title>Privacy Redacted Item</title>
      <category>movie</category>
      <guid isPermaLink="false">imdb://tt1160419</guid>
    </item>
    <item>
      <title>Unknown Catalogue Title</title>
      <dc:creator>bob</dc:creator>
      <category>movie</category>
      <guid isPermaLink="false">imdb://tt9999999</guid>
    </item>
  </channel>
</rss>`
	ownerXML := `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <title>Your Watchlist</title>
    <item>
      <title>Blade Runner 2049 (2017)</title>
      <category>movie</category>
      <guid isPermaLink="false">imdb://tt1856101</guid>
    </item>
  </channel>
</rss>`

	var friendsHits, friends304 int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/friends-secret-uuid":
			friendsHits++
			if r.Header.Get("If-None-Match") == etag && r.Header.Get("If-Modified-Since") == lastMod {
				friends304++
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			w.Header().Set("Last-Modified", lastMod)
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(friendsXML))
		case "/owner-secret-uuid":
			if r.Header.Get("If-None-Match") == `"owner-v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", `"owner-v1"`)
			w.Header().Set("Content-Type", "application/rss+xml")
			_, _ = w.Write([]byte(ownerXML))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	trakt := &rssFakeTrakt{linked: map[int64]bool{2: true}}
	res := &fakeResolver{}
	note := &fakeNotifier{}
	s := New(db, Options{
		Sealer:   sealer,
		Catalog:  cat,
		Resolver: res,
		Notifier: note,
		Trakt:    trakt,
		Users:    func(context.Context) ([]core.User, error) { return users, nil },
		Now:      func() time.Time { return now },
	})

	// Alice already has movie1 from a token-based Plex import: must dedupe and survive feed removal.
	if err := s.upsert(ctx, 2, "plex", movie1); err != nil {
		t.Fatal(err)
	}

	// Save settings via HTTP router and verify sealing at rest + redaction in API.
	friendsURL := srv.URL + "/friends-secret-uuid"
	ownerURL := srv.URL + "/owner-secret-uuid"
	putBody, _ := json.Marshal(Settings{
		Enabled:           true,
		Plex:              true,
		PlexFriendsRSSURL: friendsURL,
		PlexOwnerRSSURL:   ownerURL,
		Notify:            true,
		CheckHours:        6,
	})
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(putBody)))
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /settings = %d: %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "friends-secret-uuid") || strings.Contains(rec.Body.String(), "owner-secret-uuid") {
		t.Fatalf("API response leaked RSS URL: %s", rec.Body.String())
	}
	var apiCfg Settings
	if err := json.Unmarshal(rec.Body.Bytes(), &apiCfg); err != nil {
		t.Fatal(err)
	}
	if apiCfg.PlexFriendsRSSURL != Redacted || apiCfg.PlexOwnerRSSURL != Redacted {
		t.Fatalf("redaction mismatch: %+v", apiCfg)
	}
	var rawRow string
	if err := db.QueryRow(`SELECT value FROM watchlist_settings WHERE id=1`).Scan(&rawRow); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rawRow, "friends-secret-uuid") || !strings.Contains(rawRow, "ether-sealed:v2:") {
		t.Fatalf("settings row not sealed: %s", rawRow)
	}

	// Saving back the redacted placeholder keeps the sealed URLs intact.
	putRedacted, _ := json.Marshal(apiCfg)
	rec2 := httptest.NewRecorder()
	s.Router().ServeHTTP(rec2, httptest.NewRequest(http.MethodPut, "/settings", bytes.NewReader(putRedacted)))
	if rec2.Code != http.StatusOK {
		t.Fatalf("PUT redacted = %d: %s", rec2.Code, rec2.Body.String())
	}

	// Poll 1: imports across Alice, Bob, Carol, and Owner; counts unknown friends & unmatched title.
	if err := s.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	gotCfg, err := s.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	fst := gotCfg.PlexFriendsRSSStatus
	if fst == nil || !fst.Configured || fst.LastRead != now.Unix() || fst.Imported != 4 || fst.SkippedFriends != 2 || fst.UnmatchedTitles != 1 || fst.Problem != "" {
		t.Fatalf("friends status = %+v", fst)
	}
	ost := gotCfg.PlexOwnerRSSStatus
	if ost == nil || !ost.Configured || ost.Imported != 1 || ost.Problem != "" {
		t.Fatalf("owner status = %+v", ost)
	}

	// Alice has movie1 (deduped across "plex" + "plex-friends-rss") and movie2 (newly pushed to Trakt).
	aliceItems, err := s.Items(ctx, &users[1])
	if err != nil || len(aliceItems) != 2 {
		t.Fatalf("alice items = %+v (%v)", aliceItems, err)
	}
	trakt.mu.Lock()
	pushedCopy := append([]string(nil), trakt.pushed...)
	trakt.mu.Unlock()
	if len(pushedCopy) != 1 || pushedCopy[0] != fmt.Sprintf("2:%d:true", movie2.Key) {
		t.Fatalf("trakt pushes = %v (movie1 was already on watchlist, movie2 should push once)", pushedCopy)
	}

	// Bob got show1 (via PlexID <author>), Carol got movie1 (via email <category domain="friend"> + TMDB link), Owner got movieOwner.
	if bobItems, err := s.Items(ctx, &users[2]); err != nil || len(bobItems) != 1 || bobItems[0].Title.Key != show1.Key {
		t.Fatalf("bob items = %+v (%v)", bobItems, err)
	}
	if carolItems, err := s.Items(ctx, &users[3]); err != nil || len(carolItems) != 1 || carolItems[0].Title.Key != movie1.Key {
		t.Fatalf("carol items = %+v (%v)", carolItems, err)
	}
	if ownerItems, err := s.Items(ctx, &users[0]); err != nil || len(ownerItems) != 1 || ownerItems[0].Title.Key != movieOwner.Key {
		t.Fatalf("owner items = %+v (%v)", ownerItems, err)
	}

	// Downstream rows & availability notification work identically to token-based imports.
	if err := s.Check(ctx, &users[3]); err != nil {
		t.Fatal(err)
	}
	if hubs, err := s.Hubs(ctx, &users[3], 12); err != nil || len(hubs) != 1 || hubs[0].ID != "watchlist.available" || note.calls != 1 {
		t.Fatalf("carol hubs=%+v notifications=%d err=%v", hubs, note.calls, err)
	}

	// Poll 2: conditional request hits 304 Not Modified; items and counts remain intact, LastRead advances.
	now = now.Add(30 * time.Minute)
	if err := s.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	gotHits, got304 := friendsHits, friends304
	mu.Unlock()
	if gotHits != 2 || got304 != 1 {
		t.Fatalf("conditional request: hits=%d 304s=%d", gotHits, got304)
	}
	gotCfg, _ = s.Settings(ctx)
	if gotCfg.PlexFriendsRSSStatus.LastRead != now.Unix() || gotCfg.PlexFriendsRSSStatus.Imported != 4 {
		t.Fatalf("status after 304 = %+v", gotCfg.PlexFriendsRSSStatus)
	}

	// Poll 3: feed updates (new ETag), removing movie1 & movie2 from Alice and show1 from Bob.
	// Alice's movie1 MUST remain (came from token-based "plex" too), while movie2 and Bob's show1 are removed!
	mu.Lock()
	etag = `"feed-v2"`
	friendsXML = `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0">
  <channel>
    <item>
      <title>Dune (2021)</title>
      <category domain="friend">carol</category>
      <category>movie</category>
      <guid isPermaLink="false">imdb://tt1160419</guid>
    </item>
  </channel>
</rss>`
	mu.Unlock()

	now = now.Add(30 * time.Minute)
	if err := s.SyncRSS(ctx); err != nil {
		t.Fatal(err)
	}
	aliceAfter, err := s.Items(ctx, &users[1])
	if err != nil || len(aliceAfter) != 1 || aliceAfter[0].Title.Key != movie1.Key {
		t.Fatalf("alice after feed removal = %+v (%v)", aliceAfter, err)
	}
	bobAfter, err := s.Items(ctx, &users[2])
	if err != nil || len(bobAfter) != 0 {
		t.Fatalf("bob after feed removal = %+v (%v)", bobAfter, err)
	}
	gotCfg, _ = s.Settings(ctx)
	if gotCfg.PlexFriendsRSSStatus.Imported != 1 {
		t.Fatalf("imported after removal = %+v", gotCfg.PlexFriendsRSSStatus)
	}
}
