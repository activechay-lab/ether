package livetv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestActivityRefreshAndPublication(t *testing.T) {
	ctx := context.Background()
	clock := &testClock{now: guideTestNow}
	var hits atomic.Int32
	var failing atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if failing.Load() {
			http.Error(w, "unavailable", 503)
			return
		}
		_, _ = w.Write([]byte(m3uFixture("one=One", "two=Two")))
	}))
	defer provider.Close()
	s := testService(t, Options{Now: clock.Now, HTTPClient: provider.Client()})
	src := Source{ID: "activity", Name: "Activity", PlaylistURL: provider.URL, RefreshHours: 24, ActivityRefreshMinutes: 60}
	if _, err := s.ImportSource(ctx, src); err != nil {
		t.Fatal(err)
	}
	base := hits.Load()
	clock.Set(guideTestNow.Add(59 * time.Minute))
	if due := s.refreshSources(ctx, true); len(due) != 0 || hits.Load() != base {
		t.Fatal("cooldown bypassed")
	}
	clock.Set(guideTestNow.Add(time.Hour))
	release, err := s.acquireRefresh(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	other := &Service{o: s.o}
	if _, err := other.acquireRefresh(ctx, src.ID); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("cross-node lease: %v", err)
	}
	if _, err := s.RefreshSource(ctx, src.ID); !errors.Is(err, core.ErrNotAvailable) || hits.Load() != base {
		t.Fatalf("duplicate refresh: %v", err)
	}
	if _, err := s.ImportSource(ctx, src); !errors.Is(err, core.ErrNotAvailable) || hits.Load() != base {
		t.Fatalf("owner edit overlapped refresh: %v", err)
	}
	release()
	if due := s.refreshSources(ctx, true); len(due) != 1 || hits.Load() != base+1 {
		t.Fatal("activity did not refresh")
	}
	if due := s.refreshSources(ctx, true); len(due) != 0 || hits.Load() != base+1 {
		t.Fatal("unchanged import did not reset cooldown")
	}

	owner := &core.User{ID: 1, IsOwner: true}
	channels, err := s.AllowedChannels(ctx, owner)
	if err != nil || len(channels) != 2 {
		t.Fatalf("channels: %v %v", channels, err)
	}
	hidden := true
	if _, err := s.UpdateChannels(ctx, ChannelBatch{IDs: []string{channels[0].ID}, PlexHidden: &hidden}); err != nil {
		t.Fatal(err)
	}
	plexCtx := WithPlexLineup(ctx)
	if got, err := s.AllowedChannels(plexCtx, owner); err != nil || len(got) != 1 || got[0].ID != channels[1].ID {
		t.Fatalf("Plex subset: %v %v", got, err)
	}
	if _, err := s.AllowedChannel(plexCtx, owner, channels[0].ID); !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("hidden Plex tune: %v", err)
	}
	if got, err := s.AllowedChannels(ctx, owner); err != nil || len(got) != 2 {
		t.Fatalf("external lineup changed: %v %v", got, err)
	}
	if _, err := s.RefreshSource(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Channel(ctx, channels[0].ID); err != nil || !got.PlexHidden {
		t.Fatalf("refresh lost publication choice: %v", err)
	}

	failing.Store(true)
	clock.Set(guideTestNow.Add(2 * time.Hour))
	s.refreshSources(ctx, true)
	failedHits := hits.Load()
	clock.Set(guideTestNow.Add(2*time.Hour + 30*time.Minute))
	if due := s.refreshSources(ctx, true); len(due) != 0 || hits.Load() != failedHits {
		t.Fatal("failure cooldown bypassed")
	}
	if got, err := s.AllowedChannels(ctx, owner); err != nil || len(got) != 2 {
		t.Fatalf("failure discarded lineup: %v", err)
	}
	if activityRefreshDue(Source{PlaylistURL: provider.URL, RefreshHours: -1, ActivityRefreshMinutes: 30}, clock.Now()) {
		t.Fatal("manual source refreshed automatically")
	}
	if activityRefreshDue(Source{Playlist: "upload", ActivityRefreshMinutes: 30}, clock.Now()) {
		t.Fatal("upload refreshed automatically")
	}
}

func TestClientActivityCoalescesWithoutRequestIO(t *testing.T) {
	clock := &testClock{now: guideTestNow}
	s := &Service{o: Options{Now: clock.Now}, ctx: context.Background(), activityWake: make(chan struct{}, 1), sessions: make(map[string]*session)}
	for range 100 {
		s.NoteClientActivity()
	}
	if len(s.activityWake) != 1 {
		t.Fatal("requests did not coalesce")
	}
	<-s.activityWake
	clock.Set(guideTestNow.Add(clientIdleWindow))
	s.sessions["playing"] = &session{}
	s.NoteClientActivity()
	if len(s.activityWake) != 0 {
		t.Fatal("active playback triggered activity refresh")
	}
	delete(s.sessions, "playing")
	clock.Set(guideTestNow.Add(2 * clientIdleWindow))
	s.NoteClientActivity()
	if len(s.activityWake) != 1 {
		t.Fatal("return after idle did not wake refresh")
	}
}
