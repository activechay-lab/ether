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
	first := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?cursor=1&limit=2", "")))
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
	second := serve(h, authed(req(http.MethodGet, "/ether/api/catalog/browse?cursor=1&limit=2&after="+url.QueryEscape(body.NextCursor), "")))
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
