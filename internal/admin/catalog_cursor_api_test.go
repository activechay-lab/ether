package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/braedonsaunders/ether/internal/admin"
	"github.com/braedonsaunders/ether/internal/config"
	"github.com/braedonsaunders/ether/internal/core"
)

type cursorBrowseHelper struct {
	fakeHelper
	queries []core.BrowseQuery
}

func (b *cursorBrowseHelper) BrowseTitles(_ context.Context, q core.BrowseQuery) (core.Page, error) {
	b.queries = append(b.queries, q)
	items := make([]core.Title, q.Limit)
	for i := range items {
		items[i] = core.Title{Key: core.MovieKey(int64(10 + i)), Type: core.TypeMovie, Title: fmt.Sprint(i)}
	}
	return core.Page{Items: items, Total: len(items)}, nil
}
func TestCatalogCursorEndpointUsesReturnedBoundaryAndRejectsChangedFilters(t *testing.T) {
	store := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	helper := &cursorBrowseHelper{}
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: helper})
	first := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?limit=2", "")))
	if first.Code != 200 {
		t.Fatalf("%d %s", first.Code, first.Body)
	}
	var body struct {
		Items      []admin.TitleCard `json:"items"`
		NextCursor string            `json:"nextCursor"`
		HasMore    bool              `json:"hasMore"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 2 || body.NextCursor == "" || !body.HasMore {
		t.Fatalf("%+v", body)
	}
	second := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?limit=2&after="+url.QueryEscape(body.NextCursor), "")))
	if second.Code != 200 {
		t.Fatalf("%d %s", second.Code, second.Body)
	}
	if q := helper.queries[1]; !q.CursorPage || !q.NoTotal || q.AfterKey != core.MovieKey(11) || q.Offset != 0 || q.Limit != 3 {
		t.Fatalf("wrong next boundary %+v", q)
	}
	for _, query := range []string{"cursor=1&offset=99999", "cursor=1&type=show&after=" + url.QueryEscape(body.NextCursor)} {
		response := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?"+query, "")))
		if response.Code != 400 {
			t.Fatalf("%s: %d %s", query, response.Code, response.Body)
		}
	}
	if len(helper.queries) != 2 {
		t.Fatal("invalid cursor reached catalogue")
	}
}

// Counts have a separate progressive contract and never fall through to Browse.
type progressiveCursorHelper struct {
	cursorBrowseHelper
	countCalls int
}

func (b *progressiveCursorHelper) ConsoleCount(context.Context, core.Filter) (int, bool, error) {
	b.countCalls++
	return 0, true, nil
}
func TestCatalogDefaultsStayBoundedAndCountsNeverFallThrough(t *testing.T) {
	store := newStore(t, func(c *config.Config) { seededConfig(c); c.SetupComplete = true })
	helper := &progressiveCursorHelper{}
	h := admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: helper})
	response := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?noTotal=0&cursor=0&limit=2", "")))
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	if q := helper.queries[0]; !q.CursorPage || !q.NoTotal || q.Offset != 0 || q.Limit != 3 {
		t.Fatalf("caller escaped bounded defaults: %+v", q)
	}
	response = serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?countOnly=1", "")))
	if response.Code != 200 {
		t.Fatalf("%d %s", response.Code, response.Body)
	}
	var count struct {
		Pending bool `json:"pending"`
		Exact   bool `json:"totalExact"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &count); err != nil {
		t.Fatal(err)
	}
	if !count.Pending || count.Exact || helper.countCalls != 1 || len(helper.queries) != 1 {
		t.Fatalf("count did not remain progressive: %+v calls%d browse%d", count, helper.countCalls, len(helper.queries))
	}
	unavailable := &cursorBrowseHelper{}
	h = admin.NewHandler(admin.Deps{Config: store, IsOwnerToken: storedOwner(store), CatalogHelper: unavailable})
	response = serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?countOnly=1", "")))
	if response.Code != 503 || len(unavailable.queries) != 0 {
		t.Fatalf("unsupported count fell through to browse: %d, queries%d", response.Code, len(unavailable.queries))
	}
}
