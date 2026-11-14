package livetv

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type programmeSourcePlayback struct {
	fakePlayback
	caps []core.ClientCaps
}

func (p *programmeSourcePlayback) PrepareChannel(ctx context.Context, u *core.User, k core.RatingKey, c core.ClientCaps, g string) (*core.PlaySession, error) {
	p.caps = append(p.caps, c)
	ps, err := p.fakePlayback.PrepareChannel(ctx, u, k, c, g)
	if k == core.MovieKey(2) {
		ps.Info.Duration = 2 * time.Hour
	}
	ps.Resolution = &core.Resolution{Release: core.Release{ID: ps.ID}}
	return ps, err
}

type programmeSourceDecoder struct {
	*broadcastFakeDecoder
	life    context.Context
	blocked bool
}

func (d *programmeSourceDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	if d.blocked {
		<-ctx.Done()
		return transcode.RawFrame{}, ctx.Err()
	}
	if d.life.Err() != nil {
		return transcode.RawFrame{}, d.life.Err()
	}
	return d.broadcastFakeDecoder.Next(ctx)
}
func TestProgrammeSourceRecovery(t *testing.T) {
	t.Run("first picture admission reaches substitution", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			origin := time.Now()
			f := smallBroadcastFormat()
			pb := &programmeSourcePlayback{}
			s := testService(t, Options{Playback: pb})
			c := testChannel()
			c.Items[1].DurationMS = 2 * 60 * 60 * 1000
			c.Fallback = []Item{c.Items[1]}
			runner := &broadcastFakeRunner{decoder: func(ctx context.Context, r transcode.DecoderRequest) (transcode.Decoder, error) {
				return &programmeSourceDecoder{broadcastFakeDecoder: &broadcastFakeDecoder{format: f, closed: make(chan struct{}), value: 90}, life: ctx, blocked: strings.HasSuffix(r.InputURL, "/1") || strings.HasSuffix(r.InputURL, "/2")}, nil
			}}
			p := &scheduleBroadcastPlanner{s: s, channel: c, user: core.User{ID: 1, IsOwner: true}, format: f, runner: runner}
			prog := Programme{ID: "scheduled", ChannelID: c.ID, Start: origin.Add(-18 * time.Minute), End: origin.Add(time.Hour), Item: c.Items[0]}
			d, release, err := p.programme(prog).open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			defer release()
			if time.Since(origin) > 20*time.Second {
				t.Fatal("substitution missed deadline")
			}
			if len(pb.prepared) != 3 || pb.prepared[2].Key != c.Items[1].Key {
				t.Fatalf("did not substitute after stalled releases: %+v", pb.prepared)
			}
			if len(pb.caps[1].BroadcastExclude) != 1 || pb.caps[1].BroadcastExclude[0] != "1" {
				t.Fatal("stalled release was not excluded")
			}
			if _, err = d.Next(t.Context()); err != nil {
				t.Fatalf("opening deadline canceled admitted decoder: %v", err)
			}
		})
	})
	t.Run("duration offset substitutes without seeking EOF", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			now := time.Now()
			f := smallBroadcastFormat()
			pb := &programmeSourcePlayback{}
			s := testService(t, Options{Playback: pb})
			c := testChannel()
			c.Items[1].DurationMS = 2 * 60 * 60 * 1000
			c.Fallback = []Item{c.Items[1]}
			runner := &broadcastFakeRunner{decoder: func(ctx context.Context, r transcode.DecoderRequest) (transcode.Decoder, error) {
				if strings.HasSuffix(r.InputURL, "/1") || strings.HasSuffix(r.InputURL, "/2") {
					t.Fatal("scheduled source sought at EOF")
				}
				return &programmeSourceDecoder{broadcastFakeDecoder: &broadcastFakeDecoder{format: f, closed: make(chan struct{}), value: 90}, life: ctx}, nil
			}}
			p := &scheduleBroadcastPlanner{s: s, channel: c, user: core.User{ID: 1, IsOwner: true}, format: f, runner: runner}
			prog := Programme{ID: "expired-runtime", ChannelID: c.ID, Start: now.Add(-time.Hour), End: now.Add(time.Hour), Item: c.Items[0]}
			d, release, err := p.programme(prog).open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			defer release()
			if pb.prepared[len(pb.prepared)-1].Key != c.Items[1].Key {
				t.Fatal("duration-length offset did not substitute")
			}
		})
	})
	t.Run("runtime retry retains scheduled offset", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			now := time.Now()
			f := smallBroadcastFormat()
			pb := &programmeSourcePlayback{}
			s := testService(t, Options{Playback: pb})
			c := testChannel()
			var offsets []time.Duration
			runner := &broadcastFakeRunner{decoder: func(ctx context.Context, r transcode.DecoderRequest) (transcode.Decoder, error) {
				offsets = append(offsets, r.Offset)
				return &programmeSourceDecoder{broadcastFakeDecoder: &broadcastFakeDecoder{format: f, closed: make(chan struct{}), value: 90}, life: ctx}, nil
			}}
			p := &scheduleBroadcastPlanner{s: s, channel: c, user: core.User{ID: 1, IsOwner: true}, format: f, runner: runner}
			prog := Programme{ID: "scheduled", ChannelID: c.ID, Start: now.Add(-18 * time.Minute), End: now.Add(time.Hour), Item: c.Items[0]}
			plan := p.programme(prog)
			d, release, err := plan.open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			d.Close()
			release()
			if retry := plan.failed(errors.New("decoder stalled")); retry.Sub(time.Now()) > time.Second {
				t.Fatal("runtime failover delayed")
			}
			time.Sleep(time.Second)
			d, release, err = plan.open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer d.Close()
			defer release()
			if len(pb.caps[1].BroadcastExclude) != 1 {
				t.Fatal("retry reused failed release")
			}
			if offsets[1] < 18*time.Minute || offsets[1] > 19*time.Minute {
				t.Fatalf("retry sought outside programme position: %v", offsets)
			}
		})
	})
	t.Run("stalled programme never airs off air", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			origin := time.Now()
			f := smallBroadcastFormat()
			programme := broadcastPlan{id: "programme", programme: true, mediaKey: core.MovieKey(1), start: origin, end: origin.Add(time.Hour), open: func(context.Context) (transcode.Decoder, func(), error) {
				return &broadcastFakeDecoder{origin: origin, format: f, closed: make(chan struct{}), value: 90, stall: true}, nil, nil
			}}
			planner := coldHoldPlanner{elementBroadcastPlanner: elementBroadcastPlanner{programme}, screenPlan: broadcastPlan{id: "offair", open: coldHoldOpen(f, 10, 0)}}
			b, runner := newColdHoldBroadcast(t, planner, f)
			time.Sleep(25 * time.Second)
			synctest.Wait()
			b.close()
			frames := runner.encoders[0].snapshot()
			if len(frames) < 700 {
				t.Fatal("channel clock stopped")
			}
			for n, frame := range frames {
				if frame.Video[0] == 10 {
					t.Fatalf("off air at frame %d during recovery", n)
				}
			}
		})
	})
}
