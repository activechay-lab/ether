package sessions

import (
	"context"
	"fmt"
	"github.com/braedonsaunders/ether/internal/auth"
	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
	"github.com/braedonsaunders/ether/internal/frontend/compactids"
	"github.com/braedonsaunders/ether/internal/frontend/frontendtest"
	"github.com/braedonsaunders/ether/internal/frontend/kit/ids"
	"github.com/braedonsaunders/ether/internal/frontends/xtream"
	"github.com/braedonsaunders/ether/internal/identity"
	"github.com/braedonsaunders/ether/internal/store"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type offlineXtreamIdentity struct {
	frontendtest.Identity
	grants *identity.Grants
}

func (i offlineXtreamIdentity) AuthenticateAppPassword(ctx context.Context, u, p string) (*core.Viewer, error) {
	if u != frontend.XtreamUsername(p) {
		return nil, core.ErrUnauthorized
	}
	return i.grants.Resolve(ctx, "xtream", p)
}

type offlineXtreamPlayback struct {
	*fakePlayback
	started, stopped chan struct{}
}

func (p *offlineXtreamPlayback) ServeRange(w http.ResponseWriter, r *http.Request, _ string) {
	if r.URL.Query().Get("hold") == "1" {
		close(p.started)
		<-r.Context().Done()
		close(p.stopped)
		return
	}
	http.ServeContent(w, r, "original.mkv", time.Time{}, strings.NewReader("offline-original-media-bytes"))
}
func TestXtreamOfflineRoutesThroughRealBroker(t *testing.T) {
	ctx := context.Background()
	db := store.OpenTest(t)
	users, err := auth.New(ctx, db, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	local, _ := identity.NewLocal(db)
	viewerID, err := local.CreateViewer(ctx, "offline-viewer", "password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if err = users.RefreshViewers(ctx); err != nil {
		t.Fatal(err)
	}
	grants, _ := identity.NewGrants(db, users, time.Hour, nil)
	password, err := grants.Issue(ctx, viewerID, nil, "xtream", core.Device{ID: "xtream-login"})
	if err != nil {
		t.Fatal(err)
	}
	active := users.ActiveUser
	events := NewEventBus(nil, active)
	defer events.Close()
	pb := &offlineXtreamPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}, started: make(chan struct{}), stopped: make(chan struct{})}
	live := &fakeLive{}
	lookup := func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		typ := core.TypeMovie
		if k.Kind() == core.KindEpisode {
			typ = core.TypeEpisode
		}
		return &core.Title{Key: k, Type: typ}, nil
	}
	b, err := NewBroker(BrokerDeps{Playback: pb, Live: live, Events: events, Progress: &fakeProgress{}, Transcoder: fakeBrokerTranscoder{}, ActiveViewer: active, Lookup: lookup, MediaLookup: lookup, Now: time.Now, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, BitrateLimit: func(*core.Viewer) int { return 0 }})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	lazy, err := NewLazyStreams(db, b, "xtream", grants, time.Hour, func(*http.Request) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	allocator, _ := compactids.New(db)
	host := frontend.Host{Identity: offlineXtreamIdentity{grants: grants}, Sessions: BindSurface(b, "xtream"), LazyStreams: lazy, IDs: ids.Codecs{Allocator: allocator}, Experience: frontendtest.Experience{ItemFunc: func(ctx context.Context, r frontend.ItemRequest) (frontend.ItemDetail, error) {
		v, e := lookup(ctx, r.Viewer, r.Device, r.Key)
		return frontend.ItemDetail{Title: *v}, e
	}}, LiveTV: frontendtest.LiveTV{ChannelsFunc: func(context.Context, *core.Viewer) ([]frontend.LiveChannel, error) {
		return []frontend.LiveChannel{{ID: "channel", Name: "Offline live"}}, nil
	}}, Capabilities: frontendtest.Capabilities{}, ClientIP: func(r *http.Request) (netip.Addr, bool) { return netip.MustParseAddr("192.0.2.1"), true }, SurfaceURL: func(context.Context, *http.Request) (string, error) { return "http://ether.test/xtream", nil }}
	inst, err := (xtream.Frontend{}).Mount(ctx, host)
	if err != nil {
		t.Fatal(err)
	}
	defer inst.Close(ctx)
	handler := inst.Handler()
	call := func(path, rangeValue string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("Range", rangeValue)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	movie, _ := allocator.Compact(ctx, "xtream", "item", fmt.Sprint(core.MovieKey(1)))
	episode, _ := allocator.Compact(ctx, "xtream", "item", fmt.Sprint(core.EpisodeKey(1, 1, 1)))
	channel, _ := allocator.Compact(ctx, "xtream", "channel", "channel")
	base := "/xtream/movie/" + frontend.XtreamUsername(password) + "/" + password + fmt.Sprintf("/%d.mkv", movie)
	w := call(base, "bytes=2-8")
	if w.Code != 206 || w.Body.String() != "fline-o" || w.Header().Get("Content-Range") != "bytes 2-8/28" {
		t.Fatalf("original range: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	done := make(chan struct{})
	go func() { defer close(done); call(base+"?hold=1", "") }()
	select {
	case <-pb.started:
	case <-time.After(2 * time.Second):
		t.Fatal("file did not start")
	}
	livePath := fmt.Sprintf("/xtream/live/%s/%s/%d.m3u8", frontend.XtreamUsername(password), password, channel)
	w = call(livePath, "")
	if w.Code != 307 {
		t.Fatalf("live primary: %d %s", w.Code, w.Body.String())
	}
	select {
	case <-pb.stopped:
	case <-time.After(time.Second):
		t.Fatal("original response not promptly canceled")
	}
	<-done
	location := strings.TrimPrefix(w.Header().Get("Location"), "http://ether.test")
	w = call(location, "")
	if w.Code != 200 || live.tunes != 1 {
		t.Fatalf("live broker: %d %s tunes=%d", w.Code, w.Body.String(), live.tunes)
	}
	epPath := fmt.Sprintf("/xtream/series/%s/%s/%d.mkv", frontend.XtreamUsername(password), password, episode)
	w = call(epPath, "bytes=0-6")
	if w.Code != 206 || w.Body.String() != "offline" {
		t.Fatalf("episode range: %d %s", w.Code, w.Body.String())
	}
	list, err := grants.List(ctx, viewerID)
	if err != nil {
		t.Fatal(err)
	}
	if err = grants.RevokeGrant(ctx, viewerID, list[0].Hash); err != nil {
		t.Fatal(err)
	}
	if w = call(epPath, ""); w.Code != 401 {
		t.Fatalf("revoked primary: %d", w.Code)
	}
	if w = call(location, ""); w.Code != 404 {
		t.Fatalf("revoked capability: %d", w.Code)
	}
}
