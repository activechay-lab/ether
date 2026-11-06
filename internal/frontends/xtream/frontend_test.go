package xtream

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/compactids"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/logredact"
	"github.com/braedonsaunders/ether/internal/store"
)

type appIdentity struct {
	frontendtest.Identity
	revoked bool
}

func (a *appIdentity) AuthenticateAppPassword(ctx context.Context, u, p string) (*frontend.Viewer, error) {
	if a.revoked || u != frontend.XtreamUsername("eg1_test") || p != "eg1_test" {
		return nil, core.ErrUnauthorized
	}
	if _, ok := frontend.ClientIP(ctx); !ok {
		return nil, core.ErrUnauthorized
	}
	return &frontend.Viewer{ID: 1}, nil
}
func testHost(t *testing.T) (frontend.Host, *appIdentity) {
	t.Helper()
	a := &appIdentity{}
	db := store.OpenTest(t)
	allocator, err := compactids.New(db)
	if err != nil {
		t.Fatal(err)
	}
	host := frontend.Host{Identity: a, IDs: ids.Codecs{Allocator: allocator}, Sessions: frontendtest.Sessions{}, Experience: frontendtest.Experience{}, ClientIP: func(*http.Request) (netip.Addr, bool) { return netip.MustParseAddr("127.0.0.1"), true }, SurfaceURL: func(context.Context, *http.Request) (string, error) { return "http://ether.test/xtream", nil }, LiveTV: frontendtest.LiveTV{ChannelsFunc: func(_ context.Context, v *frontend.Viewer) ([]frontend.LiveChannel, error) {
		if v.ID != 1 {
			t.Fatal("wrong viewer")
		}
		return []frontend.LiveChannel{{ID: "allowed", Name: "News", Number: "7", Kind: "iptv"}}, nil
	}, GuideFunc: func(_ context.Context, v *frontend.Viewer, q frontend.LiveGuideQuery) ([]frontend.LiveProgramme, error) {
		if v.ID != 1 || len(q.Channels) != 1 || q.Channels[0] != "allowed" {
			t.Fatal("unscoped guide")
		}
		return []frontend.LiveProgramme{{ID: "p1", ChannelID: "allowed", Title: "Now & next", Summary: "Summary", Start: time.Now().Add(-time.Hour), End: time.Now().Add(time.Hour)}, {ID: "secret", ChannelID: "blocked", Title: "Hidden", Start: time.Now(), End: time.Now().Add(time.Hour)}}, nil
	}}}
	return host, a
}
func mount(t *testing.T, h frontend.Host) http.Handler {
	t.Helper()
	i, err := (Frontend{}).Mount(context.Background(), h)
	if err != nil {
		t.Fatal(err)
	}
	return i.Handler()
}
func request(h http.Handler, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/xtream"+path, nil))
	return w
}
func authQuery() string {
	return "username=" + frontend.XtreamUsername("eg1_test") + "&password=eg1_test"
}
func TestStartupSequenceAndRevocation(t *testing.T) {
	host, a := testHost(t)
	h := mount(t, host)
	for _, action := range []string{"", "get_live_categories", "get_live_streams", "get_vod_categories", "get_series_categories"} {
		w := request(h, "/player_api.php?"+authQuery()+"&action="+action)
		if w.Code != 200 || !json.Valid(w.Body.Bytes()) {
			t.Fatalf("%s %d %s", action, w.Code, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Hidden") {
			t.Fatal("policy leak")
		}
	}
	w := request(h, "/player_api.php?"+authQuery()+"&action=get_live_streams")
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["category_id"] != "2" || rows[0]["num"] != float64(1) {
		t.Fatal(rows)
	}
	id := fmt.Sprint(rows[0]["stream_id"])
	for _, action := range []string{"get_short_epg", "get_simple_data_table"} {
		w = request(h, "/player_api.php?"+authQuery()+"&action="+action+"&stream_id="+id)
		if w.Code != 200 || !strings.Contains(w.Body.String(), base64.StdEncoding.EncodeToString([]byte("Now & next"))) {
			t.Fatalf("epg %s", w.Body.String())
		}
		if strings.Contains(w.Body.String(), "Hidden") {
			t.Fatal("guide leak")
		}
	}
	w = request(h, "/xmltv.php?"+authQuery())
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Now &amp; next") || strings.Contains(w.Body.String(), "Hidden") {
		t.Fatal(w.Body.String())
	}
	if w = request(h, "/player_api.php?username=owner&password=main-password"); w.Code != 401 {
		t.Fatal("accepted main password")
	}
	a.revoked = true
	if w = request(h, "/player_api.php?"+authQuery()); w.Code != 401 {
		t.Fatal("revocation ignored")
	}
}
func TestCatalogueUsesNeutralPagesAndCompactIDs(t *testing.T) {
	host, _ := testHost(t)
	calls := 0
	movie := frontend.Title{Key: core.MovieKey(12), Type: core.TypeMovie, Title: "Film", Summary: "Plot", Runtime: 90 * time.Minute, Year: 2026, Rating: 8}
	host.Experience = frontendtest.Experience{ViewsFunc: func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
		return []frontend.View{{ID: "1", Type: core.TypeMovie, Name: "Movies"}, {ID: "2", Type: core.TypeMovie, Name: "Other"}}, nil
	}, LibraryItemsFunc: func(_ context.Context, v *core.Viewer, d core.Device, view string, q frontend.ItemQuery) (core.Page, error) {
		calls++
		if view != "1" || q.Limit != pageSize || q.Offset != 0 {
			t.Fatal("wrong category or unbounded page")
		}
		return core.Page{Items: []core.Title{movie}, Total: 1}, nil
	}, ItemFunc: func(_ context.Context, r frontend.ItemRequest) (frontend.ItemDetail, error) {
		if r.Viewer.ID != 1 || r.Key != movie.Key {
			t.Fatal("wrong catalogue identity")
		}
		return frontend.ItemDetail{Title: movie}, nil
	}}
	h := mount(t, host)
	w := request(h, "/player_api.php?"+authQuery()+"&action=get_vod_streams&category_id=1")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var rows []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	id := rows[0]["stream_id"].(float64)
	if id <= 0 || id > 2147483647 || calls != 1 {
		t.Fatalf("id=%v calls=%d", id, calls)
	}
	w = request(h, fmt.Sprintf("/player_api.php?%s&action=get_vod_info&vod_id=%.0f", authQuery(), id))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duration_secs":5400`) {
		t.Fatal(w.Body.String())
	}
}
func TestCredentialRedaction(t *testing.T) {
	for _, s := range []string{"http://host/xtream/live/private-user/private-pass/1.ts", "/movie/private-user/private-pass/1.mkv", "/series/private-user/private-pass/1.mp4", "/player_api.php?username=private-user&password=private-pass", `{"username":"private-user","password":"private-pass"}`} {
		safe := logredact.String(s)
		if strings.Contains(safe, "private-user") || strings.Contains(safe, "private-pass") {
			t.Fatalf("leak: %s", safe)
		}
	}
}

