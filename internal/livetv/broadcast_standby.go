package livetv

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/transcode"
)

// steadyStandby is a backup input opened at its live edge while the primary
// shows stress (a delayed hot-standby switch). Its decoder is held by
// backpressure once its first pictures are buffered, so adopting it on
// failure splices media without a cold open, continuing exactly where it
// paused: a playlist input keeps the rest of the segment it fetched instead
// of waiting for the provider's next one. It holds its own provider
// connection, so it only exists while the account's budget has room.
type steadyStandby struct {
	src      StreamSource
	link     *steadyLink
	decoder  transcode.Decoder
	frames   chan transcode.RawFrame
	err      error
	produced atomic.Bool
	failed   atomic.Bool
	done     chan struct{}
	once     sync.Once
}

func (sb *steadyStandby) ready() bool {
	return sb != nil && sb.produced.Load() && !sb.failed.Load()
}

func (sb *steadyStandby) run(ctx context.Context) {
	defer close(sb.done)
	defer close(sb.frames)
	for {
		frame, err := sb.decoder.Next(ctx)
		if err != nil {
			sb.err = err
			sb.failed.Store(true)
			return
		}
		sb.produced.Store(true)
		select {
		case sb.frames <- frame:
		case <-ctx.Done():
			return
		}
	}
}

// adopt hands the standby's decoder to the channel clock.
func (sb *steadyStandby) adopt() transcode.Decoder {
	return &standbyDecoder{sb: sb}
}

func (sb *steadyStandby) close(s *Service) {
	if sb == nil {
		return
	}
	sb.once.Do(func() {
		sb.decoder.Close()
		<-sb.done
		sb.link.close(s)
	})
}

type standbyDecoder struct{ sb *steadyStandby }

func (d *standbyDecoder) Next(ctx context.Context) (transcode.RawFrame, error) {
	select {
	case <-ctx.Done():
		return transcode.RawFrame{}, ctx.Err()
	case frame, ok := <-d.sb.frames:
		if !ok {
			return transcode.RawFrame{}, d.sb.err
		}
		return frame, nil
	}
}

// Close stops the decoder; the adopted link now belongs to the planner.
func (d *standbyDecoder) Close() error {
	d.sb.once.Do(func() {
		d.sb.decoder.Close()
		<-d.sb.done
	})
	return nil
}

func (d *standbyDecoder) Diagnostics() string {
	if source, ok := d.sb.decoder.(interface{ Diagnostics() string }); ok {
		return "standby " + source.Diagnostics()
	}
	return "standby"
}

// watchStandby opens a backup when the primary input shows stress and
// closes it once the primary has stayed healthy for steadyStandbyLinger.
// Stress is upstream data older than a second (the tap's health) or, without
// that signal, a decoder that produced no picture for a second.
func (p *providerBroadcastPlanner) watchStandby(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	serial := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		now := time.Now()
		p.mu.Lock()
		a, sb := p.attempt, p.standby
		p.mu.Unlock()
		if sb != nil && sb.failed.Load() {
			p.mu.Lock()
			if p.standby == sb {
				p.standby = nil
			}
			p.mu.Unlock()
			sb.close(p.s)
			continue
		}
		if a == nil {
			// Between attempts: when the input's own transport has recovered
			// (the tap reconnected after the ladder backed off), open now
			// rather than at the backed-off retry time.
			p.mu.Lock()
			link, waiting, retry := p.link, p.waiting, p.retry
			p.mu.Unlock()
			if waiting && link != nil && retry != nil {
				if h, ok := p.s.inputHealth(link.ss); ok && h.Connected && !h.Failed {
					p.mu.Lock()
					p.waiting = false
					p.mu.Unlock()
					p.logger().Debug("live input recovered while waiting", "input", link.src.ID)
					retry()
				}
			}
			continue
		}
		stressed := a.decoder.stressed(now)
		h, healthy := p.s.inputHealth(a.link.ss)
		if healthy && (!h.Connected || h.Stalled || h.Failed || h.DataAge > steadyStress) {
			stressed = true
		}
		// Switch on failure: once the primary stops delivering pictures (or
		// its tap gave up) a ready standby takes over at once, before the
		// clock's 1.5 s stall hold would expire.
		if sb.ready() && (a.decoder.starved(now) || healthy && h.Failed) {
			p.mu.Lock()
			retry := p.retry
			if p.attempt == a && p.standby == sb {
				p.attempt = nil
				p.ladder.next, p.ladder.liveEdge = sb.src.ID, false
			} else {
				retry = nil
			}
			p.mu.Unlock()
			if retry != nil {
				p.s.recordSourceHealth(p.channel.ID, a.src, errSteadyStarved, false)
				p.logger().Info("live input lost", "input", a.src.ID, "reason", errSteadyStarved, "next", sb.src.ID, "standby", true)
				retry()
			}
			continue
		}
		if stressed {
			p.mu.Lock()
			p.lastStress = now
			p.mu.Unlock()
			if sb == nil {
				serial++
				p.openStandby(ctx, a.src, serial)
			}
			continue
		}
		p.mu.Lock()
		quiet := now.Sub(p.lastStress) >= steadyStandbyLinger
		if quiet && p.standby == sb && sb != nil {
			p.standby = nil
		} else {
			sb = nil
		}
		p.mu.Unlock()
		if sb != nil {
			p.logger().Debug("live standby closed", "input", sb.src.ID)
			sb.close(p.s)
		}
	}
}

func (p *providerBroadcastPlanner) openStandby(ctx context.Context, primary StreamSource, serial int) {
	c := p.channel
	for _, src := range p.s.orderedSourceCandidates(c, primary.ID) {
		p.mu.Lock()
		parked := p.ladder.sources[src.ID] != nil && p.ladder.sources[src.ID].parked.After(p.s.o.Now())
		p.mu.Unlock()
		if parked {
			continue
		}
		link, err := p.s.linkSteadySource(ctx, c, src, "broadcast-"+c.ID+steadyStandbyID+"-"+strconv.Itoa(serial), 0)
		if err != nil {
			// A busy account has no room for a second connection; never
			// overlap a capped account.
			continue
		}
		input, err := p.s.relayInputLive(link.ss)
		var decoder transcode.Decoder
		if err == nil {
			decoder, err = p.runner.StartDecoder(ctx, transcode.DecoderRequest{SessionID: link.ss.info.ID, InputURL: input, Format: p.format, Live: true, InputFormat: link.format})
		}
		if err != nil {
			link.close(p.s)
			continue
		}
		sb := &steadyStandby{src: src, link: link, decoder: decoder, frames: make(chan transcode.RawFrame, max(1, p.format.FPSNum/p.format.FPSDen/2)), done: make(chan struct{})}
		go sb.run(ctx)
		p.mu.Lock()
		if p.standby != nil || ctx.Err() != nil {
			p.mu.Unlock()
			sb.close(p.s)
			return
		}
		p.standby = sb
		p.mu.Unlock()
		p.logger().Info("live standby opened", "input", src.ID, "primary", primary.ID)
		return
	}
}
