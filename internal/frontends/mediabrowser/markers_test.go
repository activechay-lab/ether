package mediabrowser

import (
	"context"
	"github.com/braedonsaunders/ether/internal/conform/openapi/spectest"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMediaSegments(t *testing.T) {
	ident := frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return &frontend.Viewer{ID: 1}, nil }}
	exp := frontendtest.Experience{ItemFunc: func(_ context.Context, r frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Markers: []frontend.Marker{{Type: "intro", Start: time.Second, End: 2 * time.Second}, {Type: "credits", Start: 3 * time.Second, End: 4 * time.Second}}}, nil
	}}
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: ident, Experience: exp})
	if err != nil {
		t.Fatal(err)
	}
	path := "/MediaSegments/" + ids.RatingKeyGUID(5) + "?includeSegmentTypes=Intro"
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("X-Emby-Token", "grant")
	inst.Handler().ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"Type":"Intro"`) || strings.Contains(w.Body.String(), `"Type":"Outro"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	doc := spectest.Document(t, "jellyfin")
	if doc == nil {
		return
	}
	if err := doc.Validate("GET", path, w.Code, w.Header().Get("Content-Type"), w.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
}
