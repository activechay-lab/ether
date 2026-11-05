package transcode

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type sourceProbeTestRunner struct {
	run func(context.Context, Command) error
}

func (r sourceProbeTestRunner) Run(ctx context.Context, c Command) error { return r.run(ctx, c) }
func (sourceProbeTestRunner) Start(Command) (Process, error) {
	return nil, errors.New("unexpected encoder/session start")
}
func sourceTestProber(t *testing.T, run func(context.Context, Command) error, timeout time.Duration) *SourceProber {
	t.Helper()
	path, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewSourceProber(SourceProbeOptions{FFprobePath: path, Runner: sourceProbeTestRunner{run}, Timeout: timeout, Concurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Close() })
	return p
}

func TestSourceProberExactMetadataAndInputBoundary(t *testing.T) {
	var calls atomic.Int32
	observed := strings.Replace(string(loadGolden(t, "mp4_h264_aac.json")), `"codec_name": "h264"`, `"codec_name": "mpeg4"`, 1)
	p := sourceTestProber(t, func(ctx context.Context, c Command) error {
		calls.Add(1)
		if c.Args[len(c.Args)-1] != "http://127.0.0.1:1234/source/exact-capability" || !hasArg(c.Args, "-format_whitelist") || !hasArg(c.Args, "-protocol_whitelist") {
			t.Error("lost existing probe input safety/identity")
		}
		_, err := fmt.Fprint(c.Stdout, observed)
		return err
	}, time.Second)
	for _, input := range []string{"/tmp/file.mp4", "http://localhost:1234/x", "http://10.0.0.1:1234/x", "https://127.0.0.1:1234/x", "http://user:secret@127.0.0.1:1234/x", "http://127.0.0.1:1234/x#fragment"} {
		if _, err := p.Probe(context.Background(), input); err == nil {
			t.Errorf("untrusted input admitted: %s", input)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("rejected input started a probe")
	}
	info, err := p.Probe(context.Background(), "http://127.0.0.1:1234/source/exact-capability")
	if err != nil || info == nil || info.Estimated || info.Duration <= 0 || len(info.Streams) == 0 {
		t.Fatalf("lost observed metadata: %v %+v", err, info)
	}
	want, err := ParseProbe([]byte(observed))
	if err != nil || info.Video().Codec != want.Video().Codec {
		t.Fatal("codec metadata rewritten")
	}
}

func TestSourceProberCloseJoinsAndSerializesChildren(t *testing.T) {
	entered, cancelled, join := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(join) }) }
	defer release()
	p := sourceTestProber(t, func(ctx context.Context, c Command) error {
		if calls.Add(1) != 1 {
			t.Error("concurrent child exceeded capacity")
		}
		close(entered)
		<-ctx.Done()
		close(cancelled)
		<-join
		return ctx.Err()
	}, time.Second)
	first := make(chan error, 1)
	go func() { _, err := p.Probe(context.Background(), "http://127.0.0.1:1234/source/cap"); first <- err }()
	<-entered
	second := make(chan error, 1)
	go func() { _, err := p.Probe(context.Background(), "http://127.0.0.1:1234/source/cap"); second <- err }()
	closed := make(chan struct{})
	go func() { _ = p.Close(); close(closed) }()
	<-cancelled
	select {
	case <-closed:
		t.Fatal("Close returned before child/readers joined")
	default:
	}
	release()
	<-closed
	if !errors.Is(<-first, ErrSourceProbeClosed) || !errors.Is(<-second, ErrSourceProbeClosed) || calls.Load() != 1 || len(p.slots) != 0 {
		t.Fatal("probe responsibilities/slots not joined")
	}
	if _, err := p.Probe(context.Background(), "http://127.0.0.1:1234/source/cap"); !errors.Is(err, ErrSourceProbeClosed) {
		t.Fatal("closed prober admitted work")
	}
}

func TestSourceProberBoundsAndSanitizesOutputAndRuntime(t *testing.T) {
	const capability = "http://127.0.0.1:1234/source/private-capability"
	p := sourceTestProber(t, func(ctx context.Context, c Command) error {
		_, _ = fmt.Fprint(c.Stderr, capability)
		_, _ = fmt.Fprint(c.Stdout, strings.Repeat("x", (2<<20)+1))
		<-ctx.Done()
		return ctx.Err()
	}, time.Second)
	_, err := p.Probe(context.Background(), capability)
	if !errors.Is(err, ErrSourceProbe) || strings.Contains(err.Error(), "private-capability") {
		t.Fatalf("unbounded/leaky output error: %v", err)
	}
	timed := sourceTestProber(t, func(ctx context.Context, c Command) error { <-ctx.Done(); return ctx.Err() }, 10*time.Millisecond)
	if _, err := timed.Probe(context.Background(), capability); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("total runtime not bounded: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := timed.Probe(ctx, capability); !errors.Is(err, context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", err)
	}
}
