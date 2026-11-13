package plex

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/livetv"
	"github.com/braedonsaunders/ether/internal/store"
)

func TestLiveGridVirtualChannels(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	service, err := livetv.New(livetv.Options{DB: db, Now: func() time.Time { return time.Date(2026, 11, 13, 2, 43, 24, 0, time.UTC) }})
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	f := newFixture()
	defer f.srv.Close()
	f.srv.setTestNow(func() time.Time { return time.Date(2026, 11, 13, 2, 43, 24, 0, time.UTC) })
	f.srv.setLiveTV(service.Frontend())
	f.srv.setTestSubscription(true)
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("virtual-%d", i)
		_, err := service.SaveChannel(ctx, livetv.Channel{ID: id, Name: id, Number: fmt.Sprint(i + 1), Kind: "virtual", Enabled: true, Epoch: fixedNow.Add(-time.Hour), Items: []livetv.Item{{Key: core.MovieKey(1), Title: "Virtual programme", DurationMS: 7200000}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	future := time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)
	comparison := "?" + url.Values{"beginsAt>": {fmt.Sprint(future.Unix())}, "endsAt<": {fmt.Sprint(future.Add(time.Hour).Unix())}, "channelGridKey": {"virtual-0"}}.Encode()
	for _, query := range []string{comparison, "", "?date=2026-11-13", "?date=2026-11-13&channelGridKey=virtual-0", "?date=2026-11-13&channelGridKey=virtual-0,virtual-1,virtual-2,virtual-3", "?date=2026-11-13&channelGridKey=virtual-0&channelGridKey=virtual-1"} {
		res := f.request(t, "GET", liveProviderPath+"/grid"+query, map[string]string{"Accept": "application/json", "X-Plex-Product": "Plex for Apple TV"})
		if res.Code != 200 {
			t.Fatalf("%s: %d %s", query, res.Code, res.Body.String())
		}
		mc := decodeJSON(t, res)
		if mc["size"].(float64) == 0 {
			t.Fatalf("%s: empty grid: %s", query, res.Body.String())
		}
		if query == comparison {
			for _, raw := range mc["Metadata"].([]any) {
				media := raw.(map[string]any)["Media"].([]any)[0].(map[string]any)
				if media["endsAt"].(float64) <= float64(future.Unix()) || media["beginsAt"].(float64) >= float64(future.Add(time.Hour).Unix()) {
					t.Fatalf("comparison window ignored: %s", res.Body.String())
				}
			}
		}
		if strings.Contains(query, "virtual-1") {
			found := map[string]bool{}
			for _, raw := range mc["Metadata"].([]any) {
				md := raw.(map[string]any)
				media := md["Media"].([]any)[0].(map[string]any)
				found[media["channelIdentifier"].(string)] = true
			}
			want := 2
			if strings.Contains(query, "virtual-3") {
				want = 4
			}
			if len(found) != want {
				t.Errorf("%s: got %d channels, want %d", query, len(found), want)
			}
		}
	}
}
