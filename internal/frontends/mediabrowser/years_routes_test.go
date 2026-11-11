package mediabrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func TestYearsRoutesListAndItem(t *testing.T) {
	exp := frontendtest.Experience{
		FacetsFunc: func(_ context.Context, _ *frontend.Viewer, _ frontend.Device, _ string, kind string) ([]core.FacetValue, error) {
			if kind != "year" {
				t.Fatalf("facet kind %q", kind)
			}
			return []core.FacetValue{{Title: "2020"}, {Title: "2021"}}, nil
		},
	}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) {
		return &frontend.Viewer{ID: 1, Username: "ada"}, nil
	}}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp})
	if err != nil {
		t.Fatal(err)
	}
	call := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-Emby-Token", "grant")
		r.Header.Set("X-Emby-Device-Id", "dev")
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	list := call("/Years")
	var listed struct {
		Items            []map[string]any
		TotalRecordCount int
	}
	if list.Code != 200 || json.Unmarshal(list.Body.Bytes(), &listed) != nil || listed.TotalRecordCount != 2 || len(listed.Items) != 2 {
		t.Fatal(list.Code, list.Body.String())
	}
	if listed.Items[0]["Name"] != "2020" || listed.Items[0]["Type"] != "Year" || listed.Items[0]["ProductionYear"] != float64(2020) {
		t.Fatal(listed.Items[0])
	}
	wantID, _ := ids.GUID(ids.View, 2020)
	if listed.Items[0]["Id"] != wantID {
		t.Fatal(listed.Items[0]["Id"], wantID)
	}
	one := call("/Years/2020")
	var year map[string]any
	if one.Code != 200 || json.Unmarshal(one.Body.Bytes(), &year) != nil || year["Id"] != wantID || year["Name"] != "2020" {
		t.Fatal(one.Code, one.Body.String())
	}
	bad := call("/Years/notayear")
	if bad.Code != 404 {
		t.Fatal(bad.Code, bad.Body.String())
	}
}

func TestUserItemDataRoute(t *testing.T) {
	exp := frontendtest.Experience{
		ItemFunc: func(_ context.Context, req frontend.ItemRequest) (frontend.ItemDetail, error) {
			if req.Key != 5 {
				return frontend.ItemDetail{}, core.ErrNotFound
			}
			return frontend.ItemDetail{Title: frontend.Title{Key: 5, Type: "movie"}}, nil
		},
		UserDataFunc: func(_ context.Context, _ *frontend.Viewer, keys []frontend.RatingKey) (map[frontend.RatingKey]frontend.WatchStatus, error) {
			out := map[frontend.RatingKey]frontend.WatchStatus{}
			for _, k := range keys {
				out[k] = frontend.WatchStatus{ViewCount: 2, IsFavorite: true}
			}
			return out, nil
		},
	}
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) {
		return &frontend.Viewer{ID: 1, Username: "ada"}, nil
	}}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp})
	if err != nil {
		t.Fatal(err)
	}
	call := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-Emby-Token", "grant")
		r.Header.Set("X-Emby-Device-Id", "dev")
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	w := call("/UserItems/" + ids.RatingKeyGUID(5) + "/UserData")
	var data map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &data) != nil || data["Played"] != true || data["IsFavorite"] != true || data["PlayCount"] != float64(2) {
		t.Fatal(w.Code, w.Body.String())
	}
	missing := call("/UserItems/" + ids.RatingKeyGUID(6) + "/UserData")
	if missing.Code != 404 {
		t.Fatal(missing.Code, missing.Body.String())
	}
}
