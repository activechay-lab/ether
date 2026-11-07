package transcode

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
)

var ErrSourceProbe = errors.New("transcode: source probe unavailable")
var ErrSourceProbeClosed = errors.New("transcode: source prober closed")

// SourceProbeOptions creates no encoder/hardware/session manager. FFmpegPath is
// only a sibling-directory hint for the existing bundled ffprobe discovery.
type SourceProbeOptions struct {
	FFprobePath, FFmpegPath string
	Runner                  Runner
	Timeout                 time.Duration // default 20s, max 1m, including queue time
	Concurrency             int           // default 2, max 16
}

// SourceProber owns all invocations and joins them on Close. Runner.Run must
// return only after its child and output readers have joined (ExecRunner does).
// No process, sampling loop or provider connection starts in the constructor.
type SourceProber struct {
	mu      sync.Mutex
	closed  bool
	wg      sync.WaitGroup
	life    context.Context
	cancel  context.CancelFunc
	path    string
	runner  Runner
	slots   chan struct{}
	timeout time.Duration
}

func NewSourceProber(o SourceProbeOptions) (*SourceProber, error) {
	if o.Timeout == 0 {
		o.Timeout = 20 * time.Second
	}
	if o.Concurrency == 0 {
		o.Concurrency = 2
	}
	if o.Timeout <= 0 || o.Timeout > time.Minute || o.Concurrency < 1 || o.Concurrency > 16 {
		return nil, ErrSourceProbe
	}
	sibling := ""
	if o.FFmpegPath != "" {
		sibling = filepath.Dir(o.FFmpegPath)
		if st, err := os.Stat(o.FFmpegPath); err == nil && st.IsDir() {
			sibling = o.FFmpegPath
		}
	}
	path, err := defaultFinder().find("ffprobe", o.FFprobePath, sibling)
	if err != nil {
		return nil, ErrSourceProbe
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, ErrSourceProbe
	}
	if o.Runner == nil {
		o.Runner = ExecRunner{}
	}
	life, cancel := context.WithCancel(context.Background())
	return &SourceProber{life: life, cancel: cancel, path: path, runner: o.Runner, timeout: o.Timeout, slots: make(chan struct{}, o.Concurrency)}, nil
}

func sourceProbeInput(input string) bool {
	u, err := url.Parse(input)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Fragment != "" || u.Opaque != "" || u.RawPath != "" || len(input) > 8192 {
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	port, perr := strconv.Atoi(u.Port())
	return err == nil && ip.IsLoopback() && perr == nil && port > 0 && port <= 65535
}

func (p *SourceProber) Probe(ctx context.Context, input string) (*core.MediaInfo, error) {
	if ctx == nil || !sourceProbeInput(input) {
		return nil, ErrSourceProbe
	}
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil, ErrSourceProbeClosed
	}
	op, cancel := context.WithTimeout(p.life, p.timeout)
	p.wg.Add(1)
	p.mu.Unlock()
	unhook := context.AfterFunc(ctx, cancel)
	defer func() { unhook(); cancel(); p.wg.Done() }()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	select {
	case <-op.Done():
		return nil, p.probeError(ctx, op)
	case p.slots <- struct{}{}:
	}
	defer func() { <-p.slots }()
	if op.Err() != nil {
		return nil, p.probeError(ctx, op)
	}
	budget := &sourceProbeBudget{remaining: 2 << 20, cancel: cancel}
	info, err := probe(op, sourceProbeRunner{Runner: p.runner, budget: budget}, p.path, input)
	if err != nil || info == nil || op.Err() != nil || ctx.Err() != nil {
		return nil, p.probeError(ctx, op)
	}
	// Optional bounded proof has its own budget/deadline: failure leaves ordinary
	// playback usable, with copy recovery unsupported.
	scanCtx, scanCancel := context.WithTimeout(op, 10*time.Second)
	scanBudget := &sourceProbeBudget{remaining: 32 << 20, cancel: scanCancel}
	scanCopyTimeline(scanCtx, sourceProbeRunner{Runner: p.runner, budget: scanBudget}, p.path, input, info)
	scanCancel()
	if op.Err() != nil || ctx.Err() != nil {
		return nil, p.probeError(ctx, op)
	}
	return info, nil
}

func (p *SourceProber) probeError(caller, op context.Context) error {
	if err := caller.Err(); err != nil {
		return err
	}
	if p.life.Err() != nil {
		return ErrSourceProbeClosed
	}
	if errors.Is(op.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	// Never expose the capability input or raw tool stderr through Error().
	return ErrSourceProbe
}

func (p *SourceProber) Close() error {
	p.mu.Lock()
	p.closed = true
	p.cancel()
	p.mu.Unlock()
	p.wg.Wait()
	return nil
}

type sourceProbeBudget struct {
	mu        sync.Mutex
	remaining int
	cancel    context.CancelFunc
}
type sourceProbeOutput struct {
	dst    io.Writer
	budget *sourceProbeBudget
}

func (w sourceProbeOutput) Write(b []byte) (int, error) {
	w.budget.mu.Lock()
	defer w.budget.mu.Unlock()
	if len(b) > w.budget.remaining {
		w.budget.cancel()
		return 0, ErrSourceProbe
	}
	w.budget.remaining -= len(b)
	if w.dst == nil {
		return len(b), nil
	}
	return w.dst.Write(b)
}

type sourceProbeRunner struct {
	Runner
	budget *sourceProbeBudget
}

func (r sourceProbeRunner) Run(ctx context.Context, c Command) error {
	c.Stdout = sourceProbeOutput{dst: c.Stdout, budget: r.budget}
	c.Stderr = sourceProbeOutput{dst: c.Stderr, budget: r.budget}
	return r.Runner.Run(ctx, c)
}
