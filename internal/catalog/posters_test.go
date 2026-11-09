package catalog

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/artwork"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/store"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// Poster lookups for titles without a row: one TMDB request per title
// however many searches ask at once, at most posterLookupsPerPage a page,
// late answers kept for the next search, and never a stored row (only
// opening a title promotes it).
func TestFillPostersDedupesBoundsAndNeverStores(t *testing.T) {
	var requests atomic.Int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		<-release
		id := strings.TrimPrefix(r.URL.Path, "/movie/")
		if id == "3" { // TMDB has no poster for this one
			fmt.Fprintf(w, `{"id":3,"title":"Three","release_date":"2001-01-01","poster_path":null}`)
			return
		}
		fmt.Fprintf(w, `{"id":%s,"title":"T%s","release_date":"2001-01-01","poster_path":"/p%s.jpg"}`, id, id, id)
	}))
	defer srv.Close()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := tmdb.New("k", slog.Default(), tmdb.WithBaseURL(srv.URL), tmdb.WithCacheTTL(0))
	defer client.Close()
	c, err := New(db, client, config.Catalog{Language: "en-US", Region: "US"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	page := func() []core.Title {
		var out []core.Title
		for id := int64(1); id <= posterLookupsPerPage+4; id++ {
			out = append(out, core.Title{Key: core.MovieKey(id), Type: core.TypeMovie, Title: "x"})
		}
		return out
	}
	all := func(*core.Title) bool { return true }

	// Five concurrent searches show the same page while TMDB is slow: every
	// one answers at its deadline with no poster (frontends draw Ether's).
	var wg sync.WaitGroup
	for range 5 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := page()
			c.FillPosters(context.Background(), p, all, 20*time.Millisecond)
			for _, tt := range p {
				if tt.PosterPath != "" {
					t.Errorf("%v: poster before TMDB answered", tt.Key)
				}
			}
		}()
	}
	wg.Wait()
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for c.posters.flights.Len() > 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := requests.Load(); got != posterLookupsPerPage {
		t.Fatalf("TMDB requests = %d, want %d (one per title, capped per page)", got, posterLookupsPerPage)
	}
	// The next search gets the posters that landed, with no new request.
	p := page()
	c.FillPosters(context.Background(), p, all, time.Second)
	for i, tt := range p[:posterLookupsPerPage] {
		want := fmt.Sprintf("/p%d.jpg", i+1)
		if i+1 == 3 {
			want = ""
		}
		if tt.PosterPath != want {
			t.Errorf("%v poster = %q, want %q", tt.Key, tt.PosterPath, want)
		}
	}
	if got := requests.Load(); got != posterLookupsPerPage+4 {
		t.Fatalf("TMDB requests after the second page = %d, want %d (cached titles are not asked again; the rest of the page is)", got, posterLookupsPerPage+4)
	}
	// Artwork reads them from memory; nothing was stored.
	if mt, stored, err := c.MemoryTitle(context.Background(), core.MovieKey(1)); err != nil || stored || mt == nil || mt.PosterPath != "/p1.jpg" {
		t.Fatalf("MemoryTitle = %+v, stored %v, err %v", mt, stored, err)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_titles`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("catalog_titles rows = %d (err %v), want 0: poster lookups must not store titles", rows, err)
	}
}

// A long-tail search hit whose poster nobody knows is looked up once and
// shows it (on this search when TMDB is quick, else on the next); a hit
// the names file marks as having no poster is never asked about; a hit
// whose date is not known yet still gets artwork; nothing is stored.
func TestTailSearchFillsPosters(t *testing.T) {
	var asked sync.Map
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, _ := asked.LoadOrStore(r.URL.Path, new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
		switch r.URL.Path {
		case "/movie/335984":
			fmt.Fprint(w, `{"id":335984,"title":"Blade Runner 2049","release_date":"2017-10-04","poster_path":"/br2049.jpg"}`)
		case "/search/multi", "/search/movie", "/search/tv":
			fmt.Fprint(w, `{"page":1,"results":[],"total_pages":1,"total_results":0}`)
		default:
			w.WriteHeader(404)
			fmt.Fprint(w, `{"status_message":"not found"}`)
		}
	}))
	defer srv.Close()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := tmdb.New("k", slog.Default(), tmdb.WithBaseURL(srv.URL), tmdb.WithCacheTTL(0))
	defer client.Close()
	c, err := New(db, client, config.Catalog{Language: "en-US", Region: "US"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	c.setTailIndex(testTail(t))
	key := core.MovieKey(335984)
	poster := func() string {
		items, err := c.Search(context.Background(), "blade runner", []core.MediaType{core.TypeMovie}, 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if it.Key == key {
				return it.PosterPath
			}
		}
		t.Fatalf("search did not return %v: %+v", key, items)
		return ""
	}
	got := poster()
	if got == "" { // TMDB missed the deadline; the next search has it
		for deadline := time.Now().Add(5 * time.Second); c.posters.flights.Len() > 0 && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		got = poster()
	}
	if got != "/br2049.jpg" {
		t.Fatalf("poster = %q, want the looked-up /br2049.jpg", got)
	}
	count := func(path string) int64 {
		if n, ok := asked.Load(path); ok {
			return n.(*atomic.Int64).Load()
		}
		return 0
	}
	if n := count("/movie/335984"); n != 1 {
		t.Errorf("lean fetches of the unknown hit = %d, want 1", n)
	}
	if n := count("/movie/78"); n != 0 {
		t.Errorf("the hit marked PosterNone was looked up %d times", n)
	}
	// Artwork: a date-unknown hit with no row is grantable and keeps the
	// poster the lookup found.
	if !c.UndatedSearchHit(context.Background(), core.MovieKey(999)) && !c.UndatedSearchHit(context.Background(), core.ShowKey(2)) {
		t.Error("an export-only tail entry is not an undated search hit")
	}
	arts, err := c.ArtworkTitles(context.Background(), []core.RatingKey{key, core.ShowKey(2)})
	if err != nil || arts[key].PosterPath != "/br2049.jpg" || arts[core.ShowKey(2)].Key != core.ShowKey(2) {
		t.Fatalf("ArtworkTitles = %+v, %v", arts, err)
	}
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM catalog_titles`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("catalog_titles rows = %d (err %v), want 0", rows, err)
	}
}

