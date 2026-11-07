package livetv

import (
	"context"
	"errors"
	"time"

	"github.com/braedonsaunders/ether/internal/transcode"
)

// A codec or stream-set change on a live input starves one elementary
// stream while the other keeps flowing. Waiting for the decoder to give up
// (ErrInputChanged) and only then reopening costs about a second of held
// picture. Instead, once one stream has starved for spliceOpenAfter, a
// candidate decoder opens on the same input at its live edge; the clock
// switches to it as soon as it has a picture while the old one is still
// starved (or reported the change). If the old decoder recovers first, the
// candidate closes again. The provider connection is the same shared tap.
const (
	spliceOpenAfter   = 200 * time.Millisecond
	spliceSwitchAfter = 250 * time.Millisecond
	spliceRecovered   = time.Second
	spliceRetryAfter  = 2 * time.Second
	splicePoll        = 40 * time.Millisecond
)

type pumped struct {
	frame transcode.RawFrame
	err   error
}

type decoderPump struct {
	dec    transcode.Decoder
	ch     chan pumped
	cancel context.CancelFunc
	held   *pumped // a candidate's first picture, kept until the switch
	opened time.Time
}

func startPump(parent context.Context, dec transcode.Decoder) *decoderPump {
	ctx, cancel := context.WithCancel(parent)
	p := &decoderPump{dec: dec, ch: make(chan pumped), cancel: cancel, opened: time.Now()}
	go func() {
		for {
			frame, err := dec.Next(ctx)
			select {
			case p.ch <- pumped{frame, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return p
}

func (p *decoderPump) close() {
	if p != nil {
		p.cancel()
		go p.dec.Close()
	}
}

type starving interface{ Starving() time.Duration }

// pull is the next picture of the current decoder, splicing a live-edge
// candidate in when the current one starves.
func (d *providerBroadcastDecoder) pull(ctx context.Context) (transcode.RawFrame, error) {
	if !d.passed || d.reopen == nil {
		return d.Decoder.Next(ctx)
	}
	if _, ok := d.Decoder.(starving); !ok {
		return d.Decoder.Next(ctx)
	}
	if d.cur == nil {
		d.cur = startPump(d.life, d.Decoder)
	}
	tick := time.NewTicker(splicePoll)
	defer tick.Stop()
	failed := false
	for {
		var candidate chan pumped
		if d.cand != nil && d.cand.held == nil {
			candidate = d.cand.ch
		}
		select {
		case <-ctx.Done():
			return transcode.RawFrame{}, ctx.Err()
		case r := <-d.cur.ch:
			if r.err == nil {
				return r.frame, nil
			}
			if d.cand == nil || !errors.Is(r.err, transcode.ErrInputChanged) {
				return r.frame, r.err
			}
			// The change is confirmed: wait for the candidate's picture.
			failed = true
			d.cur.ch = nil
		case r := <-candidate:
			if r.err != nil {
				d.cand.close()
				d.cand = nil
				if failed {
					return transcode.RawFrame{}, transcode.ErrInputChanged
				}
				continue
			}
			if !d.replayed(r.frame) {
				d.cand.held = &r
			}
		case <-tick.C:
		}
		starved := d.Decoder.(starving).Starving()
		switch {
		case d.cand != nil && d.cand.held != nil && (failed || starved >= spliceSwitchAfter):
			frame := d.cand.held.frame
			d.cand.held = nil
			d.cur.close()
			d.Decoder, d.cur, d.cand = d.cand.dec, d.cand, nil
			d.planner.logger().Info("live input spliced to a new decoder", "input", d.sourceID, "reason", "stream change", "starved", starved)
			return frame, nil
		case d.cand == nil && !failed && starved >= spliceOpenAfter && time.Since(d.lastCandidate) >= spliceRetryAfter:
			d.lastCandidate = time.Now()
			if dec, err := d.reopen(); err == nil {
				d.cand = startPump(d.life, dec)
			}
		case d.cand != nil && !failed && starved == 0 && time.Since(d.cand.opened) >= spliceRecovered:
			d.cand.close()
			d.cand = nil
		}
	}
}

// replayed reports a candidate picture this source already aired.
func (d *providerBroadcastDecoder) replayed(frame transcode.RawFrame) bool {
	if !frame.SourcePTSValid {
		return true
	}
	d.planner.mu.Lock()
	floor, seen := d.planner.lastAired[d.sourceID]
	d.planner.mu.Unlock()
	return seen && frame.SourcePTS <= floor && floor-frame.SourcePTS < steadyClockReset
}

// Close stops the current decoder and any candidate.
func (d *providerBroadcastDecoder) Close() error {
	if d.cancel != nil {
		d.cancel()
	}
	d.cand.close()
	if d.cur != nil {
		d.cur.cancel()
	}
	return d.Decoder.Close()
}
