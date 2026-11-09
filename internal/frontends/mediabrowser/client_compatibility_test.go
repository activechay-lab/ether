package mediabrowser

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
)

func TestStopEncodingUsesAuthenticatedDeviceScope(t *testing.T) {
	viewer := &frontend.Viewer{ID: 7}
	stopped := 0
	inst, err := (Jellyfin{MachineID: "test"}).Mount(context.Background(), frontend.Host{
		Identity: frontendtest.Identity{ResolveFunc: func(context.Context, string) (*frontend.Viewer, error) { return viewer, nil }},
		Sessions: frontendtest.Sessions{StopFunc: func(_ context.Context, access frontend.SessionAccess, id string) error {
			if access.Viewer != viewer || access.Device.ID != "tv" || access.Protocol != "jellyfin" || access.Owner || id != "session" {
				t.Fatalf("scope %+v id %s", access, id)
			}
			stopped++
			return nil
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"?deviceId=other&playSessionId=session", 403},
		{"?deviceId=tv", 400},
		{"?deviceId=tv&playSessionId=session", 204},
	} {
		r := httptest.NewRequest("DELETE", "/jellyfin/Videos/ActiveEncodings"+tc.query, nil)
		r.Header.Set("X-Emby-Token", "token")
		r.Header.Set("X-Emby-Device-Id", "tv")
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		if w.Code != tc.status {
			t.Fatalf("%s: %d %s", tc.query, w.Code, w.Body)
		}
	}
	if stopped != 1 {
		t.Fatalf("stopped %d sessions", stopped)
	}
}

func TestEmbyHomeSectionsAndWakeOnLanInfo(t *testing.T) {
	viewer := &frontend.Viewer{ID: 1}
	identity := frontendtest.Identity{ResolveFunc: func(_ context.Context, token string) (*frontend.Viewer, error) {
		if token != "valid" {
			return nil, errors.New("denied")
		}
		return viewer, nil
	}}
	experience := frontendtest.Experience{ViewsFunc: func(_ context.Context, v *frontend.Viewer) ([]frontend.View, error) {
		if v != viewer {
			t.Fatal("wrong viewer")
		}
		return []frontend.View{{ID: "1", Name: "Movies", Type: "movie"}, {ID: "2", Name: "TV", Type: "show"}, {ID: "3", Name: "Music", Type: "music"}}, nil
	}}
	inst, err := (Emby{MachineID: "test"}).Mount(context.Background(), frontend.Host{Identity: identity, Experience: experience})
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(context.Background())
	call := func(path, token string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if token != "" {
			r.Header.Set("X-Emby-Token", token)
		}
		w := httptest.NewRecorder()
		inst.Handler().ServeHTTP(w, r)
		return w
	}
	uid, _ := ids.GUID(ids.User, 1)
	other, _ := ids.GUID(ids.User, 2)
	movies, _ := ids.GUID(ids.View, 1)
	shows, _ := ids.GUID(ids.View, 2)

	w := call("/Users/"+uid+"/HomeSections", "valid")
	if w.Code != http.StatusOK {
		t.Fatalf("HomeSections: %d %s", w.Code, w.Body)
	}
	var result struct {
		Items []struct {
			Id           string
			SectionType  string
			CollectionId string
		}
		TotalRecordCount int
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	want := []struct{ sectionType, collectionID string }{
		{"SmallLibraryTiles", ""}, {"Resume", ""}, {"NextUp", ""},
		{"LatestMedia", movies}, {"LatestMedia", shows},
	}
	if result.TotalRecordCount != len(want) || len(result.Items) != len(want) {
		t.Fatalf("sections %+v", result)
	}
	seenIDs := map[string]bool{}
	for n, item := range result.Items {
		if item.SectionType != want[n].sectionType || item.CollectionId != want[n].collectionID {
			t.Fatalf("section %d: %+v", n, item)
		}
		if len(item.Id) != 32 || seenIDs[item.Id] {
			t.Fatalf("section %d id %q", n, item.Id)
		}
		seenIDs[item.Id] = true
	}
	if w := call("/Users/"+other+"/HomeSections", "valid"); w.Code != http.StatusUnauthorized {
		t.Fatalf("other user HomeSections: %d %s", w.Code, w.Body)
	}
	if w := call("/Users/"+uid+"/HomeSections", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated HomeSections: %d %s", w.Code, w.Body)
	}

	if w := call("/System/WakeOnLanInfo", "valid"); w.Code != http.StatusOK || (w.Body.String() != "[]\n" && w.Body.String() != "[]") {
		t.Fatalf("WakeOnLanInfo: %d %q", w.Code, w.Body.String())
	}
	if w := call("/System/WakeOnLanInfo", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated WakeOnLanInfo: %d %s", w.Code, w.Body)
	}
}
