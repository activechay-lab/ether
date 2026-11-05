package livetv

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

type broadcastFakeEncoder struct {
	mu     sync.Mutex
	frames []transcode.RawFrame
	closed bool
	output fakeHLS
}

func (e *broadcastFakeEncoder) Write(ctx context.Context, f transcode.RawFrame) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	copyFrame := transcode.RawFrame{Video: append([]byte(nil), f.Video...), PCM: append([]byte(nil), f.PCM...)}
	e.mu.Lock()
	e.frames = append(e.frames, copyFrame)
	e.mu.Unlock()
	return nil
}
func (e *broadcastFakeEncoder) Segments() transcode.SegmentIndex { return fakeBroadcastIndex{} }
func (e *broadcastFakeEncoder) Close() error                     { e.mu.Lock(); e.closed = true; e.mu.Unlock(); return nil }
func (e *broadcastFakeEncoder) snapshot() []transcode.RawFrame {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]transcode.RawFrame(nil), e.frames...)
}

type broadcastFakeRunner struct {
	mu       sync.Mutex
	encoders []*broadcastFakeEncoder
	requests []transcode.EncoderRequest
	decoder  func(context.Context, transcode.DecoderRequest) (transcode.Decoder, error)
}

func (r *broadcastFakeRunner) StartEncoder(_ context.Context, req transcode.EncoderRequest) (transcode.ContinuousEncoder, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e := &broadcastFakeEncoder{}
	r.encoders = append(r.encoders, e)
	r.requests = append(r.requests, req)
	return e, nil
}
func (r *broadcastFakeRunner) StartDecoder(ctx context.Context, req transcode.DecoderRequest) (transcode.Decoder, error) {
	return r.decoder(ctx, req)
}

type broadcastFakePlanner struct {
	plan broadcastPlan
	next *broadcastPlan
}

func (p broadcastFakePlanner) plans(time.Time) (broadcastPlan, *broadcastPlan) { return p.plan, p.next }

type broadcastFakeDecoder struct {
	origin time.Time
	format transcode.RawFormat
	closed chan struct{}
	once   sync.Once
	tick   int64
	stall  bool
}

func (d *broadcastFakeDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	if d.stall {
		elapsed := time.Since(d.origin)
		if elapsed >= time.Second && elapsed < 21*time.Second {
			select {
			case <-ctx.Done():
				return transcode.RawFrame{}, ctx.Err()
			case <-d.closed:
				return transcode.RawFrame{}, io.EOF
			case <-time.After(21*time.Second - elapsed):
			}
		}
	}
	select {
	case <-ctx.Done():
		return transcode.RawFrame{}, ctx.Err()
	case <-d.closed:
		return transcode.RawFrame{}, io.EOF
	default:
	}
	frame := blackFrame(d.format, tickSamples(d.format, d.tick))
	d.tick++
	for i := range frame.Video {
		frame.Video[i] = 80
	}
	for i := 0; i < len(frame.PCM); i += 2 {
		binary.LittleEndian.PutUint16(frame.PCM[i:], 10000)
	}
	return frame, nil
}
func (d *broadcastFakeDecoder) Close() error { d.once.Do(func() { close(d.closed) }); return nil }
func smallBroadcastFormat() transcode.RawFormat {
	return transcode.RawFormat{Width: 2, Height: 2, FPSNum: 30, FPSDen: 1, SampleRate: 48000, Channels: 2, PixelFormat: "yuv420p"}
}