type syntheticExport struct {
	frontendtest.Experience
	size, calls int
}

func (s *syntheticExport) ExportLibraryPage(_ context.Context, _ *frontend.Viewer, _ frontend.Device, view string, after frontend.RatingKey, limit int) (frontend.LibraryExportPage, error) {
	s.calls++
	if view != "1" || limit != pageSize {
		panic("unbounded export")
	}
	start := 0
	if after != 0 {
		start = int(after - core.MovieKey(0))
	}
	end := min(start+limit, s.size)
	items := make([]frontend.Title, 0, end-start)
	for n := start; n < end; n++ {
		items = append(items, frontend.Title{Key: core.MovieKey(int64(n + 1)), Type: core.TypeMovie, Title: fmt.Sprintf("Film %d", n)})
	}
	return frontend.LibraryExportPage{Items: items, Next: core.MovieKey(int64(end)), Done: end == s.size}, nil
}

type discardResponse struct {
	h     http.Header
	bytes int
}

func (w *discardResponse) Header() http.Header         { return w.h }
func (w *discardResponse) WriteHeader(int)             {}
func (w *discardResponse) Write(b []byte) (int, error) { w.bytes += len(b); return len(b), nil }

var _ io.Writer = (*discardResponse)(nil)

func TestLargeCatalogueExport(t *testing.T) {
	host, _ := testHost(t)
	s := &syntheticExport{size: 100001}
	s.ViewsFunc = func(context.Context, *frontend.Viewer) ([]frontend.View, error) {
		return []frontend.View{{ID: "1", Type: core.TypeMovie}}, nil
	}
	s.LibraryItemsFunc = func(context.Context, *frontend.Viewer, frontend.Device, string, frontend.ItemQuery) (core.Page, error) {
		t.Fatal("offset fallback used")
		return core.Page{}, nil
	}
	host.Experience = s
	h := mount(t, host)
	w := &discardResponse{h: make(http.Header)}
	start := time.Now()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/xtream/player_api.php?"+authQuery()+"&action=get_vod_streams&category_id=1", nil))
	elapsed := time.Since(start)
	if s.calls != 401 || w.bytes < 1000000 {
		t.Fatalf("calls=%d bytes=%d", s.calls, w.bytes)
	}
	if elapsed > 30*time.Second {
		t.Fatalf("export too slow %v", elapsed)
	}
	t.Logf("100001 titles: %v, %d bounded pages, %d bytes", elapsed, s.calls, w.bytes)
}

type testLazy struct {
	t   *testing.T
	got frontend.PlanRequest
}

