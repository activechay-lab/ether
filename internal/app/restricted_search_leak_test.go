package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/catalog"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/experience"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/plextv"
	"github.com/braedonsaunders/ether/internal/safety"
	"github.com/braedonsaunders/ether/internal/tmdb"
)

// leakTMDB answers searches with bare summaries: TMDB search results carry
// no certification and no countries, and these carry no genre ids either.
// Details hold the truth.
type leakTMDB struct {
	searchDown
	summaries []tmdb.Movie
	details   map[int64]string // raw TMDB movie details JSON
	down      bool             // details fail (outage, rate limit)
}

func (s leakTMDB) hits(q string) []tmdb.Movie {
	var out []tmdb.Movie
	for _, m := range s.summaries {
		if strings.Contains(strings.ToLower(m.Title), strings.ToLower(q)) {
			out = append(out, m)
		}
	}
	return out
}
func (s leakTMDB) SearchMulti(_ context.Context, q string, _ int) (*tmdb.Paged[tmdb.MultiResult], error) {
	p := &tmdb.Paged[tmdb.MultiResult]{Page: 1, TotalPages: 1}
	for _, m := range s.hits(q) {
		p.Results = append(p.Results, tmdb.MultiResult{ID: m.ID, MediaType: "movie", Title: m.Title, ReleaseDate: m.ReleaseDate, OriginalLanguage: m.OriginalLanguage, Popularity: m.Popularity, VoteAverage: m.VoteAverage, VoteCount: m.VoteCount})
	}
	return p, nil
}
func (s leakTMDB) SearchMovies(_ context.Context, q string, _ int) (*tmdb.Paged[tmdb.Movie], error) {
	return &tmdb.Paged[tmdb.Movie]{Page: 1, TotalPages: 1, Results: s.hits(q)}, nil
}
func (s leakTMDB) SearchTV(context.Context, string, int) (*tmdb.Paged[tmdb.TVShow], error) {
	return &tmdb.Paged[tmdb.TVShow]{Page: 1, TotalPages: 1}, nil
}
func (s leakTMDB) MovieDetails(_ context.Context, id int64) (*tmdb.MovieDetails, error) {
	raw, ok := s.details[id]
	if !ok || s.down {
		return nil, errMetadataDown
	}
	var d tmdb.MovieDetails
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, err
	}
	return &d, nil
}

var _ catalog.TMDB = leakTMDB{}

// TestRestrictedSearchLeak drives the production App (real catalogue, safety
// service, auth, Plex server and mounted Jellyfin front end) with a TMDB whose
// search hits are bare summaries. Each profile gets a fresh App, so its first
// search is the first search the server ever ran. It records whether a title
// the profile must not see appears in search, opens in detail, and passes the
// play gate.
//
// Profile A: MaxContentRating PG (kids preset), BlockedGenres Horror, and a
// custom catalogue rule hiding country KR.
// Profile B: no rating cap; custom catalogue rules only: hide contentRating
// gte R, hide country KR, hide genre Horror.
//
// Titles:
//
//	9101 Quietwater Lullaby  remote-only search hit; details: US R, KR, Horror
//	9102 Quietwater Nocturne remote-only search hit; details also empty
//	9103 Quietwater Hollow   pre-stored undetailed row (no rating/countries/
//	                         genres), not in TMDB search; details: US R, KR, Horror
func TestRestrictedSearchLeak(t *testing.T) {
	got := map[string]bool{}
	for _, mode := range []string{"up", "down"} {
		for _, shape := range []string{"open", "A", "B"} {
			t.Run(mode+"/"+shape, func(t *testing.T) { restrictedLeakProfile(t, mode, shape, got) })
		}
	}
	var leaks []string
	for k, reached := range got {
		parts := strings.Split(k, "/")
		if parts[1] == "open" || !reached {
			continue
		}
		// 9102 is genuinely unrated with no facets. Whether B's rules should
		// hide it once details confirm that is a policy choice; only its
		// search (before details exist) counts.
		if parts[1] == "B" && parts[4] == "Quietwater Nocturne" && parts[3] != "search" {
			continue
		}
		leaks = append(leaks, k)
	}
	sort.Strings(leaks)
	// Mode "up" has working TMDB details; "down" fails every details fetch
	// (outage, rate limit), so the catalogue serves the undetailed row. Before
	// the fail-closed catalogue rules, profile B leaked search, detail and play
	// on both surfaces in "down" and search in "up".
	for _, k := range leaks {
		t.Errorf("restricted profile reached: %s", k)
	}
	// The control profile must find and open everything, or the test proves nothing.
	for _, title := range []string{"Quietwater Lullaby", "Quietwater Nocturne", "Quietwater Hollow"} {
		for _, k := range []string{"up/open/plex/search/", "up/open/jellyfin/search/", "up/open/plex/detail/", "up/open/jellyfin/detail/", "up/open/gate/play/", "down/open/plex/detail/", "down/open/gate/play/"} {
			if !got[k+title] {
				t.Errorf("control: %s%s did not reach the unrestricted viewer", k, title)
			}
		}
	}
}

