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

	"github.com/braedonsaunders/ether/internal/plexguide"
)

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
	s := testService(t, Options{Now: func() time.Time { return guideTestNow }, HTTPClient: server.Client(), GuideProvider: &guide.Client{HTTPClient: server.Client(), URL: server.URL}})
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
	s := testService(t, Options{HTTPClient: server.Client(), GuideProvider: &guide.Client{HTTPClient: server.Client(), URL: server.URL}})
	s.o.GuideProvider.(*guide.Client).DB = s.o.DB
	if _, err := s.o.GuideProvider.Grid(context.Background(), "one", guideTestNow, nil); err == nil {
		t.Fatal("expected rate limit")
	}
	other := &guide.Client{DB: s.o.DB, HTTPClient: server.Client(), URL: server.URL}
	if _, err := other.Grid(context.Background(), "one", guideTestNow, nil); err == nil || !strings.Contains(err.Error(), "rate-limited") {
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
	s := &Service{o: Options{HTTPClient: server.Client(), GuideProvider: &guide.Client{HTTPClient: server.Client(), URL: server.URL}}}
	now := time.Date(2026, 11, 4, 20, 0, 0, 0, time.UTC)
	dates := guideGridDates(now, now, now.Add(12*time.Hour))
	budget := len(dates)
	got, err := s.guideGrids(context.Background(), "one", dates, &budget)
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
	dates = guideGridDates(boundary, boundary, boundary.Add(12*time.Hour))
	if len(dates) != 3 || dates[0].Format("2006-01-02") != "2026-11-04" || dates[2].Format("2006-01-02") != "2026-11-06" {
		t.Fatalf("local boundary dates: %v", dates)
	}
}
