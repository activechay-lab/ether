package livetv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// providerBroadcastPlanner feeds one IPTV channel's continuous output from
// its provider inputs. The output never restarts: every recovery (same-source
// reopen, codec-change rejoin, backup source, hot standby) is a new decoder
// spliced into the same encoder timeline by the broadcast clock.
type providerBroadcastPlanner struct {
	guideCancel           context.CancelFunc
	guideDone             chan struct{}
	guideFrom, guideUntil time.Time
	guide                 []Programme
	s                     *Service
	channel               Channel
	format                transcode.RawFormat
	runner                transcode.ContinuousRunner
	mu                    sync.Mutex
	lastAired             map[string]time.Duration

	ladder     *steadyLadder
	link       *steadyLink // granted input of the current source, reused by same-source reopens
	primed     *steadyLink // the tune's probed input; the first open reads the bytes it probed
	attempt    *steadyAttempt
	standby    *steadyStandby
	retryAt    time.Time
	lastStress time.Time
}

// steadyLink is one granted provider input: the relay grant (and with it the
// shared tap) plus the provider-account connection it holds.
type steadyLink struct {
	src     StreamSource
	ss      *session
	release func()
	format  string // probed container: "hls", "mpegts" or empty
	uses    int
	once    sync.Once
}

func (l *steadyLink) close(s *Service) {
	if l == nil {
		return
	}
	l.once.Do(func() {
		s.revokeInput(l.ss)
		l.ss.cancel()
		if l.release != nil {
			l.release()
		}
	})
}

type steadyAttempt struct {
	src     StreamSource
	link    *steadyLink
	decoder *providerBroadcastDecoder
}

// A recovery that rejoins the source it just lost gets a short first-picture
// deadline when a backup can take over; a cold or sole input waits longer.
const (
	steadyFirstFrameCold     = 8 * time.Second
	steadyFirstFrameAlone    = 20 * time.Second
	steadyFirstFrameRejoin   = 4 * time.Second
	steadyFirstFrameBackup   = 8 * time.Second
	steadyStress             = time.Second
	steadyStandbyLinger      = 30 * time.Second
	steadyClockReset         = 2 * time.Minute
	steadyStandbyID          = "-standby"
	steadyMaxLinkUses        = 4
	steadyOpenResolveTimeout = 5 * time.Second
)

var errSteadyFirstFrame = errors.New("live input produced no picture in time")

func (p *providerBroadcastPlanner) plans(now time.Time) (broadcastPlan, *broadcastPlan) {
	p.mu.Lock()
	ready := !now.Before(p.guideFrom) && now.Before(p.guideUntil)
	var tag *broadcastPolicyTag
	if ready {
		for _, programme := range p.guide {
			if !now.Before(programme.Start) && now.Before(programme.End) {
				tag = programme.policyTag
				break
			}
		}
	}
	p.mu.Unlock()
	if !ready {
		// Hold on a neutral signal screen until the background EPG snapshot is
		// ready. A placeholder denial must never poison a published screen segment.
		// Changing identity also retires an active source if its policy expires.
		return broadcastPlan{id: "provider-policy-wait-" + p.channel.ID}, nil
	}
	return broadcastPlan{element: "programme", policy: tag, programme: true, id: "provider-" + p.channel.ID, open: p.open, failed: p.failed}, nil
}

// A cold IPTV tune waits briefly for its first real picture before the
// output starts on the reconnecting screen: the probe already read the bytes
// this decoder starts from.
func (p *providerBroadcastPlanner) coldWait() time.Duration { return 3 * time.Second }

func (p *providerBroadcastPlanner) logger() *slog.Logger {
	return p.s.o.Logger.With("channel", p.channel.ID)
}

func sourceIDs(sources []StreamSource) []string {
	ids := make([]string, len(sources))
	for i, src := range sources {
		ids[i] = src.ID
	}
	return ids
}