func (s *testLazy) Issue(_ context.Context, p string, r frontend.PlanRequest) (string, error) {
	s.got = r
	if r.Channel != "allowed" || p != "eg1_test" {
		s.t.Fatal("bad live scope")
	}
	return "/sessions/lazy/opaque/live/index.m3u8", nil
}
func (s *testLazy) ServeHTTP(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }
func (s *testLazy) ServeAuthenticated(w http.ResponseWriter, r *http.Request, p string, plan frontend.PlanRequest) {
	s.got = plan
	if p != "eg1_test" || r.Header.Get("Range") != "bytes=10-19" {
		s.t.Fatal("range or credential lost")
	}
	w.Header().Set("Content-Range", "bytes 10-19/100")
	w.WriteHeader(206)
	_, _ = w.Write([]byte("0123456789"))
}
func TestXtreamPlaybackUsesBrokerAndPreservesRange(t *testing.T) {
	host, _ := testHost(t)
	key := core.MovieKey(12)
	host.Experience = frontendtest.Experience{ItemFunc: func(_ context.Context, r frontend.ItemRequest) (frontend.ItemDetail, error) {
		if r.Key != key || r.Viewer.ID != 1 {
			return frontend.ItemDetail{}, core.ErrNotFound
		}
		return frontend.ItemDetail{Title: frontend.Title{Key: key, Type: core.TypeMovie}}, nil
	}}
	host.Capabilities = frontendtest.Capabilities{BuildFunc: func(_ context.Context, r frontend.CapabilityRequest) (frontend.CapabilityResult, error) {
		if r.Device.Protocol != "xtream" || len(r.Device.VideoCodecs) == 0 {
			t.Fatal("missing native declarations")
		}
		return frontend.CapabilityResult{}, nil
	}}
	lazy := &testLazy{t: t}
	host.LazyStreams = lazy
	h := mount(t, host)
	id, err := host.IDs.Compact(context.Background(), "xtream", "item", fmt.Sprint(key))
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", fmt.Sprintf("/xtream/movie/%s/eg1_test/%d.mkv", frontend.XtreamUsername("eg1_test"), id), nil)
	r.Header.Set("Range", "bytes=10-19")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 || lazy.got.Item != key || lazy.got.Intent.AllowTranscoding == nil || *lazy.got.Intent.AllowTranscoding || !lazy.got.Intent.Options.DirectPlay {
		t.Fatalf("bad playback: %d %+v", w.Code, lazy.got)
	}
	id, err = host.IDs.Compact(context.Background(), "xtream", "channel", "allowed")
	if err != nil {
		t.Fatal(err)
	}
	w = request(h, fmt.Sprintf("/live/%s/eg1_test/%d.m3u8", frontend.XtreamUsername("eg1_test"), id))
	if w.Code != 307 || w.Header().Get("Location") != "http://ether.test/xtream/sessions/lazy/opaque/live/index.m3u8" || strings.Contains(w.Header().Get("Location"), "eg1_") {
		t.Fatal(w.Code, w.Header())
	}
}
func TestXtreamSeriesSeasonsAndEpisodeIdentity(t *testing.T) {
	host, _ := testHost(t)
	show := frontend.Title{Key: core.ShowKey(1), Type: core.TypeShow, Title: "Show"}
	host.Experience = frontendtest.Experience{ItemFunc: func(_ context.Context, r frontend.ItemRequest) (frontend.ItemDetail, error) {
		return frontend.ItemDetail{Title: show}, nil
	}, ChildrenFunc: func(_ context.Context, v *frontend.Viewer, d frontend.Device, k core.RatingKey, q frontend.ItemQuery) (core.Page, error) {
		if v.ID != 1 || q.Limit != pageSize {
			t.Fatal("unscoped/unbounded series")
		}
		if k == show.Key {
			return core.Page{Items: []core.Title{{Key: core.SeasonKey(1, 1), Type: core.TypeSeason, Season: 1}, {Key: core.SeasonKey(1, 2), Type: core.TypeSeason, Season: 2}}, Total: 2}, nil
		}
		n := k.Season()
		return core.Page{Items: []core.Title{{Key: core.EpisodeKey(1, n, 1), ShowKey: show.Key, Type: core.TypeEpisode, Season: n, Episode: 1, Title: "Episode"}}, Total: 1}, nil
	}}
	id, err := host.IDs.Compact(context.Background(), "xtream", "item", fmt.Sprint(show.Key))
	if err != nil {
		t.Fatal(err)
	}
	h := mount(t, host)
	w := request(h, fmt.Sprintf("/player_api.php?%s&action=get_series_info&series_id=%d", authQuery(), id))
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var result struct {
		Seasons []struct {
			ID int32 `json:"id"`
		}
		Episodes map[string][]struct {
			ID string `json:"id"`
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Seasons) != 2 || result.Seasons[0].ID == 0 || result.Seasons[1].ID == 0 || len(result.Episodes["1"]) != 1 || len(result.Episodes["2"]) != 1 {
		t.Fatal(w.Body.String())
	}
	for n := 1; n <= 2; n++ {
		ref, err := host.IDs.Resolve(context.Background(), "xtream", "item", int32(mustInt(t, result.Episodes[fmt.Sprint(n)][0].ID)))
		if err != nil || ref != fmt.Sprint(core.EpisodeKey(1, n, 1)) {
			t.Fatalf("episode identity %s %v", ref, err)
		}
	}
}
func mustInt(t *testing.T, s string) int64 {
	t.Helper()
	var n int64
	if _, err := fmt.Sscan(s, &n); err != nil {
		t.Fatal(err)
	}
	return n
}
