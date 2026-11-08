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