// failed records why the current input ended and returns when the channel
// opens its next one. It never parks the channel behind a source cooldown.
func (p *providerBroadcastPlanner) failed(err error) time.Time {
	now := p.s.o.Now()
	candidates := p.s.orderedSourceCandidates(p.channel)
	p.mu.Lock()
	defer p.mu.Unlock()
	a := p.attempt
	p.attempt = nil
	if a == nil {
		// open failed before any decoder started; it already chose the retry.
		if p.retryAt.IsZero() {
			return now.Add(steadySpacing)
		}
		return p.retryAt
	}
	aired := a.decoder.delivered()
	changed := errors.Is(err, transcode.ErrInputChanged)
	// A transport-stream tap already reconnected its URL itself before its
	// readers saw the end, so the ladder skips straight to the next source.
	tapped := a.link.format == "mpegts"
	failover := false
	if h, ok := p.s.inputHealth(a.link.ss); ok {
		tapped = h.Kind == "ts"
		failover = h.Failed
	}
	failover = failover || tapped && steadyFailover(err)
	if !changed {
		p.s.recordSourceHealth(p.channel.ID, a.src, err, false)
	}
	at := p.ladder.failed(a.src.ID, err, aired, failover, sourceIDs(candidates), now)
	if sb := p.standby; sb != nil && !changed && sb.src.ID != a.src.ID && sb.ready() {
		// Hot standby: the backup is already decoding at its live edge.
		p.ladder.next, p.ladder.liveEdge = sb.src.ID, false
		at = now
	}
	p.logger().Info("live input lost", "input", a.src.ID, "reason", err, "aired", aired, "failover", failover, "next", p.ladder.next, "retryIn", at.Sub(now))
	return at
}

// open starts the decoder of the input the ladder chose. Synchronous failures
// (resolution, grant, spawn) advance the ladder within this call, so the
// channel moves to a backup without waiting for another clock tick.
func (p *providerBroadcastPlanner) open(ctx context.Context) (transcode.Decoder, func(), error) {
	c := p.channel
	p.mu.Lock()
	p.attempt = nil
	p.retryAt = time.Time{}
	p.mu.Unlock()
	var last error = core.ErrNotAvailable
	busy := map[string]bool{}
	for range 2 * (MaxChannelInputs + 1) {
		now := p.s.o.Now()
		var candidates []StreamSource
		for _, src := range p.s.orderedSourceCandidates(c) {
			if !busy[src.ID] {
				candidates = append(candidates, src)
			}
		}
		if len(candidates) == 0 {
			p.mu.Lock()
			p.retryAt = now.Add(steadySpacing)
			p.mu.Unlock()
			return nil, nil, fmt.Errorf("broadcast provider: %w", last)
		}
		p.mu.Lock()
		id, liveEdge, ok, retry := p.ladder.choose(sourceIDs(candidates), now)
		if !ok {
			p.retryAt = retry
			p.mu.Unlock()
			return nil, nil, fmt.Errorf("broadcast provider: every input is resting: %w", last)
		}
		var src StreamSource
		for _, candidate := range candidates {
			if candidate.ID == id {
				src = candidate
			}
		}
		if sb := p.standby; sb != nil && sb.src.ID == id && sb.ready() {
			p.standby = nil
			if p.link != sb.link {
				p.link.close(p.s)
			}
			p.link = sb.link
			p.ladder.opened(id, now)
			decoder := p.wrap(src, sb.adopt(), 0)
			p.attempt = &steadyAttempt{src: src, link: sb.link, decoder: decoder}
			p.mu.Unlock()
			p.logger().Info("live input switched to standby", "input", id)
			return decoder, nil, nil
		}
		link := p.link
		if primed := p.primed; primed != nil {
			p.primed = nil
			if primed.src.ID == id && link == nil {
				link = primed
			} else {
				primed.close(p.s)
			}
		}
		if link != nil && (link.src.ID != id || link.src.URL != src.URL || link.uses >= steadyMaxLinkUses) {
			// One provider connection per account while one is live: release
			// the previous input before reserving another. A link used for every
			// opening rung is re-resolved, refreshing expiring provider URLs.
			link.close(p.s)
			link = nil
		}
		p.link = link
		p.mu.Unlock()
		if link == nil {
			var err error
			link, err = p.s.linkSteadySource(ctx, c, src, "broadcast-"+c.ID)
			if errors.Is(err, ErrProviderBusy) || errors.Is(err, ErrProviderPaced) {
				busy[id] = true
				last = err
				continue
			}
			if err != nil {
				last = err
				p.syncFailure(src, err, now)
				continue
			}
			p.mu.Lock()
			p.link = link
			p.mu.Unlock()
		}
		decoder, err := p.startDecoder(ctx, link, liveEdge)
		if err != nil {
			last = err
			p.mu.Lock()
			if p.link == link {
				p.link = nil
			}
			p.mu.Unlock()
			link.close(p.s)
			p.syncFailure(src, err, now)
			continue
		}
		wrapped := p.wrap(src, decoder, p.firstFrameDeadline(src.ID, len(candidates) > 1))
		p.mu.Lock()
		link.uses++
		p.ladder.opened(id, now)
		p.attempt = &steadyAttempt{src: src, link: link, decoder: wrapped}
		p.mu.Unlock()
		p.logger().Debug("live input opening", "input", id, "liveEdge", liveEdge, "format", link.format, "uses", link.uses)
		return wrapped, nil, nil
	}
	return nil, nil, fmt.Errorf("broadcast provider: %w", last)
}

