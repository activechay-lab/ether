package plex

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	watchstate "github.com/braedonsaunders/ether/internal/state"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestHistoryIdentityProjectionAndDelete(t *testing.T) {
	f := newFixture()
	defer f.srv.Close()
	db, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	state := watchstate.New(db, watchstate.Options{Now: func() time.Time { return fixedNow }})
	f.srv.d.State = state
	ctx := watchstate.WithHistoryTitle(watchstate.WithOrigin(context.Background(), watchstate.Origin{DeviceID: 9}), &fightClub)
	if err := state.Scrobble(ctx, ownerUser.ID, fightClub.Key); err != nil {
		t.Fatal(err)
	}
	rows, err := state.History(ctx, ownerUser.ID, time.Time{}, 10)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows %+v %v", rows, err)
	}
	path := "/status/sessions/history/" + strconv.FormatInt(rows[0].ID, 10)
	for _, test := range []struct {
		path     string
		status   int
		contains bool
	}{
		{"/status/sessions/history/all", 200, false},
		{"/status/sessions/history/all?type=1", 200, true},
		{"/status/sessions/history/all?type=1&deviceID=9", 200, true},
		{"/status/sessions/history/all?type=1&deviceID=8", 200, false},
		{"/status/sessions/history/all?type=1&sort=invalid", 400, false},
		{"/status/sessions/history/all?offset=2147483648", 400, false},
		{path, 200, true},
	} {
		r := f.get(t, test.path, "Accept", "application/json")
		if r.Code != test.status || strings.Contains(r.Body.String(), fightClub.Title) != test.contains {
			t.Fatalf("%s: %d %s", test.path, r.Code, r.Body.String())
		}
	}
	single := f.get(t, path, "Accept", "application/json")
	for _, field := range []string{`"key":`, `"ratingKey":`, `"thumb":`} {
		if strings.Contains(single.Body.String(), field) {
			t.Fatalf("single links: %s", single.Body.String())
		}
	}
	if rec := f.request(t, http.MethodDelete, path, map[string]string{"X-Plex-Token": friendToken}); rec.Code != 404 {
		t.Fatalf("cross-user deletion %d", rec.Code)
	}
	delete(f.catalog.titles, fightClub.Key)
	rec := f.get(t, "/status/sessions/history/all?type=1", "Accept", "application/json")
	if !strings.Contains(rec.Body.String(), fightClub.Title) || strings.Contains(rec.Body.String(), `"ratingKey":`) {
		t.Fatalf("deleted item projection %s", rec.Body.String())
	}
	if rec := f.request(t, http.MethodDelete, path, nil); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.get(t, path); rec.Code != 404 {
		t.Fatalf("deleted read: %d", rec.Code)
	}
	_ = core.TypeMovie
}
