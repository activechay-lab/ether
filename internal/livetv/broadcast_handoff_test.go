package livetv

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/braedonsaunders/ether/internal/core"
)

type budgetTranscoder struct{ *fakeTranscoder }

func (budgetTranscoder) ContinuousBudget() (float64, bool) { return 100, false }

// A direct session handed off to Steady shares its provider connection: on a
// one-connection account the hand-off is admitted, and the Steady output
// reads the session's own resolved input. Sessions refused for reasons other
// than capacity are never handed off.
func TestSteadyHandoffSharesTheDirectConnection(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	s := testService(t, Options{Transcoder: budgetTranscoder{&fakeTranscoder{}}, HTTPClient: up.Client()})
	ctx := context.Background()
	if _, err := s.ImportSource(ctx, Source{ID: "acct", Name: "Provider", Playlist: "#EXTM3U\n#EXTINF:-1 tvg-id=\"one\",One\n" + up.URL + "/live/1.ts\n"}); err != nil {
		t.Fatal(err)
	}
	e, err := s.sourceEntry(ctx, "acct")
	if err != nil {
		t.Fatal(err)
	}
	e.limit = 1
	s.authCache.m["acct"] = e
	cs, _ := s.Channels(ctx)
	c := cs[0]
	if err := normalizeStreamSources(&c); err != nil {
		t.Fatal(err)
	}
	direct := &session{info: SessionInfo{ID: "direct-session-0001", Channel: c}, user: core.User{ID: 1, IsOwner: true}, ready: true, inputSource: c.StreamSources[0], inputURL: c.StreamSources[0].URL, inputHeaders: http.Header{}, steadyRefused: steadyRefusedCapacity}
	direct.ctx, direct.cancel = context.WithCancel(ctx)
	defer direct.cancel()
	s.mu.Lock()
	s.sessions[direct.info.ID] = direct
	s.mu.Unlock()

	feed, err := s.steadyHandoff(ctx, direct)
	if err != nil {
		t.Fatalf("hand-off on a one-connection account: %v", err)
	}
	b := s.broadcasts.running(broadcastKey{channelID: c.ID})
	if b == nil {
		t.Fatal("no Steady output started")
	}
	planner := b.planner.(*providerBroadcastPlanner)
	planner.mu.Lock()
	link := planner.primed
	if link == nil {
		link = planner.link
	}
	planner.mu.Unlock()
	if link == nil || link.ss.inputURL != direct.inputURL {
		t.Fatal("Steady output did not adopt the session's resolved input")
	}
	feed.Close()

	hdr := &session{info: SessionInfo{ID: "direct-session-0002", Channel: c}, steadyRefused: steadyRefusedHDR, inputURL: direct.inputURL}
	if _, err := s.steadyHandoff(ctx, hdr); !errors.Is(err, core.ErrNotAvailable) {
		t.Fatalf("HDR-refused session handed off: %v", err)
	}
}