func (p *providerBroadcastPlanner) syncFailure(src StreamSource, err error, now time.Time) {
	p.s.recordSourceHealth(p.channel.ID, src, err, false)
	candidates := sourceIDs(p.s.orderedSourceCandidates(p.channel))
	p.mu.Lock()
	p.ladder.opened(src.ID, now)
	p.retryAt = p.ladder.failed(src.ID, err, false, true, candidates, now)
	p.mu.Unlock()
}

func (p *providerBroadcastPlanner) firstFrameDeadline(id string, backup bool) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case !backup:
		return steadyFirstFrameAlone
	case len(p.lastAired) == 0:
		return steadyFirstFrameCold
	case id == p.ladder.source:
		return steadyFirstFrameRejoin
	}
	return steadyFirstFrameBackup
}

func (p *providerBroadcastPlanner) startDecoder(ctx context.Context, link *steadyLink, liveEdge bool) (transcode.Decoder, error) {
	input, err := p.s.relayInput(link.ss)
	if liveEdge {
		input, err = p.s.relayInputLive(link.ss)
	}
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	_, aired := p.lastAired[link.src.ID]
	p.mu.Unlock()
	reserve := p.s.stabilityReserve(p.channel) > 0
	return p.runner.StartDecoder(ctx, transcode.DecoderRequest{SessionID: link.ss.info.ID, InputURL: input, Format: p.format, Live: true, PrimeLive: !aired && !reserve, ReserveStart: reserve && !liveEdge, InputFormat: link.format})
}

func (p *providerBroadcastPlanner) wrap(src StreamSource, d transcode.Decoder, firstFrame time.Duration) *providerBroadcastDecoder {
	c := p.channel
	return &providerBroadcastDecoder{Decoder: d, planner: p, sourceID: src.ID, firstFrame: firstFrame, started: time.Now(), frame: time.Second * time.Duration(p.format.FPSDen) / time.Duration(p.format.FPSNum), ready: func() {}, healthy: func() {
		p.s.recordSourceHealth(c.ID, src, nil, true)
		p.mu.Lock()
		p.ladder.delivered(src.ID)
		p.mu.Unlock()
	}}
}

