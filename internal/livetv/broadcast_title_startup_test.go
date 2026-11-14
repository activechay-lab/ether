package livetv

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type titleRequestGraphics struct {
	testGraphics
	request core.ClipRequest
}

func (g *titleRequestGraphics) Clip(ctx context.Context, request core.ClipRequest) (core.ClipRef, bool) {
	g.request = request
	return g.testGraphics.Clip(ctx, request)
}

type titleStartupPlanner struct{ coldHoldPlanner }

func (titleStartupPlanner) startupLead() time.Duration { return 10 * time.Second }
func (p titleStartupPlanner) introPlan(time.Time) (broadcastPlan, time.Duration) {
	return p.card, 4 * time.Second
}

type coldTitleDecoder struct {
	*delayedMomentDecoder
	masterAt time.Time
	tick     int
	animated bool
}

func (d *coldTitleDecoder) startupBurstReady() bool { return !time.Now().Before(d.masterAt) }
func (d *coldTitleDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	f, err := d.delayedMomentDecoder.Next(ctx)
	transition := !d.animated && d.startupBurstReady()
	if transition {
		d.animated = true
	}
	if d.animated && len(f.Video) > 0 {
		f.Video[0] = 200
	}
	f.GraphicsTransition = transition
	d.tick++
	return f, err
}

