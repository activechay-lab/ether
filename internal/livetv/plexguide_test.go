package livetv

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
	s := &Service{o: Options{HTTPClient: server.Client(), PlexGuideURL: server.URL, PlexGuideToken: func() string { return "test-token" }}}
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
	s := &Service{o: Options{HTTPClient: server.Client(), PlexGuideURL: server.URL}}
	budget := 1
	ps, err := s.plexGrid(context.Background(), "key", guideTestNow, &budget)
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

func TestPlexGuideMappedRefreshAndReviewedAliases(t *testing.T) {
	var grids atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lineups/lineup/channels":
			fmt.Fprint(w, `{"MediaContainer":{"Channel":[{"gridKey":"one","title":"WCBS-DT","callSign":"WCBSDT","affiliateCallSign":"CBS","vcn":"2.1"},{"gridKey":"two","title":"Other"}]}}`)
		case "/grid":
			if r.URL.Query().Get("channelGridKey") != "one" {
				t.Error("requested an unmapped channel")
			}
			grids.Add(1)
			fmt.Fprintf(w, `{"MediaContainer":{"Metadata":[{"type":"movie","title":"Movie","guid":"plex://movie/example","Media":[{"beginsAt":%d,"endsAt":%d}]}]}}`, guideTestNow.Unix(), guideTestNow.Add(time.Hour).Unix())
		default:
			t.Error("unexpected request")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	ctx := context.Background()
	s := testService(t, Options{Now: func() time.Time { return guideTestNow }, HTTPClient: server.Client(), PlexGuideURL: server.URL})
	_, err := s.ImportSource(ctx, Source{ID: "guide", Name: "Guide", Kind: "plex", LineupID: "lineup"})
	if err != nil {
		t.Fatal(err)
	}
	if grids.Load() != 0 {
		t.Fatal("fetched grid before a mapping")
	}
	found, err := s.GuideChannels(ctx, "CBS", "", 50)
	if err != nil || len(found) != 1 || found[0].GuideID != "one" {
		t.Fatalf("search: %+v %v", found, err)
	}
	_, err = s.ImportSource(ctx, Source{ID: "playlist", Name: "Playlist", Playlist: m3uFixture("=CBS New York")})
	if err != nil {
		t.Fatal(err)
	}
	proposal, err := s.MatchGuides(ctx, GuideMatchRequest{DryRun: true})
	if err != nil || len(proposal.Changes) != 1 || proposal.Changes[0].Method != "alias" || !proposal.Changes[0].ReviewRegion {
		t.Fatalf("proposal: %+v %v", proposal, err)
	}
	if result, e := s.MatchGuides(ctx, GuideMatchRequest{}); e != nil || len(result.Changes) != 0 {
		t.Fatalf("alias applied without review: %+v %v", result, e)
	}
	if _, err = s.MatchGuides(ctx, GuideMatchRequest{ChannelIDs: []string{proposal.Changes[0].ChannelID}, Reviewed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RefreshSource(ctx, "guide"); err != nil {
		t.Fatal(err)
	}
	if grids.Load() != 2 {
		t.Fatalf("got %d grid requests", grids.Load())
	}
	src := sourceByID(t, s, "guide")
	if !src.HasGuide || src.ProgrammeCount != 1 || src.NextRefresh == nil {
		t.Fatalf("source: %+v", src)
	}
}

func TestPlexGuideRateLimitSharedBetweenServices(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(429)
	}))
	defer server.Close()
	s := testService(t, Options{HTTPClient: server.Client(), PlexGuideURL: server.URL})
	if _, err := s.plexGuideRequest(context.Background(), "/grid", nil, nil); err == nil {
		t.Fatal("expected rate limit")
	}
	other := &Service{o: Options{DB: s.o.DB, HTTPClient: server.Client(), PlexGuideURL: server.URL}}
	if _, err := other.plexGuideRequest(context.Background(), "/grid", nil, nil); err == nil || !strings.Contains(err.Error(), "rate-limited") {
		t.Fatalf("shared status: %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("made %d requests during shared cooldown", requests.Load())
	}
}

func TestPlexGuideOverlappingDatesAndAirings(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Query().Get("date") {
		case "2026-11-04":
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"type":"episode","title":"Daily","parentIndex":2026,"originallyAvailableAt":"2026-11-04","Media":[{"beginsAt":1793815200,"endsAt":1793818800},{"beginsAt":1793808000,"endsAt":1793811600}]},{"title":"Other","Media":[{"beginsAt":1793811600,"endsAt":1793815200}]}]}}`)
		case "2026-11-05":
			fmt.Fprint(w, `{"MediaContainer":{"Metadata":[{"type":"episode","title":"Daily","parentIndex":2026,"originallyAvailableAt":"2026-11-04","Media":[{"beginsAt":1793815200,"endsAt":1793818800},{"beginsAt":1793808000,"endsAt":1793811600}]}]}}`)
		default:
			t.Error("unexpected date")
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	s := &Service{o: Options{HTTPClient: server.Client(), PlexGuideURL: server.URL}}
	now := time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)
	dates := plexGridDates(now, now, now.Add(12*time.Hour))
	budget := len(dates)
	got, err := s.plexGrids(context.Background(), "one", dates, &budget)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || len(got) != 3 {
		t.Fatalf("calls=%d programmes=%d", calls.Load(), len(got))
	}
	for i := 1; i < len(got); i++ {
		if !got[i-1].Start.Before(got[i].Start) {
			t.Fatal("airings not sorted or deduplicated")
		}
	}
	if got[0].Item.Title != "Daily" || got[0].Item.Season != 0 || got[0].Item.Episode != 0 || got[0].Item.ReleasedMS == 0 || got[2].Item.Title != "Daily" {
		t.Fatalf("incorrect daily airings: %+v", got)
	}
	local := time.FixedZone("western", -8*60*60)
	boundary := time.Date(2026, 11, 5, 1, 0, 0, 0, time.UTC).In(local)
	dates = plexGridDates(boundary, boundary, boundary.Add(12*time.Hour))
	if len(dates) != 3 || dates[0].Format("2006-01-02") != "2026-11-04" || dates[2].Format("2006-01-02") != "2026-11-06" {
		t.Fatalf("local boundary dates: %v", dates)
	}
}