// linkSteadySource reserves the provider account's connection, resolves the
// input and grants it to the loopback relay.
func (s *Service) linkSteadySource(ctx context.Context, c Channel, src StreamSource, reservation string) (*steadyLink, error) {
	account := src.AuthSourceID
	if account == "" {
		account = c.SourceID
	}
	entry, err := s.sourceEntry(ctx, account)
	if err != nil {
		return nil, err
	}
	release, err := s.reserveBroadcastProvider(account, c.ID, reservation, entry.limit)
	if err != nil {
		return nil, err
	}
	attempt, cancelAttempt := context.WithTimeout(ctx, steadyOpenResolveTimeout)
	resolved, headers, err := s.ResolveStreamSource(attempt, c, src)
	cancelAttempt()
	if err != nil {
		release()
		return nil, err
	}
	inputCtx, cancel := context.WithCancel(s.ctx)
	ss := &session{ctx: inputCtx, cancel: cancel, info: SessionInfo{ID: reservation, Channel: c}, inputSource: src, inputURL: resolved, inputHeaders: headers}
	grant := src
	grant.URL = resolved
	if err := s.grantInput(ss, grant, headers); err != nil {
		cancel()
		release()
		return nil, err
	}
	return &steadyLink{src: src, ss: ss, release: release, format: s.cachedInputFormat(ss)}, nil
}

// reserveBroadcastProvider takes a provider connection under the same
// Service.mu as ordinary tunes. A waiting output holds no slot. Direct
// viewers sharing a channel count once; every Steady reservation (an output's
// input, or its hot standby) is its own connection.
func (s *Service) reserveBroadcastProvider(account, channel, id string, limit int) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.broadcastProviders == nil {
		s.broadcastProviders = map[string]map[string]string{}
	}
	active := s.broadcastProviders[account]
	if active == nil {
		active = map[string]string{}
		s.broadcastProviders[account] = active
	}
	channels := map[string]bool{}
	for _, ss := range s.sessions {
		if ss.broadcast != nil || !ss.ready {
			continue
		}
		source := ss.info.Channel.SourceID
		if ss.inputSource.AuthSourceID != "" {
			source = ss.inputSource.AuthSourceID
		}
		if source == account {
			channels[ss.info.Channel.ID] = true
		}
	}
	if _, exists := active[id]; exists {
		return nil, ErrProviderBusy
	}
	if limit > 0 && len(channels)+len(active)+s.inputRelay.taps.externalCount(account) >= limit {
		return nil, ErrProviderBusy
	}
	active[id] = channel
	var once sync.Once
	return func() { once.Do(func() { s.mu.Lock(); delete(s.broadcastProviders[account], id); s.mu.Unlock() }) }, nil
}

// providerBroadcastDecoder adapts one provider decoder to the channel clock:
// it never replays media this source already aired, bounds the wait for the
// first picture, and reports delivery and stress to the planner.
type providerBroadcastDecoder struct {
	transcode.Decoder
	planner    *providerBroadcastPlanner
	sourceID   string
	once       sync.Once
	ready      func()
	healthy    func()
	firstFrame time.Duration
	started    time.Time
	frame      time.Duration
	passed     bool
	decodedAt  atomic.Int64
	aired      atomic.Int64
	healthOnce sync.Once
}

func (d *providerBroadcastDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	if !d.passed && d.firstFrame > 0 {
		remaining := d.firstFrame - time.Since(d.started)
		if remaining <= 0 {
			return transcode.RawFrame{}, errSteadyFirstFrame
		}
		bounded, cancel := context.WithTimeoutCause(ctx, remaining, errSteadyFirstFrame)
		defer cancel()
		frame, err := d.next(bounded)
		if err != nil && ctx.Err() == nil && errors.Is(context.Cause(bounded), errSteadyFirstFrame) {
			return frame, errSteadyFirstFrame
		}
		return frame, err
	}
	return d.next(ctx)
}