// Reproduce lab.33: plate at 1.08s, master 1.2s later, programme at 6.4s,
// and speculative startup lead of 14s. The encoded title must keep flowing until the next ready segment cut.
// The startup burst must not spend the title interval before the master exists.
func TestColdTuneTitleStartupSequence(t *testing.T) {
	t.Run("real planner binds current programme title", func(t *testing.T) {
		now := time.Now()
		enabled := true
		graphics := &titleRequestGraphics{testGraphics: testGraphics{cached: true}}
		service := &Service{o: Options{Graphics: graphics}}
		format := smallBroadcastFormat()
		runner := &broadcastFakeRunner{decoder: func(_ context.Context, req transcode.DecoderRequest) (transcode.Decoder, error) {
			if !req.Loop {
				t.Fatal("cold title must loop until programme arrives")
			}
			return &broadcastFakeDecoder{format: format, closed: make(chan struct{}), value: 30}, nil
		}}
		planner := &scheduleBroadcastPlanner{s: service, runner: runner, format: format, channel: Channel{ID: "riot", Name: "RIOT 90", Look: &ChannelLook{LookID: "showcase-90", Tonight: &enabled}}, window: []Programme{{ID: "current", Start: now.Add(-4 * time.Minute), End: now.Add(time.Hour), Item: Item{Key: 1, Title: "NewsRadio"}}}}
		intro, duration := planner.introPlan(now)
		if intro.id == "" || intro.fill || duration != 4*time.Second || !planner.joinAt.Equal(now.Add(duration)) {
			t.Fatalf("warm title lacks an exclusive segment/seek: %+v %s join=%s", intro, duration, planner.joinAt)
		}
		graphics.cached = false
		coldIntro, coldDuration := planner.introPlan(now)
		if coldIntro.id != intro.id || coldDuration != duration {
			t.Fatal("cold and warm opening intervals differ")
		}
		graphics.cached = true
		plan := planner.momentScreenAt(now)
		decoder, release, err := plan.open(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer decoder.Close()
		if release != nil {
			defer release()
		}
		if graphics.request.Moment != "tonight" || graphics.request.Bind["next.title"] != "NewsRadio" {
			t.Fatalf("wrong cold title: %+v", graphics.request)
		}
	})
	t.Run("warm title owns its complete opening segment", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := smallBroadcastFormat()
			p := introFillPlanner{broadcastFakePlanner: broadcastFakePlanner{plan: broadcastPlan{id: "programme", programme: true, open: coldHoldOpen(f, 90, 1100*time.Millisecond)}}, intro: broadcastPlan{id: "tonight", element: "tonight", open: coldHoldOpen(f, 30, 500*time.Millisecond)}}
			runner := &broadcastFakeRunner{}
			b, err := newBroadcast(t.Context(), runner, p, f)
			if err != nil {
				t.Fatal(err)
			}
			defer b.close()
			if _, err = b.addRendition(t.Context(), "main", core.Decision{}); err != nil {
				t.Fatal(err)
			}
			b.start()
			time.Sleep(6 * time.Second)
			synctest.Wait()
			b.close()
			frames := runner.encoders[0].snapshot()
			if len(frames) < 150 {
				t.Fatalf("only %d frames", len(frames))
			}
			for tick := 0; tick < 120; tick++ {
				if frames[tick].Video[0] != 30 {
					t.Fatalf("warm tick %d: got %d before title ends", tick, frames[tick].Video[0])
				}
			}
			if frames[140].Video[0] != 90 {
				t.Fatalf("programme did not follow title: %d", frames[140].Video[0])
			}
		})
	})
	synctest.Test(t, func(t *testing.T) {
		origin := time.Now()
		f := smallBroadcastFormat()
		programme := broadcastPlan{id: "programme", programme: true, element: "programme", start: origin.Add(-4 * time.Minute), end: origin.Add(time.Hour), open: coldHoldOpen(f, 90, 6400*time.Millisecond)}
		p := titleStartupPlanner{coldHoldPlanner{
			elementBroadcastPlanner: elementBroadcastPlanner{programme, broadcastPlan{id: "after", start: programme.end}},
			screenPlan:              broadcastPlan{id: "off-air", open: coldHoldOpen(f, 10, 0)},
			card: broadcastPlan{id: "tonight-card", element: "tonight", open: func(context.Context) (transcode.Decoder, func(), error) {
				return &coldTitleDecoder{delayedMomentDecoder: &delayedMomentDecoder{broadcastFakeDecoder: &broadcastFakeDecoder{format: f, value: 30, closed: make(chan struct{})}, ready: origin.Add(1080 * time.Millisecond)}, masterAt: origin.Add(2280 * time.Millisecond)}, nil, nil
			}},
		}}
		runner := &broadcastFakeRunner{}
		b, err := newBroadcast(t.Context(), runner, p, f)
		if err != nil {
			t.Fatal(err)
		}
		defer b.close()
		if _, err = b.addRendition(t.Context(), "main", core.Decision{}); err != nil {
			t.Fatal(err)
		}
		b.start()
		time.Sleep(1500 * time.Millisecond)
		synctest.Wait()
		competingOpening := b.momentCard != nil
		first := runner.encoders[0].snapshot()
		if len(first) == 0 {
			t.Fatal("no first frame within target duration")
		}
		for i, frame := range first {
			if frame.Video[0] != 30 {
				t.Fatalf("startup tick %d: got %d, want complete title 30", i, frame.Video[0])
			}
		}
		time.Sleep(20 * time.Second)
		synctest.Wait()
		b.close()
		frames := runner.encoders[0].snapshot()
		animatedAt, programmeAt := -1, -1
		for i, frame := range frames {
			switch frame.Video[0] {
			case 200:
				if animatedAt < 0 {
					animatedAt = i
				}
			case 90:
				if programmeAt < 0 {
					programmeAt = i
				}
			case 30:
			default:
				if i < 120 || frame.Video[0] < 90 || frame.Video[0] > 200 {
					t.Fatalf("tick %d aired unexpected picture: %d", i, frame.Video[0])
				}
			}
		}

		t.Logf("animated at tick %d; programme at tick %d", animatedAt, programmeAt)
		if programmeAt < 240 || programmeAt > 250 {
			t.Fatalf("programme cut at %d, want next ready segment boundary plus crossfade (240..250)", programmeAt)
		}
		if animatedAt != 120 {
			t.Fatalf("animation cut at %d, want segment boundary120", animatedAt)
		}
		if competingOpening {
			t.Fatal("opening title also opened a competing cold-hold reader")
		}

		if animatedAt >= programmeAt {
			t.Fatalf("title animation %d must precede programme %d", animatedAt, programmeAt)
		}
	})
}
