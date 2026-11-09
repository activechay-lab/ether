package sessions

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/frontend"
)

type warmRecordingPlayback struct {
	*fakePlayback
	prepared, warmed, discarded []core.ClientCaps
}

func (p *warmRecordingPlayback) PrepareSurface(ctx context.Context, u *core.User, key core.RatingKey, c core.ClientCaps, release string, exact bool) (*core.PlaySession, error) {
	p.prepared = append(p.prepared, c)
	return p.fakePlayback.PrepareSurface(ctx, u, key, c, release, exact)
}
func (p *warmRecordingPlayback) Prewarm(_ context.Context, _ *core.User, _ core.RatingKey, c core.ClientCaps, _ core.PrewarmOptions) bool {
	p.warmed = append(p.warmed, c)
	return true
}
func (p *warmRecordingPlayback) DiscardWarm(_ *core.User, c core.ClientCaps, _ core.RatingKey) {
	p.discarded = append(p.discarded, c)
}

// A detail warm-up must be keyed and profiled exactly like the Prepare that
// later takes it, and only with caps the device declared for playback.
func TestDetailWarmUsesDeclaredPlayCaps(t *testing.T) {
	ctx := context.Background()
	active := func(_ context.Context, id int64) (*core.Viewer, error) { return &core.Viewer{ID: id}, nil }
	events := NewEventBus(nil, active)
	defer events.Close()
	pb := &warmRecordingPlayback{fakePlayback: &fakePlayback{sessions: map[string]*core.PlaySession{}, limit: 4}}
	b, err := NewBroker(BrokerDeps{Events: events, Playback: pb, Transcoder: fakeBrokerTranscoder{}, Progress: &fakeProgress{}, ActiveViewer: active, CanPlay: func(*core.Viewer, bool, time.Time) bool { return true }, BitrateLimit: func(*core.Viewer) int { return 40000 }, OnTimeline: func(*core.Viewer, core.RatingKey, time.Duration, time.Duration, string) {}, Lookup: func(_ context.Context, _ *core.Viewer, _ core.Device, k core.RatingKey) (*core.Title, error) {
		return &core.Title{Key: k, Type: core.TypeMovie}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	viewer, dev := &core.Viewer{ID: 1}, core.Device{ID: "tv"}
	generic := core.ClientCaps{Product: "App", ClientID: "tv"}
	detail := frontend.PlanRequest{Viewer: viewer, Device: dev, Protocol: "jellyfin", Item: core.MovieKey(2), Caps: generic}
	if b.WarmDetail(ctx, detail) || len(pb.warmed) != 0 {
		t.Fatal("warmed a device that never declared playback caps")
	}
	declared := core.ClientCaps{Product: "App", ClientID: "tv", VideoCodecs: []string{"hevc", "h264"}, MaxHeight: 2160, HDR10: true, MaxBitrateKbps: 80000}
	if _, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: dev, Protocol: "jellyfin", Item: core.MovieKey(1), Caps: declared, Intent: frontend.PlayIntent{DeclaredCaps: true, Options: core.DecideOptions{DirectPlay: true}}}); err != nil {
		t.Fatal(err)
	}
	if !b.WarmDetail(ctx, detail) || len(pb.warmed) != 1 || len(pb.prepared) != 1 {
		t.Fatalf("warmed=%d prepared=%d", len(pb.warmed), len(pb.prepared))
	}
	if !reflect.DeepEqual(pb.warmed[0], pb.prepared[0]) || pb.warmed[0].MaxBitrateKbps != 40000 {
		t.Fatalf("warm caps differ from Prepare's:\nwarm    %+v\nprepare %+v", pb.warmed[0], pb.prepared[0])
	}
	remote := detail
	remote.Caps.Remote = true
	if b.WarmDetail(ctx, remote) || len(pb.warmed) != 1 {
		t.Fatal("warmed across a network-locality change")
	}
	wider := declared
	wider.DolbyVision = true
	if _, err := b.Plan(ctx, frontend.PlanRequest{Viewer: viewer, Device: dev, Protocol: "jellyfin", Item: core.MovieKey(2), Caps: wider, Intent: frontend.PlayIntent{Estimate: true, DeclaredCaps: true}}); err != nil {
		t.Fatal(err)
	}
	if len(pb.discarded) != 1 || !reflect.DeepEqual(pb.discarded[0], pb.warmed[0]) {
		t.Fatalf("widened caps did not discard the warm entry: %+v", pb.discarded)
	}
}

// Frontends reach the broker through the surface binding, which must not
// hide the warmer.
func TestSurfaceBindingForwardsDetailWarmer(t *testing.T) {
	if _, ok := BindSurface(&Broker{}, "jellyfin").(frontend.DetailWarmer); !ok {
		t.Fatal("BindSurface hides frontend.DetailWarmer")
	}
}