func (d *providerBroadcastDecoder) next(ctx context.Context) (transcode.RawFrame, error) {
	for {
		frame, err := d.Decoder.Next(ctx)
		if err != nil {
			return frame, err
		}
		if !frame.SourcePTSValid {
			return transcode.RawFrame{}, fmt.Errorf("live decoder did not report a source timestamp")
		}
		d.decodedAt.Store(time.Now().UnixNano())
		if !d.passed {
			// A reopened input may start inside media this source already aired
			// (a provider playlist that stopped advancing, a reserve backlog).
			// Within one decoder the media is continuous, including provider
			// discontinuities, so the floor applies only until it is passed.
			// A source whose clock restarted far below the floor is new media.
			d.planner.mu.Lock()
			floor, seen := d.planner.lastAired[d.sourceID]
			d.planner.mu.Unlock()
			if seen && frame.SourcePTS <= floor && floor-frame.SourcePTS < steadyClockReset {
				continue
			}
			d.passed = true
		}
		d.once.Do(d.ready)
		return frame, nil
	}
}

func (d *providerBroadcastDecoder) Aired(frame transcode.RawFrame) {
	if !frame.SourcePTSValid {
		return
	}
	d.planner.mu.Lock()
	if d.planner.lastAired == nil {
		d.planner.lastAired = map[string]time.Duration{}
	}
	d.planner.lastAired[d.sourceID] = frame.SourcePTS
	d.planner.mu.Unlock()
	if n := d.aired.Add(1); d.healthy != nil && time.Duration(n)*d.frame >= steadyDelivered {
		d.healthOnce.Do(d.healthy)
	}
}

// delivered reports that this input aired enough media to count as healthy.
func (d *providerBroadcastDecoder) delivered() bool {
	return d != nil && time.Duration(d.aired.Load())*d.frame >= steadyDelivered
}

// stressed reports a live input that stopped producing pictures.
func (d *providerBroadcastDecoder) stressed(now time.Time) bool {
	at := d.decodedAt.Load()
	return at != 0 && now.Sub(time.Unix(0, at)) > steadyStress
}

func (d *providerBroadcastDecoder) Diagnostics() string {
	d.planner.mu.Lock()
	floor := d.planner.lastAired[d.sourceID]
	d.planner.mu.Unlock()
	details := ""
	if source, ok := d.Decoder.(interface{ Diagnostics() string }); ok {
		details = source.Diagnostics()
	}
	return fmt.Sprintf("lastAiredSourcePTS=%s %s", floor, details)
}

func (p *providerBroadcastPlanner) broadcastLogger() *slog.Logger {
	return p.s.o.Logger.With("channel", p.channel.ID)
}

// IPTV outputs retain EPG policy at exact frame-clock boundaries as well.
// Fetching/hydrating the bounded upcoming window is background work, as is
// the hot-standby watch.
func (p *providerBroadcastPlanner) startPlanner(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	p.guideCancel = cancel
	p.guideDone = make(chan struct{})
	if p.ladder == nil {
		p.ladder = newSteadyLadder()
	}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		tick := time.NewTicker(30 * time.Second)
		defer tick.Stop()
		for {
			now := p.s.o.Now()
			until := now.Add(2 * time.Minute)
			ps, err := p.s.programmes(ctx, p.channel, now, until)
			if err == nil {
				for i := range ps {
					ps[i].policyTag = p.s.programmePolicy(ctx, ps[i])
				}
				p.mu.Lock()
				p.guideFrom, p.guideUntil, p.guide = now, until, ps
				p.mu.Unlock()
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	go func() { defer workers.Done(); p.watchStandby(ctx) }()
	go func() { workers.Wait(); close(p.guideDone) }()
}

func (p *providerBroadcastPlanner) closePlanner() {
	p.guideCancel()
	<-p.guideDone
	p.mu.Lock()
	link, primed, standby := p.link, p.primed, p.standby
	p.link, p.primed, p.standby = nil, nil, nil
	p.mu.Unlock()
	standby.close(p.s)
	primed.close(p.s)
	link.close(p.s)
}
