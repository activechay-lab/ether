package livetv

import (
	"context"
	"errors"
	"time"
)

// A shared passthrough timeline escalates as a whole: all viewers read one
// owned timeline per channel, so the channel's producer moves onto one
// Steady lease and the splice happens once, for everyone. The same rules
// apply as for a relay: Steady must have been allowed (refused only for
// capacity), there is no way back mid-session, and only a TS timeline can
// take Steady's TS feed after a discontinuity.

// watchChurn turns the timeline's codec splices and skipped segments into
// churn (run loop only).
func (p *passthroughProducer) watchChurn(ctx context.Context) {
	if n := p.tl.codecSpliceCount(); n > p.seenSplices {
		p.seenSplices = n
		p.churn(ctx, "format")
	}
	if p.ingest != nil && p.ingest.skippedTotal > p.seenSkipped {
		p.seenSkipped = p.ingest.skippedTotal
		p.churn(ctx, "damage")
	}
}

// churn records a misbehaviour; enough of them inside churnWindow ask for
// Steady.
func (p *passthroughProducer) churn(ctx context.Context, class string) {
	now := time.Now()
	kept := p.churnAt[:0]
	for _, at := range p.churnAt {
		if now.Sub(at) < churnWindow {
			kept = append(kept, at)
		}
	}
	p.churnAt = append(kept, now)
	if len(p.churnAt) >= churnEvents {
		p.wantSteady(ctx, class)
	}
}

func (p *passthroughProducer) onSteady() bool {
	p.steadyMu.Lock()
	defer p.steadyMu.Unlock()
	return p.steady != nil
}

// wantSteady starts a hand-off of the whole channel in the background.
func (p *passthroughProducer) wantSteady(ctx context.Context, class string) {
	if p.viewer == nil || !p.tl.tsOnly() {
		return
	}
	p.steadyMu.Lock()
	busy := p.steady != nil || p.steadyReady != nil || p.steadyBusy || time.Now().Before(p.steadyRetryAt)
	if !busy {
		p.steadyBusy = true
	}
	p.steadyMu.Unlock()
	if busy {
		return
	}
	viewer := p.viewer()
	done := func(feed SteadyFeed, err error) {
		p.steadyMu.Lock()
		defer p.steadyMu.Unlock()
		p.steadyBusy = false
		if err != nil {
			p.steadyRetryAt = time.Now().Add(steadyRetry)
			if !errors.Is(err, context.Canceled) {
				p.log.Info("owned passthrough stays Direct; Steady declined the hand-off", Diagnose("steady_handoff", err).LogAttrs()...)
			}
			return
		}
		p.steadyReady, p.steadyClass = feed, class
	}
	if viewer == nil {
		done(nil, errors.New("livetv: no viewer on the timeline"))
		return
	}
	// The hand-off runs under a viewer's session, with the producer's own
	// current source (it may have failed over since that viewer tuned).
	viewer.mu.Lock()
	rep := &session{info: viewer.info, user: viewer.user, caps: viewer.caps, ctx: viewer.ctx, steadyRefused: viewer.steadyRefused,
		inputURL: p.raw, inputSource: p.src, inputHeaders: p.headers.Clone()}
	viewer.mu.Unlock()
	rep.info.Channel = p.c
	if !p.s.steadyAllowed(rep) {
		done(nil, errors.New("livetv: Steady not allowed for this channel"))
		return
	}
	go func() {
		feed, err := p.s.steadyHandoff(ctx, rep)
		if err == nil {
			rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = feed.Ready(rctx)
			cancel()
			if err != nil {
				feed.Close()
				feed = nil
			}
		}
		done(feed, err)
	}()
}

// takeSteady splices a ready feed in for the whole channel (run loop only).
func (p *passthroughProducer) takeSteady() bool {
	p.steadyMu.Lock()
	feed, class := p.steadyReady, p.steadyClass
	p.steadyReady = nil
	if feed == nil {
		p.steadyMu.Unlock()
		return false
	}
	p.steady = feed
	p.steadyMu.Unlock()
	p.reportHistory()
	p.s.noteSourceMode(p.c.ID, p.src, "passthrough", "escalated")
	if p.ingest == nil {
		p.ingest = newHLSIngest(p.tl, nil, p.log, 0, 2)
	}
	p.ingest.live = true
	p.ingest.rebase(&steadyOrigin{feed: feed})
	p.tl.splice(true)
	p.log.Warn("owned passthrough handed off to Steady", "evidence", class)
	return true
}

func (p *passthroughProducer) closeSteady() {
	p.steadyMu.Lock()
	feeds := []SteadyFeed{p.steady, p.steadyReady}
	p.steady, p.steadyReady = nil, nil
	p.steadyMu.Unlock()
	for _, f := range feeds {
		if f != nil {
			f.Close()
		}
	}
}
