package plex

import (
	"context"
	"encoding/json"
	"fmt"
	conformplex "github.com/braedonsaunders/ether/internal/conform/plex"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/plex/model"
)

// Every partition of a canonical row must reassemble exactly that row, even
// when another screen publishes a new generation between arbitrary requests.
// Inputs deliberately include mixed types, repeated source keys, different
// page sizes, retries, out-of-order pages, expiry and daypart changes.
func TestShelfGenerationPagingInvariant(t *testing.T) {
	for _, collection := range []bool{true, false} {
		for _, evidence := range []bool{false, true} {
			t.Run(fmt.Sprintf("collection=%t/evidence=%t", collection, evidence), func(t *testing.T) {
				shelfGenerationPagingInvariant(t, collection, evidence)
			})
		}
	}
}
func shelfGenerationPagingInvariant(t *testing.T, collection, evidence bool) {
	f := newFixture()
	defer f.srv.Close()
	now := fixedNow
	f.srv.d.Now = func() time.Time { return now }
	var mixed []core.Title
	for i := 0; i < 140; i++ {
		movie, show := fightClub, breakingBad
		movie.Key = core.MovieKey(int64(10000 + i))
		show.Key = core.ShowKey(int64(10000 + i))
		mixed = append(mixed, movie, show)
		f.catalog.titles[movie.Key] = movie
		f.catalog.titles[show.Key] = show
	}
	// Duplicates must disappear before offsets/counts, not per page.
	mixed = append(mixed[:30], append([]core.Title{mixed[4], mixed[4]}, mixed[30:]...)...)
	key := core.RatingKey(0)
	if collection {
		key = core.CollectionKey(123)
	}
	rec := &fakeRecommender{section: map[core.MediaType][]core.Hub{}}
	for _, typ := range []core.MediaType{core.TypeMovie, core.TypeShow} {
		rec.section[typ] = []core.Hub{{ID: "collection.mixed", Key: key, Title: "Mixed", Items: mixed}}
	}
	f.srv.d.Recommender = rec
	headers := map[string]string{"Accept": "application/json"}
	for k, vs := range conformplex.Clients()["web"].Headers {
		if k != "X-Plex-Token" {
			headers[k] = vs[0]
		}
	}
	get := func(path string) model.MediaContainer {
		t.Helper()
		response := f.request(t, "GET", path, headers)
		if response.Code != 200 {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
		var body struct{ MediaContainer model.MediaContainer }
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body.MediaContainer
	}
	row := func(section int) model.Hub {
		t.Helper()
		for _, h := range get(fmt.Sprintf("/hubs/sections/%d", section)).Hub {
			if h.HubIdentifier == "collection.mixed" {
				return h
			}
		}
		t.Fatal("mixed row missing")
		return model.Hub{}
	}
	keys := func(items []model.Metadata) []string {
		out := []string{}
		for _, item := range items {
			out = append(out, item.RatingKey)
		}
		return out
	}
	initial := []model.Hub{row(1), row(2)}
	for section, h := range initial {
		expected := []string{}
		for i := 0; i < 140; i++ {
			k := core.MovieKey(int64(10000 + i))
			if section == 1 {
				k = core.ShowKey(int64(10000 + i))
			}
			expected = append(expected, k.String())
		}
		if !reflect.DeepEqual(keys(h.Metadata), expected[:h.Size]) || h.Size != 12 || !h.More {
			t.Fatalf("initial section %d: size=%d more=%v keys=%v", section, h.Size, h.More, keys(h.Metadata))
		}
		if got := get(h.HubKey); !reflect.DeepEqual(keys(got.Metadata), expected[:h.Size]) {
			t.Fatal("hubKey changed visible subset")
		}
		for seed := int64(0); seed < 12; seed++ {
			rng := rand.New(rand.NewSource(seed))
			type page struct{ start, size int }
			pages := []page{}
			for start := h.Size; start < len(expected); {
				size := min(1+rng.Intn(50), len(expected)-start)
				pages = append(pages, page{start, size})
				start += size
			}
			rng.Shuffle(len(pages), func(i, j int) { pages[i], pages[j] = pages[j], pages[i] })
			assembled := append([]string(nil), expected[:h.Size]...)
			assembled = append(assembled, make([]string, len(expected)-h.Size)...)
			for _, p := range pages {
				now = now.Add(6 * time.Hour) // expires presentation/shelf caches AND crosses dayparts
				// New evidence changes discovery eligibility while the old generation
				// remains complete; section switching can publish the new answer.
				if evidence {
					f.srv.d.FilterAvailable = func(_ context.Context, titles []core.Title) []core.Title {
						out := []core.Title{}
						for _, title := range titles {
							if title.Key != mixed[0].Key {
								out = append(out, title)
							}
						}
						return out
					}
				}
				// Change membership and order, then switch sections and publish new screens.
				for typ, rows := range rec.section {
					items := append([]core.Title(nil), mixed...)
					if p.start%2 == 0 {
						for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
							items[i], items[j] = items[j], items[i]
						}
					}
					rows[0].Items = items
					rec.section[typ] = rows
				}
				row(2 - section)
				row(section + 1)
				u, _ := url.Parse(h.Key)
				q := u.Query()
				q.Set("X-Plex-Container-Start", fmt.Sprint(p.start))
				q.Set("X-Plex-Container-Size", fmt.Sprint(p.size))
				u.RawQuery = q.Encode()
				got := get(u.String())
				if got.Size != p.size || got.TotalSize == nil || *got.TotalSize != len(expected) || !reflect.DeepEqual(keys(got.Metadata), expected[p.start:p.start+p.size]) {
					t.Fatalf("seed=%d section=%d page=%+v size=%d total=%v keys=%v", seed, section, p, got.Size, got.TotalSize, keys(got.Metadata))
				}
				copy(assembled[p.start:], keys(got.Metadata))
				// Retry the exact page: it must be idempotent.
				if retry := get(u.String()); !reflect.DeepEqual(keys(retry.Metadata), keys(got.Metadata)) {
					t.Fatal("retry shifted")
				}
			}
			if !reflect.DeepEqual(assembled, expected) {
				t.Fatal("duplicate or gap in page union")
			}
		}
	}
	// A generation is not authorization; changing clients must fail closed.
	if response := f.request(t, "GET", initial[0].Key, map[string]string{"X-Plex-Client-Identifier": "another-client"}); response.Code != 404 {
		t.Fatalf("cross-client generation: %d", response.Code)
	}
	// Memory eviction/restart never silently starts a new listing at an old offset.
	f.srv.shelfMu.Lock()
	f.srv.shelfGenerations = nil
	f.srv.shelfMu.Unlock()
	if response := f.request(t, "GET", initial[0].Key, headers); response.Code != http.StatusGone {
		t.Fatalf("lost generation: %d", response.Code)
	}
}

func TestBrowseScopeOmitsPagingAndCredentials(t *testing.T) {
	pr := &Request{ClientID: "client-one"}
	first := httptest.NewRequest("GET", "/library/sections/1/all?sort=titleSort&X-Plex-Container-Start=0&X-Plex-Container-Size=50&X-Plex-Token=secret", nil)
	next := httptest.NewRequest("GET", "/library/sections/1/all?sort=titleSort&X-Plex-Container-Start=50&X-Plex-Container-Size=100", nil)
	a, b := browseScope(first, pr), browseScope(next, pr)
	if a != b || strings.Contains(a, "secret") {
		t.Fatalf("unstable/credential-bearing scope: %q %q", a, b)
	}
	next.URL.RawQuery = "sort=addedAt"
	if a == browseScope(next, pr) {
		t.Fatal("sorts shared a sequence")
	}
}
