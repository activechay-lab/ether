package sessions

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

// kindPlayback extends fakePlayback with the plugin kind entry points so a
// plugin key can be planned through the real Broker.Plan path.
type kindPlayback struct {
	fakePlayback
	info     core.MediaInfo
	prepared int
}

func (k *kindPlayback) PrepareKind(_ context.Context, u *core.User, in core.KindInput, c core.ClientCaps) (*core.PlaySession, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for _, p := range k.sessions {
		if p.UserID == u.ID && p.ClientID == c.ClientID && p.Key == in.Title.Key {
			return p, nil
		}
	}
	k.prepared++
	id := fmt.Sprintf("kind-source-%d", k.prepared)
	p := &core.PlaySession{
		ID: id, UserID: u.ID, ClientID: c.ClientID, Key: in.Title.Key,
		Info:       k.info,
		Resolution: &core.Resolution{Release: core.Release{ID: "kind:" + in.SourceID, Name: in.Title.Title, Protocol: core.ProtocolHTTP}},
	}
	k.sessions[id] = p
	return p, nil
}

func (k *kindPlayback) SessionForKind(user int64, client string, key core.RatingKey) (*core.PlaySession, bool) {
	return k.SessionFor(user, client, key)
}

// A plugin key planned through the real Broker.Plan entry point is transcoded
// when the client caps reject the source codec and direct-plays when they fit,
// exactly like a catalogue title (spec §15.1).
func TestPlanKindDecidesTranscodeOrDirect(t *testing.T) {
	ctx := context.Background()
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	key := core.PluginKey(7)
	source := func(ctx context.Context, _ *core.Viewer, k core.RatingKey) (*core.KindInput, error) {
		return &core.KindInput{
			Title:    &core.Title{Key: k, Type: core.TypeMovie, Title: "Plugin Leaf"},
			SourceID: "leaf-1",
			Open:     func(context.Context) (core.MediaSource, error) { return nil, core.ErrNotAvailable },
		}, nil
	}
	newBroker := func(info core.MediaInfo) (*Broker, *kindPlayback) {
		playback := &kindPlayback{fakePlayback: fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}, info: info}
		events := NewEventBus(nil, active)
		t.Cleanup(events.Close)
		b, err := NewBroker(BrokerDeps{SurfacePolicy: testSessionPolicy,
			Events: events, Playback: playback, Transcoder: profileTranscoder{}, Progress: &fakeProgress{},
			ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true },
			BitrateLimit: func(*core.Viewer) int { return 0 },
			OnTimeline:   func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {},
			KindSource:   source,
			Lookup: func(context.Context, *core.Viewer, core.Device, core.RatingKey) (*core.Title, error) {
				return nil, core.ErrNotFound
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close(ctx) })
		return b, playback
	}
	// Caps that accept h264/mkv only.
	caps := core.ClientCaps{Product: "Plex", ClientID: "c1", MaxHeight: 1080,
		VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac", "eac3"}, Containers: []string{"mkv", "mp4"}}

	// Source is HEVC in mkv: the client caps reject the codec, so the plan
	// must transcode.
	hevc := core.MediaInfo{Container: "mkv", Duration: 100 * time.Second, Size: 100 << 20,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "hevc", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
		}}
	b, playback := newBroker(hevc)
	allow := true
	plan, err := b.Plan(ctx, frontend.PlanRequest{Viewer: &core.Viewer{ID: 1}, Device: core.Device{ID: "d"}, Protocol: "plex", Item: key, Caps: caps, Intent: frontend.PlayIntent{AllowTranscoding: &allow, Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}}})
	if err != nil {
		t.Fatalf("Plan (hevc): %v", err)
	}
	if plan.Decision.Kind == core.DecisionDirectPlay {
		t.Fatalf("hevc plan decision = directplay, want a transcode/remux; plan=%+v", plan)
	}
	if playback.prepared != 1 {
		t.Fatalf("kind prepares = %d, want 1", playback.prepared)
	}

	// Source is H.264 in mkv: the client direct-plays it.
	h264 := core.MediaInfo{Container: "mkv", Duration: 100 * time.Second, Size: 100 << 20,
		Streams: []core.StreamInfo{
			{Index: 0, Type: "video", Codec: "h264", Width: 1920, Height: 1080},
			{Index: 1, Type: "audio", Codec: "eac3", Channels: 6},
		}}
	b2, _ := newBroker(h264)
	plan2, err := b2.Plan(ctx, frontend.PlanRequest{Viewer: &core.Viewer{ID: 1}, Device: core.Device{ID: "d"}, Protocol: "plex", Item: key, Caps: caps, Intent: frontend.PlayIntent{AllowTranscoding: &allow, Options: core.DecideOptions{DirectPlay: true, DirectStream: true, AudioStream: -1, SubtitleStream: -1}}})
	if err != nil {
		t.Fatalf("Plan (h264): %v", err)
	}
	if plan2.Decision.Kind != core.DecisionDirectPlay {
		t.Fatalf("h264 plan decision = %q, want directplay", plan2.Decision.Kind)
	}
}
