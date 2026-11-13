package guide

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The budget and backoff protect a shared provider, including when the
// provider fails. These checks intentionally exercise the real HTTP client.
func TestPlexGuideRequestBudgetAndBackoff(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("X-Plex-Token") != "test-token" {
			t.Error("missing server token")
		}
		if r.URL.Path == "/rate" {
			w.Header().Set("Retry-After", "30")
			w.WriteHeader(429)
			return
		}
		fmt.Fprint(w, `{"MediaContainer":{"Lineup":[{"id":"a","title":"A (2 channels)","type":"ota"}]}}`)
	}))
	defer server.Close()
	s := &Client{HTTPClient: server.Client(), URL: server.URL, Token: func() string { return "test-token" }}
	budget := 1
	if _, err := s.plexGuideRequest(context.Background(), "/ok", nil, &budget); err != nil {
		t.Fatal(err)
	}
	if _, err := s.plexGuideRequest(context.Background(), "/ok", nil, &budget); err == nil || requests.Load() != 1 {
		t.Fatal("budget did not prevent request")
	}
	s.plexGate.next = time.Time{}
	if _, err := s.plexGuideRequest(context.Background(), "/rate", nil, nil); err == nil || !strings.Contains(err.Error(), "rate-limited") || strings.Contains(err.Error(), "test-token") {
		t.Fatalf("unexpected error: %v", err)
	}
	if time.Until(s.plexGate.next) < 29*time.Second {
		t.Fatal("Retry-After was not honoured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := s.plexGuideRequest(ctx, "/ok", nil, nil); err == nil || requests.Load() != 2 {
		t.Fatal("backoff did not prevent request")
	}
}

func TestPlexGuideGridMapping(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/grid" || r.URL.Query().Get("channelGridKey") != "key" || r.URL.Query().Get("type") != "1,4" || r.URL.Query().Get("date") != "2026-10-19" {
			t.Errorf("unexpected request: %s", r.URL.String())
		}
		fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"type":"episode","title":"Episode","grandparentTitle":"Show","summary":"Summary","guid":"plex://episode/example","grandparentGuid":"plex://show/example","parentIndex":2,"index":3,"originallyAvailableAt":"2026-10-18","grandparentThumb":"https://example.invalid/art","Genre":[{"tag":"Drama"}],"Media":[{"beginsAt":1792411200,"endsAt":1792414800,"premiere":true,"live":true}]}]}}`)
	}))
	defer server.Close()
	s := &Client{HTTPClient: server.Client(), URL: server.URL}
	budget := 1
	ps, err := s.Grid(context.Background(), "key", time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC), &budget)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d programmes", len(ps))
	}
	p := ps[0]
	if p.ChannelID != "key" || p.Item.Title != "Show" || p.Item.EpisodeTitle != "Episode" || p.Item.Season != 2 || p.Item.Episode != 3 || !p.Item.Premiere || !p.Item.Live || p.Item.GUID != "plex://episode/example" || p.Item.ShowGUID != "plex://show/example" || len(p.Item.Genres) != 1 || p.Item.ReleasedMS == 0 {
		t.Fatalf("bad mapping: %+v", p)
	}
}