func TestBroadcastStallAndRecoveryKeepsOneOutput(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		origin := time.Now()
		format := smallBroadcastFormat()
		runner := &broadcastFakeRunner{}
		opened := 0
		plan := broadcastPlan{id: "programme", open: func(context.Context) (transcode.Decoder, func(), error) {
			opened++
			return &broadcastFakeDecoder{origin: origin, format: format, closed: make(chan struct{}), stall: true}, nil, nil
		}}
		screenFrame := blackFrame(format, 1600)
		for i := range screenFrame.Video {
			screenFrame.Video[i] = 32
		}
		screen := &frameRing{frames: []transcode.RawFrame{{Video: screenFrame.Video}}, pcm: screenFrame.PCM}
		b, err := newBroadcast(context.Background(), runner, broadcastFakePlanner{plan: plan}, format, screen)
		if err != nil {
			t.Fatal(err)
		}
		defer b.close()
		if _, err := b.addRendition(context.Background(), "main", core.Decision{MaxHeight: 720}); err != nil {
			t.Fatal(err)
		}
		b.start()
		time.Sleep(40 * time.Second)
		synctest.Wait()
		b.close()
		if opened != 3 {
			t.Fatalf("decoder attempts=%d, want initial and two bounded retries", opened)
		}
		if len(runner.encoders) != 1 {
			t.Fatal("encoder restarted")
		}
		frames := runner.encoders[0].snapshot()
		if len(frames) < 1199 || len(frames) > 1201 {
			t.Fatalf("tick gap: %d frames", len(frames))
		}
		samples := 0
		seenReal, seenScreen, recovered := false, false, false
		for _, frame := range frames {
			samples += len(frame.PCM) / 4
			switch frame.Video[0] {
			case 80:
				if seenScreen {
					recovered = true
				}
				seenReal = true
			case 32:
				if seenReal {
					seenScreen = true
				}
			}
		}
		if !seenReal || !seenScreen || !recovered {
			t.Fatal("missing real→screen→real transition")
		}
		if samples != len(frames)*1600 {
			t.Fatal("PCM drift")
		}
		if runner.requests[0].DVRWindow != 30*time.Minute {
			t.Fatal("DVR contract lost")
		}
	})
}

func TestBroadcastRegistrySharesViewersAndStopsAfterGrace(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		runner := &broadcastFakeRunner{}
		registry := &broadcastRegistry{}
		defer registry.close()
		format := smallBroadcastFormat()
		created := 0
		create := func() (*broadcast, error) {
			created++
			return newBroadcast(context.Background(), runner, broadcastFakePlanner{plan: broadcastPlan{id: "screen"}}, format, nil)
		}
		a, err := registry.acquire(context.Background(), broadcastKey{channelID: "one"}, "main", core.Decision{MaxHeight: 720}, create)
		if err != nil {
			t.Fatal(err)
		}
		b, err := registry.acquire(context.Background(), broadcastKey{channelID: "one"}, "main", core.Decision{MaxHeight: 720}, create)
		if err != nil {
			t.Fatal(err)
		}
		if created != 1 || a.broadcast != b.broadcast || len(runner.encoders) != 1 {
			t.Fatal("duplicate shared output")
		}
		a.close()
		time.Sleep(61 * time.Second)
		synctest.Wait()
		if _, err := b.output(); err != nil {
			t.Fatal("remaining viewer lost output")
		}
		b.close()
		time.Sleep(59 * time.Second)
		synctest.Wait()
		if _, err := b.output(); err != nil {
			t.Fatal("idle grace cut short")
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if _, err := b.output(); err == nil {
			t.Fatal("idle output remained live")
		}
		personal, err := registry.acquire(context.Background(), broadcastKey{channelID: "one", viewerID: 2}, "personal", core.Decision{}, create)
		if err != nil {
			t.Fatal(err)
		}
		defer personal.close()
		if created != 2 {
			t.Fatal("personal channel shared across viewers")
		}
	})
}

func TestFractionalTickPCMAndCrossfade(t *testing.T) {
	f := smallBroadcastFormat()
	f.FPSNum, f.FPSDen = 30000, 1001
	var sum int
	for n := int64(0); n < 30000; n++ {
		sum += tickSamples(f, n)
	}
	if sum != 48000*1001 {
		t.Fatal("fractional PCM accumulator drift", sum)
	}
	old := make([]byte, 1600*4)
	next := make([]byte, len(old))
	for i := 0; i < len(old); i += 2 {
		binary.LittleEndian.PutUint16(old[i:], 10000)
	}
	fade := pcmFade{}
	fade.start(old, f)
	fade.apply(next, 2)
	if int16(binary.LittleEndian.Uint16(next)) != 10000 || fade.at != 1600 {
		t.Fatal("crossfade first tick")
	}
	tail := make([]byte, len(old))
	fade.apply(tail, 2)
	if fade.at != 1920 || int16(binary.LittleEndian.Uint16(tail[400*4:])) != 0 {
		t.Fatal("crossfade not exactly 40 ms")
	}
}

type timedBroadcastPlanner struct{ first, second broadcastPlan }

func (p timedBroadcastPlanner) plans(now time.Time) (broadcastPlan, *broadcastPlan) {
	if now.Before(p.second.start) {
		return p.first, &p.second
	}
	return p.second, nil
}