// A long-tail hit known only by a name the poster fonts cannot set (CJK,
// Thai) shows the localised title the poster lookup returned, and Ether's
// generated poster draws it, in search and in artwork alike.
func TestLookupTitleDrawsPosterForCJKHit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/movie/129":
			fmt.Fprint(w, `{"id":129,"title":"Spirited Away","original_title":"千と千尋の神隠し","release_date":"2001-07-20","poster_path":null}`)
		case "/search/multi", "/search/movie", "/search/tv":
			fmt.Fprint(w, `{"page":1,"results":[],"total_pages":1,"total_results":0}`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	client := tmdb.New("k", slog.Default(), tmdb.WithBaseURL(srv.URL), tmdb.WithCacheTTL(0))
	defer client.Close()
	c, err := New(db, client, config.Catalog{Language: "en-US", Region: "US"}, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	ix, err := buildTailIndex(gzLines(t, `{"adult":false,"id":129,"original_title":"千と千尋の神隠し","popularity":1.2,"video":false}`), gzLines(t), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.setTailIndex(ix)
	key := core.MovieKey(129)
	hit := func() core.Title {
		items, err := c.Search(context.Background(), "千と千尋", []core.MediaType{core.TypeMovie}, 10)
		if err != nil || len(items) == 0 || items[0].Key != key {
			t.Fatalf("search = %+v, %v", items, err)
		}
		return items[0]
	}
	got := hit()
	if got.Title != "Spirited Away" {
		for deadline := time.Now().Add(5 * time.Second); c.posters.flights.Len() > 0 && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
		got = hit()
	}
	if got.Title != "Spirited Away" || got.OriginalTitle != "千と千尋の神隠し" || got.PosterPath != "" {
		t.Fatalf("hit = %q / %q poster %q", got.Title, got.OriginalTitle, got.PosterPath)
	}
	held, _, ok := c.heldTitle(key)
	for _, tt := range []core.Title{got, held} {
		p, isGen := artwork.ForTitle(&tt)
		if !ok || !isGen || p.Request().Title != "Spirited Away" {
			t.Fatalf("generated poster draws %q (ok %v %v), want the localised title", p.Request().Title, ok, isGen)
		}
	}
	pa, _ := artwork.ForTitle(&got)
	pb, _ := artwork.ForTitle(&held)
	if a, b := pa.Stamp(), pb.Stamp(); a != b {
		t.Fatalf("search and artwork disagree on the generated poster: %q vs %q", a, b)
	}
}