func restrictedLeakProfile(t *testing.T, mode, shape string, got map[string]bool) {
	a := setupTestApp(t, true)
	ctx := context.Background()
	if err := a.users.SetOwner(ctx, plextv.Account{ID: 42, Username: "owner"}, "owner-token"); err != nil {
		t.Fatal(err)
	}
	released := "2010-05-01"
	truth := func(id int64, title string) string {
		return fmt.Sprintf(`{"id":%d,"title":%q,"release_date":%q,"original_language":"en","vote_count":50,"vote_average":6.1,"popularity":4,
			"genres":[{"id":27,"name":"Horror"}],"production_countries":[{"iso_3166_1":"KR","name":"South Korea"}],
			"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"R","release_date":"2010-05-01T00:00:00.000Z","type":3}]}]}}`, id, title, released)
	}
	bare := func(id int64, title string) string {
		return fmt.Sprintf(`{"id":%d,"title":%q,"release_date":%q,"original_language":"en","vote_count":50,"vote_average":6.1,"popularity":4}`, id, title, released)
	}
	summary := func(id int64, title string) tmdb.Movie {
		return tmdb.Movie{ID: id, Title: title, ReleaseDate: released, OriginalLanguage: "en", VoteCount: 50, VoteAverage: 6.1, Popularity: 4}
	}
	a.catalog.SetTMDB(leakTMDB{
		summaries: []tmdb.Movie{summary(9101, "Quietwater Lullaby"), summary(9102, "Quietwater Nocturne")},
		details: map[int64]string{
			9101: truth(9101, "Quietwater Lullaby"),
			9102: bare(9102, "Quietwater Nocturne"),
			9103: truth(9103, "Quietwater Hollow"),
		},
		down: mode == "down",
	})
	rel := time.Date(2010, 5, 1, 0, 0, 0, 0, time.UTC)
	if err := a.catalog.UpsertTitle(ctx, core.Title{Key: core.MovieKey(9103), Type: core.TypeMovie, IDs: core.IDs{TMDB: 9103}, Title: "Quietwater Hollow", Released: rel, Year: 2010, VoteCount: 50, Rating: 6.1, Popularity: 4}, true); err != nil {
		t.Fatal(err)
	}
	titles := []struct {
		key  core.RatingKey
		name string
	}{{core.MovieKey(9101), "Quietwater Lullaby"}, {core.MovieKey(9102), "Quietwater Nocturne"}, {core.MovieKey(9103), "Quietwater Hollow"}}
	rows := func(when string) {
		for _, ti := range titles {
			rating, detail := "-", -1
			_ = a.db.QueryRowContext(ctx, `SELECT content_rating, detail FROM catalog_titles WHERE key=?`, int64(ti.key)).Scan(&rating, &detail)
			t.Logf("row %-20s content_rating=%q detail=%d (%s)", ti.name, rating, detail, when)
		}
	}

	local, err := identity.NewLocal(a.db)
	if err != nil {
		t.Fatal(err)
	}
	name := "kid" + strings.ToLower(shape)
	id, err := local.CreateViewer(ctx, name, "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err = a.users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	hide := func(field, op string, value any) core.LibraryExclusion {
		raw, _ := json.Marshal(value)
		return core.LibraryExclusion{Action: "hide", MediaType: "mixed", Match: "all", Rules: []core.CollectionRule{{Field: field, Op: op, Value: raw}}}
	}
	switch shape {
	case "A": // kids preset cap, genre block, country rule
		u, err := a.users.ActiveUser(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		u.MaxContentRating = "PG"
		if err = a.users.UpdateUser(ctx, *u); err != nil {
			t.Fatal(err)
		}
		if err = a.safety.Put(ctx, id, safety.Policy{BlockedGenres: []string{"Horror"}, BlockedKeywords: []string{"pornography", "erotic movie"}}); err != nil {
			t.Fatal(err)
		}
		if err = a.catalog.PutViewerRules(ctx, id, catalog.ViewerRules{Mode: "custom", Exclusions: []core.LibraryExclusion{hide("country", "in", []string{"KR"})}}); err != nil {
			t.Fatal(err)
		}
	case "B": // catalogue visibility rules only
		if err = a.catalog.PutViewerRules(ctx, id, catalog.ViewerRules{Mode: "custom", Exclusions: []core.LibraryExclusion{
			hide("contentRating", "gte", "R"), hide("country", "in", []string{"KR"}), hide("genre", "in", []string{"Horror"}),
		}}); err != nil {
			t.Fatal(err)
		}
	}

	plexInst, ok := a.frontends.registry.Instance("plex")
	if !ok {
		t.Fatal("plex server unavailable")
	}
	plexHandler := plexInst.Handler()
	inst, err := a.frontends.registry.Mount(ctx, core.Protocol("jellyfin"))
	if err != nil {
		t.Fatal(err)
	}
	defer a.frontends.registry.Unmount(ctx, core.Protocol("jellyfin"))
	plexToken, err := a.users.IssueDelegation(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	plexCall := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.168.1.20:1234"
		r.Header.Set("X-Plex-Token", plexToken)
		r.Header.Set("X-Plex-Client-Identifier", "leak-test")
		r.Header.Set("X-Plex-Product", "Plex Web")
		r.Header.Set("Accept", "application/json")
		plexHandler.ServeHTTP(w, r)
		return w
	}
	var jfToken string
	jfCall := func(method, path, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.RemoteAddr = "192.168.1.20:1234"
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Authorization", `MediaBrowser Client="TV", Device="tv", DeviceId="leak-tv", Version="1", Token="`+jfToken+`"`)
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	login := jfCall("POST", "/Users/AuthenticateByName", `{"Username":"`+name+`","Pw":"password-1234"}`)
	if login.Code != 200 {
		t.Fatalf("login %d %s", login.Code, login.Body)
	}
	var signed struct {
		AccessToken string
		User        struct {
			ID string `json:"Id"`
		}
	}
	if err = json.Unmarshal(login.Body.Bytes(), &signed); err != nil {
		t.Fatal(err)
	}
	jfToken = signed.AccessToken
	uid := signed.User.ID

	record := func(surface, phase, title string, reached bool, note string) {
		k := mode + "/" + shape + "/" + surface + "/" + phase + "/" + title
		got[k] = got[k] || reached
		t.Logf("%-4s %-8s %-12s %-20s reached=%-5v %s", shape, surface, phase, title, reached, note)
	}
	search := func(phase string) {
		// Twice: a confident local match answers the first search without
		// waiting for TMDB, whose results join the mirror for the next one.
		for attempt := 1; attempt <= 2; attempt++ {
			w := jfCall("GET", "/Items?searchTerm=Quietwater&Recursive=true&IncludeItemTypes=Movie,Series&Limit=20&userId="+uid, "")
			for _, ti := range titles {
				record("jellyfin", phase, ti.name, w.Code == 200 && strings.Contains(w.Body.String(), ti.name), fmt.Sprintf("attempt=%d code=%d", attempt, w.Code))
			}
			rows(fmt.Sprintf("after jellyfin %s %d", phase, attempt))
			w = plexCall("/hubs/search?query=Quietwater&limit=20")
			for _, ti := range titles {
				record("plex", phase, ti.name, w.Code == 200 && strings.Contains(w.Body.String(), ti.name), fmt.Sprintf("attempt=%d code=%d", attempt, w.Code))
			}
			rows(fmt.Sprintf("after plex %s %d", phase, attempt))
			time.Sleep(200 * time.Millisecond) // let the detached TMDB search land
		}
	}
	search("search")
	for _, ti := range titles {
		w := jfCall("GET", "/Users/"+uid+"/Items/"+ids.RatingKeyGUID(ti.key), "")
		record("jellyfin", "detail", ti.name, w.Code == 200 && strings.Contains(w.Body.String(), ti.name), fmt.Sprintf("code=%d", w.Code))
		w = plexCall("/library/metadata/" + ti.key.String())
		record("plex", "detail", ti.name, w.Code == 200 && strings.Contains(w.Body.String(), ti.name), fmt.Sprintf("code=%d", w.Code))
	}
	rows("after detail")
	// Play. Plex's decision endpoint runs playableTitle before any source
	// work (a pass reaches "no sources"); Jellyfin PlaybackInfo is logged.
	// The production predicate is evaluated directly as the "gate": safety
	// ConfirmPlayback (viewer facts only, no title), then TitleAllowed with
	// the safety service on the title the catalogue serves.
	u, err := a.users.ActiveUser(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	_, confirmErr := a.safety.ConfirmPlayback(ctx, u, false, time.Now())
	clip := func(s string) string {
		if len(s) > 140 {
			return s[:140]
		}
		return s
	}
	for _, ti := range titles {
		w := plexCall("/video/:/transcode/universal/decision?" + url.Values{"path": {"/library/metadata/" + ti.key.String()}, "mediaIndex": {"0"}, "partIndex": {"0"}, "protocol": {"hls"}, "directPlay": {"0"}, "directStream": {"1"}}.Encode())
		t.Logf("%-4s plex     play-http    %-20s code=%d %q", shape, ti.name, w.Code, clip(w.Body.String()))
		w = jfCall("POST", "/Items/"+ids.RatingKeyGUID(ti.key)+"/PlaybackInfo?UserId="+uid, `{}`)
		t.Logf("%-4s jellyfin play-http    %-20s code=%d %q", shape, ti.name, w.Code, clip(w.Body.String()))
		served, getErr := a.catalog.Get(ctx, ti.key)
		allowed := getErr == nil && confirmErr == nil && experience.TitleAllowed(a.safety, u, served)
		rating := ""
		if served != nil {
			rating = served.ContentRating
		}
		record("gate", "play", ti.name, allowed, fmt.Sprintf("confirm=%v get=%v rating=%q", confirmErr, getErr, rating))
	}
	search("search-after")
}