type countedBroadcastDecoder struct {
	transcode.Decoder
	calls *int
}

func (d countedBroadcastDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	*d.calls++
	return d.Decoder.Next(ctx)
}
func TestBroadcastPrewarmsTwentySecondsBeforeCut(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		format := smallBroadcastFormat()
		origin := time.Now()
		runner := &broadcastFakeRunner{}
		var secondOpened time.Time
		nextCalls := 0
		first := broadcastPlan{id: "one", start: origin, end: origin.Add(25 * time.Second), open: func(context.Context) (transcode.Decoder, func(), error) {
			return &broadcastFakeDecoder{origin: origin, format: format, closed: make(chan struct{})}, nil, nil
		}}
		second := broadcastPlan{id: "two", start: first.end, end: first.end.Add(time.Hour), open: func(context.Context) (transcode.Decoder, func(), error) {
			secondOpened = time.Now()
			return countedBroadcastDecoder{Decoder: &broadcastFakeDecoder{origin: origin, format: format, closed: make(chan struct{})}, calls: &nextCalls}, nil, nil
		}}
		b, err := newBroadcast(context.Background(), runner, timedBroadcastPlanner{first: first, second: second}, format, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer b.close()
		if _, err := b.addRendition(context.Background(), "main", core.Decision{}); err != nil {
			t.Fatal(err)
		}
		b.start()
		time.Sleep(6 * time.Second)
		synctest.Wait()
		if secondOpened.Sub(origin) != 5*time.Second {
			t.Fatal("prewarm timing", secondOpened.Sub(origin))
		}
		if nextCalls != 1 {
			t.Fatal("prewarm read beyond held first frame", nextCalls)
		}
		time.Sleep(20 * time.Second)
		synctest.Wait()
		if nextCalls < 2 {
			t.Fatal("next input did not activate at boundary")
		}
	})
}

func TestProviderBroadcastReservationIsReleasedWhileWaiting(t *testing.T) {
	s := testService(t, Options{})
	release, err := s.reserveBroadcastProvider("account", "one", "first", 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.reserveBroadcastProvider("account", "two", "second", 1); err != ErrProviderBusy {
		t.Fatal("provider account cap exceeded", err)
	}
	release()
	release()
	release, err = s.reserveBroadcastProvider("account", "two", "second", 1)
	if err != nil {
		t.Fatal("waiting output retained provider slot", err)
	}
	release()
}

type fakeBroadcastIndex struct{}

func (fakeBroadcastIndex) Latest() (transcode.Segment, bool)               { return transcode.Segment{}, false }
func (fakeBroadcastIndex) Window(time.Time, time.Time) []transcode.Segment { return nil }
func (fakeBroadcastIndex) Since(int64, int) []transcode.Segment            { return nil }
func (fakeBroadcastIndex) Wait(ctx context.Context, _ int64) (transcode.Segment, error) {
	<-ctx.Done()
	return transcode.Segment{}, ctx.Err()
}
func (fakeBroadcastIndex) ServeTS(http.ResponseWriter, *http.Request, int64)   {}
func (fakeBroadcastIndex) ServeInit(http.ResponseWriter, *http.Request)        {}
func (fakeBroadcastIndex) ServeFMP4(http.ResponseWriter, *http.Request, int64) {}
func (fakeBroadcastIndex) AttachTS(context.Context) (io.ReadCloser, error)     { return nil, io.EOF }
func (fakeBroadcastIndex) Format() (string, int, int, int) {
	return "avc1.640028,mp4a.40.2", 2, 2, 1000
}

func TestBroadcastRingLoopsAudioAcrossFractionalSeams(t *testing.T) {
	f := smallBroadcastFormat()
	f.FPSNum, f.FPSDen = 30000, 1001
	pcm := make([]byte, 12)
	for i := 0; i < 6; i++ {
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(i+1))
	}
	r := &frameRing{frames: []transcode.RawFrame{{Video: make([]byte, 6)}}, pcm: pcm}
	var bytes int
	for n := int64(0); n < 30; n++ {
		frame := r.next(tickSamples(f, n), 2)
		for i := 0; i < len(frame.PCM); i += 2 {
			want := int16((bytes/2)%6 + 1)
			if got := int16(binary.LittleEndian.Uint16(frame.PCM[i:])); got != want {
				t.Fatal("ring audio seam", got, want)
			}
			bytes += 2
		}
	}
}
