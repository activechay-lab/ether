package admin_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type browseHelper struct {
	fakeHelper
	queries []core.BrowseQuery
}

func (b *browseHelper) BrowseTitles(_ context.Context, q core.BrowseQuery) (core.Page, error) {
	b.queries = append(b.queries, q)
	return core.Page{Total: 107, Items: []core.Title{{Key: core.MovieKey(7), Type: core.TypeMovie, Title: "Seven", PosterPath: "/seven.jpg"}}}, nil
}

func TestCatalogBrowse(t *testing.T) {
	store := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	helper := &browseHelper{}
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: helper})
	path := "/ether/api/catalog/browse?type=movie&q=Seven&genre=Drama&decade=1990&sort=title&limit=48"
	if rec := serve(h, req(http.MethodGet, path, "")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status %d", rec.Code)
	}
	rec := serve(h, authed(req(http.MethodGet, path, "")))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(helper.queries) != 1 {
		t.Fatalf("queries: %v", helper.queries)
	}
	q := helper.queries[0]
	if q.Filter.Type != core.TypeMovie || q.Filter.Query != "Seven" || len(q.Filter.Genres) != 1 || q.Filter.Genres[0] != "Drama" || q.Filter.YearFrom != 1990 || q.Filter.YearTo != 1999 || q.Sort != core.SortTitle || q.Desc || q.Offset != 0 || q.Limit != 49 || !q.CursorPage || !q.NoTotal {
		t.Fatalf("wrong query: %+v", q)
	}
	var body struct {
		Items []admin.TitleCard `json:"items"`
		Total int               `json:"total"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Total != 1 || len(body.Items) != 1 || body.Items[0].Title != "Seven" || body.Items[0].Poster != admin.ImageURL("/seven.jpg", 342) {
		t.Fatalf("wrong response: %+v", body)
	}
	for _, query := range []string{"offset=48", "offset=999999", "cursor=0&offset=1", "limit=101", "offset=-1", "offset=nope", "limit=0", "decade=1997", "decade=nope", "sort=invalid", "ruleVisibility=invalid", "ruleVisibility=hidden&rules=bad", "ruleVisibility=shown&rules=%5B%5D&ruleFallback=invalid"} {
		t.Run(query, func(t *testing.T) {
			rec := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?"+query, "")))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
	if len(helper.queries) != 1 {
		t.Fatal("invalid requests reached the catalog")
	}
	rec = serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse", "")))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	if q := helper.queries[1]; q.Limit != 49 || q.Offset != 0 || q.Sort != core.SortPopularity || !q.Desc || !q.CursorPage || !q.NoTotal {
		t.Fatalf("bad defaults: %+v", q)
	}
}

func TestCatalogBrowseWaitingFilter(t *testing.T) {
	store := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	helper := &browseHelper{}
	waiting := []core.RatingKey{core.MovieKey(7), core.MovieKey(8)}
	h := admin.NewHandler(admin.Deps{
		Config:             store,
		IsOwnerToken:       storedOwner(store),
		CatalogHelper:      helper,
		WaitingReleaseKeys: func(context.Context) []core.RatingKey { return waiting },
	})
	rec := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?waiting=1", "")))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if len(helper.queries) != 1 {
		t.Fatalf("queries: %v", helper.queries)
	}
	q := helper.queries[0]
	if q.Filter.Type != core.TypeMovie || len(q.Filter.IncludeKeys) != 2 || q.Filter.IncludeKeys[0] != waiting[0] {
		t.Fatalf("waiting query: %+v", q)
	}
	empty := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: helper})
	rec = serve(empty, authed(req(http.MethodGet, "/ether/api/catalog/browse?waiting=1&type=show", "")))
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	q = helper.queries[1]
	if q.Filter.Type != core.TypeMovie || q.Filter.IncludeKeys == nil || len(q.Filter.IncludeKeys) != 0 {
		t.Fatalf("empty waiting: %+v", q)
	}
}

func TestCatalogAdvancedBrowse(t *testing.T) {
	store := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	helper := &browseHelper{}
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: helper})
	filter := `{"type":"movie","minRating":7,"yearFrom":1990,"yearTo":1999,"maxRuntime":7200000000000,"minVotes":500,"group":{"operator":"or","children":[{"filter":{"genres":["Thriller"]}},{"unwatched":true,"userId":3}]}}`
	rec := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?filter="+url.QueryEscape(filter)+"&noTotal=1&secondarySort=title&desc=0", "")))
	if rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	q := helper.queries[0]
	if !q.NoTotal || q.Limit != 49 || q.Desc || !q.Filter.LocalOnly || q.Filter.MaxRuntime != 2*time.Hour || q.Filter.MinVotes != 500 || q.Filter.Group.Children[1].UserID != 3 || len(q.SecondarySorts) != 1 {
		t.Fatalf("query: %+v", q)
	}
	var body struct {
		TotalExact bool `json:"totalExact"`
		HasMore    bool `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TotalExact || body.HasMore {
		t.Fatalf("bad progressive state: %+v", body)
	}
}

func TestCatalogAdvancedBrowseRejectsInvalidExpressions(t *testing.T) {
	store := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	helper := &browseHelper{}
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: helper})
	for _, filter := range []string{
		`{"yearFrom":2000,"yearTo":1990}`, `{"minRating":11}`, `{"maxRuntime":-1}`, `{"type":"episode"}`,
		`{"CatalogCandidates":true}`, `{"group":{"operator":"xor","children":[{}]}}`,
		`{"group":{"unwatched":true}}`, `{"credits":[{"kind":"sql"}]}`, `{"releasedFilters":[{"op":"sql"}]}`,
		`{"group":{"operator":"and","children":[]}}`, `{"group":{"stateField":"viewedLeafCount","stateOperator":">","stateValue":0}}`,
	} {
		t.Run(filter, func(t *testing.T) {
			rec := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?filter="+url.QueryEscape(filter), "")))
			if rec.Code != 400 {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
		})
	}
	if len(helper.queries) != 0 {
		t.Fatal("invalid requests reached SQL")
	}
}
