package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// searchHits answers every search with titles the catalogue has not stored
// and TMDB has no poster for (so Ether draws one: no network needed).
type searchHits struct{ searchDown }

func (searchHits) hits(query string) []tmdb.MultiResult {
	var out []tmdb.MultiResult
	for i := int64(1); i <= 6; i++ {
		out = append(out, tmdb.MultiResult{ID: 7000 + i + int64(len(query))*10, MediaType: "movie", Title: query + " " + fmt.Sprint(i), ReleaseDate: "2001-02-03", Popularity: 3, VoteCount: 20})
	}
	return out
}
func (s searchHits) SearchMulti(_ context.Context, q string, _ int) (*tmdb.Paged[tmdb.MultiResult], error) {
	r := s.hits(q)
	return &tmdb.Paged[tmdb.MultiResult]{Page: 1, TotalPages: 1, TotalResults: len(r), Results: r}, nil
}
func (s searchHits) SearchMovies(_ context.Context, q string, _ int) (*tmdb.Paged[tmdb.Movie], error) {
	var out []tmdb.Movie
	for _, r := range s.hits(q) {
		out = append(out, r.AsMovie())
	}
	return &tmdb.Paged[tmdb.Movie]{Page: 1, TotalPages: 1, TotalResults: len(out), Results: out}, nil
}

// Owner rule: a search page writes nothing. N searches on a grant frontend
// show every hit with a poster (Ether's, drawn and hashed) and leave the
// catalogue, the artwork metadata and the UltraBlur tables as they were;
// the hashes of row-less hits live in memory and are served from there.
func TestSearchPagesWriteNoRows(t *testing.T) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	owner, err := a.users.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	if err = local.Create(ctx, owner.ID, "viewer", "password-1234"); err != nil {
		t.Fatal(err)
	}
	a.catalog.SetTMDB(searchHits{})
	counts := func() map[string]int {
		out := map[string]int{}
		for _, table := range []string{"catalog_titles", "artwork_image_metadata", "artwork_image_metadata_claims", "plex_ultrablur"} {
			var n int
			if err := a.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
				t.Fatal(err)
			}
			out[table] = n
		}
		return out
	}
	before := counts()
	inst, err := a.frontends.registry.Mount(ctx, core.Protocol("jellyfin"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.frontends.registry.Unmount(ctx, core.Protocol("jellyfin"))
	call := func(path, body, token string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		method := "GET"
		if body != "" {
			method = "POST"
		}
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "192.0.2.20:1234"
		r.Header.Set("Authorization", `MediaBrowser Client="TV", DeviceId="tv", Token="`+token+`"`)
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	login := call("/Users/AuthenticateByName", `{"Username":"viewer","Pw":"password-1234"}`, "")
	var signed struct {
		AccessToken string
		User        struct {
			ID string `json:"Id"`
		}
	}
	if login.Code != 200 || json.Unmarshal(login.Body.Bytes(), &signed) != nil {
		t.Fatalf("login %d %s", login.Code, login.Body)
	}
	queries := []string{"quiet harbour", "northern lights", "glass river", "paper moon", "salt road"}
	var shown []core.Title
	for _, q := range queries {
		hints := call("/Search/Hints?searchTerm="+url.QueryEscape(q)+"&Limit=20", "", signed.AccessToken)
		var page struct {
			SearchHints []struct {
				Name            string
				PrimaryImageTag string
			}
		}
		if hints.Code != 200 || json.Unmarshal(hints.Body.Bytes(), &page) != nil || len(page.SearchHints) == 0 {
			t.Fatalf("%q hints %d %s", q, hints.Code, hints.Body)
		}
		for _, h := range page.SearchHints {
			if h.PrimaryImageTag == "" {
				t.Errorf("%q: hit %q has no poster", q, h.Name)
			}
		}
		items := call("/Users/"+signed.User.ID+"/Items?searchTerm="+url.QueryEscape(q)+"&Recursive=true&IncludeItemTypes=Movie&Fields=PrimaryImageAspectRatio", "", signed.AccessToken)
		if items.Code != 200 || !strings.Contains(items.Body.String(), `"Primary"`) {
			t.Fatalf("%q items %d %s", q, items.Code, items.Body)
		}
		for _, r := range (searchHits{}).hits(q) {
			shown = append(shown, core.Title{Key: core.MovieKey(r.ID), Type: core.TypeMovie, Title: r.Title})
		}
	}
	// Hash every shown poster as the metadata workers would, and wait.
	sctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := a.artwork.StoreMetadata(sctx, owner, shown); err != nil {
		t.Fatal(err)
	}
	// The hashes are served from memory.
	for _, title := range shown[:6] {
		images, _, err := a.artwork.List(ctx, frontend.ArtworkRequest{Viewer: owner, Item: title.Key})
		if err != nil || len(images) == 0 || images[0].BlurHash == "" {
			t.Fatalf("%v: images %+v, %v: want a poster with its hash", title.Key, images, err)
		}
	}
	if after := counts(); fmt.Sprint(after) != fmt.Sprint(before) {
		t.Fatalf("searches wrote rows: before %v, after %v", before, after)
	}
}
