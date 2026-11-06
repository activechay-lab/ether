package livetv

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

func TestViewingSessionIncludesSelectedChannel(t *testing.T) {
	tc := &stagedIPTVTranscoder{info: stagedIPTVInfo()}
	s, u, channel := stagedIPTVFixture(t, tc)
	info, err := s.Tune(context.Background(), u, channel.ID, core.ClientCaps{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background(), info.ID, u)
	views := s.ViewingSessions()
	if len(views) != 1 || views[0].LiveChannel == nil || views[0].LiveChannel.ID != channel.ID || views[0].LiveChannel.Name != channel.Name || views[0].LiveChannel.Number != channel.Number {
		t.Fatalf("live session channel = %+v", views)
	}
	// The wire snapshot must retain channel identity alongside a distinct EPG title.
	views[0].TitleName = "Evening News"
	data, err := json.Marshal(views[0])
	if err != nil {
		t.Fatal(err)
	}
	var decoded core.PlaySession
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.TitleName != "Evening News" || decoded.LiveChannel == nil || decoded.LiveChannel.ID != channel.ID || decoded.LiveChannel.Name != channel.Name || decoded.LiveChannel.Number != channel.Number {
		t.Fatalf("round trip: %+v", decoded)
	}
	vod, err := json.Marshal(core.PlaySession{TitleName: "Movie"})
	if err != nil || strings.Contains(string(vod), "liveChannel") {
		t.Fatalf("VOD JSON = %s, %v", vod, err)
	}
}

func TestVirtualChannelIdentityIsSeparateFromProgramme(t *testing.T) {
	now := time.Unix(7, 0).UTC()
	s := testService(t, Options{Now: func() time.Time { return now }, Playback: &fakePlayback{}, Transcoder: &fakeTranscoder{}})
	c := testChannel()
	if _, err := s.SaveChannel(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	u := &core.User{ID: 1, IsOwner: true}
	info, err := s.Tune(context.Background(), u, c.ID, core.ClientCaps{})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Stop(context.Background(), info.ID, u)
	views := s.ViewingSessions()
	if len(views) != 1 || views[0].LiveChannel == nil || views[0].LiveChannel.ID != c.ID || views[0].LiveChannel.Name != c.Name || views[0].Key == 0 {
		t.Fatalf("virtual channel identity lost: %+v", views)
	}
}
