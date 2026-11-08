package stats

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/store"
)

func TestSourceHistoryDebridDeltasAndWindow(t *testing.T) {
	ctx := context.Background()
	db, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	s := New(db, Options{Now: func() time.Time { return now }})
	for _, n := range []int64{10, 25, 5} { // 5 is a restart: counts from zero again
		if err := s.SnapshotDebrid(ctx, []DebridSample{{ID: "rd", Requests: n, Cached: n / 5}}); err != nil {
			t.Fatal(err)
		}
		if err := s.SnapshotStream(ctx, StreamSample{MemoryHits: n, BlocksFetched: 2 * n}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO stats_provider_days(provider,day,bytes,articles,misses,errors) VALUES('old','2026-09-01',1,1,0,0),('p','2026-11-07',100,4,1,0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO stats_plays(id,user_id,rating_key,username,title,platform,device,decision,started,updated,failovers,origin) VALUES('play',1,1,'u','t','p','d','directplay',?,?,1,'Debrid')`, now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	h, err := s.SourceHistoryFor(ctx, 30)
	if err != nil {
		t.Fatal(err)
	}
	if h.Since != "2026-10-10" || h.Until != "2026-11-08" {
		t.Fatalf("window %s..%s", h.Since, h.Until)
	}
	if len(h.DebridDays) != 1 || h.DebridDays[0].Requests != 30 || h.DebridDays[0].Cached != 6 {
		t.Fatalf("debrid days %+v", h.DebridDays)
	}
	if len(h.StreamDays) != 1 || h.StreamDays[0].MemoryHits != 30 || h.StreamDays[0].BlocksFetched != 60 {
		t.Fatalf("stream days %+v", h.StreamDays)
	}
	if len(h.ProviderDays) != 1 || h.ProviderDays[0].ID != "p" {
		t.Fatalf("provider days outside the window leaked: %+v", h.ProviderDays)
	}
	if len(h.Plays) != 1 || h.Plays[0].Origin != "Debrid" || h.Plays[0].Day != "2026-11-08" || h.Plays[0].Failovers != 1 {
		t.Fatalf("plays %+v", h.Plays)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/sources?days=500", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"days":90`) {
		t.Fatalf("bounded window: %d %s", w.Code, w.Body.String())
	}
}
