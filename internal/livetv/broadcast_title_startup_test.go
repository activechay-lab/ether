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

func (titleStartupPlanner) startupLead() time.Duration { return 12 * time.Second }

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

// Reproduce lab.31: tune inside a programme, a title plate takes 1.08s,
// its master is ready at 2.5s, and programme decoding finishes at 3.8s while the output builds 16s of lead.
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
	synctest.Test(t, func(t *testing.T) {
		origin := time.Now()
		f := smallBroadcastFormat()
		programme := broadcastPlan{id: "programme", programme: true, element: "programme", start: origin.Add(-4 * time.Minute), end: origin.Add(time.Hour), open: coldHoldOpen(f, 90, 3800*time.Millisecond)}
		p := titleStartupPlanner{coldHoldPlanner{
			elementBroadcastPlanner: elementBroadcastPlanner{programme, broadcastPlan{id: "after", start: programme.end}},
			screenPlan:              broadcastPlan{id: "off-air", open: coldHoldOpen(f, 10, 0)},
			card: broadcastPlan{id: "tonight-card", element: "tonight", open: func(context.Context) (transcode.Decoder, func(), error) {
				return &coldTitleDecoder{delayedMomentDecoder: &delayedMomentDecoder{broadcastFakeDecoder: &broadcastFakeDecoder{format: f, value: 30, closed: make(chan struct{})}, ready: origin.Add(1080 * time.Millisecond)}, masterAt: origin.Add(2500 * time.Millisecond)}, nil, nil
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
				t.Fatalf("tick %d aired black/off-air: %d", i, frame.Video[0])
			}
		}
		if animatedAt != 120 {
			t.Fatalf("animation cut at %d, want segment boundary 120", animatedAt)
		}
		t.Logf("animated at tick %d; programme at tick %d", animatedAt, programmeAt)
		if programmeAt < 450 {
			t.Fatalf("programme cut at %d, want roughly 16s (at least 450 ticks)", programmeAt)
		}
		if animatedAt >= programmeAt {
			t.Fatalf("title animation %d must precede programme %d", animatedAt, programmeAt)
		}
	})
}
