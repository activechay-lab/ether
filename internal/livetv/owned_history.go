package livetv

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// Direct decisions from a source's history (D's decaying source evidence)
// and the mid-session hand-off of a Direct relay to Steady.

const (
	// historyDamagePerHour is the damage rate (decode errors in copy
	// segments, transport errors at the tap) at which a source's next relay
	// starts re-encoded: copy hands damage straight to the player.
	historyDamagePerHour = 1.0
	// historyKeyframeGap is the longest keyframe gap at which copy cannot
	// cut segments inside the tune budget.
	historyKeyframeGap = 4 * time.Second

	// A session escalates to Steady after this many slate episodes or
	// format reopens inside churnWindow.
	churnEvents = 2
	churnWindow = 10 * time.Minute
	// steadyRetry is how long a refused hand-off waits before asking again.
	steadyRetry = 2 * time.Minute
)

// historyStart reports the evidence that asks a Direct relay of this source
// to start re-encoded, or "" when copy is fine.
func (s *Service) historyStart(channelID string, src StreamSource) string {
	h, ok := s.sourceHistory(channelID, src)
	if !ok {
		return ""
	}
	switch {
	case h.DamageEvents > 0 && h.DamagePerHour() >= historyDamagePerHour:
		return "damage"
	case h.MaxKeyframeGap() >= historyKeyframeGap:
		return "keyframe"
	}
	return ""
}

// historyDecision turns a copy decision into a re-encode when the source's
// history says copy would fail; evidence is "" when it stays as it was.
func (s *Service) historyDecision(ss *session, info core.MediaInfo, dec core.Decision) (core.Decision, string) {
	if !dec.VideoCopy {
		return dec, ""
	}
	ev := s.historyStart(ss.info.Channel.ID, ss.inputSource)
	if ev == "" {
		return dec, ""
	}
	next := s.transcoder().Decide(info, relayCaps(ss.caps), core.DecideOptions{DirectStream: false, AudioStream: -1, SubtitleStream: -1})
	if next.VideoCopy {
		return dec, ""
	}
	if next.Container != transcode.ContainerFMP4 && next.Container != transcode.ContainerMPEGTS {
		next.Container = dec.Container
	}
	return next, ev
}

// LearnedSourceHealth is the owner-facing summary of a source's history: the
// media evidence behind how Ether starts it. Only what is present is set.
type LearnedSourceHealth struct {
	// Damage: picture or sound damage seen recently.
	Damage bool `json:"damage,omitempty"`
	// LongGOP: keyframes too far apart to copy without delay.
	LongGOP bool `json:"longGop,omitempty"`
	// StallsPerHour of received media, rounded; 0 when under one.
	StallsPerHour int `json:"stallsPerHour,omitempty"`
	// FormatChanges recently seen (program or codec changes).
	FormatChanges int `json:"formatChanges,omitempty"`
	// StartsConverted: a direct tune of this source starts re-encoded.
	StartsConverted bool `json:"startsConverted,omitempty"`
}

// learnedHealth summarises a source's history for the owner, or nil when
// there is nothing to say.
func (s *Service) learnedHealth(channelID string, src StreamSource) *LearnedSourceHealth {
	h, ok := s.sourceHistory(channelID, src)
	if !ok {
		return nil
	}
	ev := s.historyStart(channelID, src)
	l := LearnedSourceHealth{
		Damage:          h.DamageEvents > 0 && h.DamagePerHour() >= historyDamagePerHour,
		LongGOP:         h.MaxKeyframeGap() >= historyKeyframeGap,
		StallsPerHour:   int(h.StallsPerHour() + 0.5),
		FormatChanges:   int(h.FormatChanges),
		StartsConverted: ev != "",
	}
	if l == (LearnedSourceHealth{}) {
		return nil
	}
	return &l
}

// noteRelayEscalation records why a copy relay left copy, into the source's
// decaying evidence and its mode history.
func (p *relayProducer) noteRelayEscalation(class string, gap time.Duration) {
	ss := p.sess.Load()
	ss.mu.Lock()
	c, src := ss.info.Channel, ss.inputSource
	ss.mu.Unlock()
	switch class {
	case "damage":
		p.s.noteSourceEvidence(c.ID, src, SourceEvidence{DamageEvents: 1})
	case "keyframe":
		p.s.noteSourceEvidence(c.ID, src, SourceEvidence{KeyframeGap: gap})
	}
	p.s.noteSourceMode(c.ID, src, "copy", "escalated")
}

// noteRelayEnd records the mode a relay session ended in.
func (p *relayProducer) noteRelayEnd() {
	ss := p.sess.Load()
	if ss == nil {
		return
	}
	ss.mu.Lock()
	c, src := ss.info.Channel, ss.inputSource
	ss.mu.Unlock()
	p.mu.Lock()
	mode := "transcode"
	switch {
	case p.steady != nil:
		mode = "steady"
	case p.dec.VideoCopy:
		mode = "copy"
	}
	outcome := "ok"
	if p.escalated {
		outcome = "escalated"
	}
	p.mu.Unlock()
	p.s.noteSourceMode(c.ID, src, mode, outcome)
}

// churn counts a slate episode or format reopen and reports whether the
// session has seen enough inside churnWindow to ask for Steady.
func (p *relayProducer) churn(now time.Time) bool {
	kept := p.churnAt[:0]
	for _, at := range p.churnAt {
		if now.Sub(at) < churnWindow {
			kept = append(kept, at)
		}
	}
	p.churnAt = append(kept, now)
	return len(p.churnAt) >= churnEvents
}

// wantSteady starts a hand-off to Steady in the background, where Steady
// would have been allowed for this channel and capacity is free. The relay
// keeps producing meanwhile; the run loop splices the feed in once it has
// its cushion. Only a TS relay can hand off: an fMP4 playlist cannot drop
// its init section at a discontinuity.
func (p *relayProducer) wantSteady(ctx context.Context, class string) {
	p.mu.Lock()
	busy := p.closed || p.steady != nil || p.steadyBusy || time.Now().Before(p.steadyRetryAt) || p.dec.Container == transcode.ContainerFMP4
	if !busy {
		p.steadyBusy = true
	}
	p.mu.Unlock()
	if busy {
		return
	}
	ss := p.sess.Load()
	if !p.s.steadyAllowed(ss) {
		p.mu.Lock()
		p.steadyBusy, p.steadyRetryAt = false, time.Now().Add(steadyRetry)
		p.mu.Unlock()
		return
	}
	go func() {
		feed, err := p.s.steadyHandoff(ctx, ss)
		if err == nil {
			rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
			err = feed.Ready(rctx)
			cancel()
			if err != nil {
				feed.Close()
			}
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		p.steadyBusy = false
		if err != nil || p.closed {
			if err == nil {
				feed.Close()
			} else if !errors.Is(err, context.Canceled) {
				p.steadyRetryAt = time.Now().Add(steadyRetry)
				p.log.Info("owned relay stays Direct; Steady declined the hand-off", Diagnose("steady_handoff", err).LogAttrs()...)
			}
			return
		}
		p.steadyReady, p.steadyClass = feed, class
	}()
}

// takeSteady splices a ready Steady feed in as the producer (run loop only).
func (p *relayProducer) takeSteady() bool {
	p.mu.Lock()
	feed, class := p.steadyReady, p.steadyClass
	p.steadyReady = nil
	if feed == nil || p.closed {
		p.mu.Unlock()
		if feed != nil {
			feed.Close()
		}
		return false
	}
	old, oldCancel := p.hls, p.cancel
	hedge, overlap := p.hedge, p.overlap
	p.hls, p.cancel, p.hedge, p.overlap = nil, nil, nil, nil
	p.steady = feed
	wasCopy := p.dec.VideoCopy && !p.escalated
	p.escalated = true
	p.dec.VideoCopy, p.dec.Container = false, transcode.ContainerMPEGTS
	p.startedAt = time.Now()
	p.ingest.rebase(&steadyOrigin{feed: feed})
	p.tl.splice(true)
	p.mu.Unlock()
	if oldCancel != nil {
		oldCancel()
	}
	if old != nil {
		old.Close()
	}
	hedge.close()
	overlap.close()
	if wasCopy {
		p.noteRelayEscalation(class, 0)
	}
	p.log.Warn("owned relay handed off to Steady", "evidence", class)
	return true
}

// steadyOrigin reads a Steady hand-off feed as an ingest origin.
type steadyOrigin struct{ feed SteadyFeed }

func (o *steadyOrigin) label() string { return "steady" }

func (o *steadyOrigin) media(ctx context.Context) ([]byte, *url.URL, error) {
	b, err := o.feed.Playlist(ctx)
	if err != nil {
		return nil, nil, err
	}
	return b, nil, nil
}

func (o *steadyOrigin) get(ctx context.Context, uri string, _, _ int64) ([]byte, error) {
	name := uri
	if u, err := url.Parse(uri); err == nil {
		name = u.Path
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	n, ok := transcode.ParseSegmentName(name)
	if !ok {
		v, err := strconv.ParseInt(strings.TrimSuffix(name, ".ts"), 10, 64)
		if err != nil {
			return nil, err
		}
		return o.feed.Segment(ctx, v)
	}
	return o.feed.Segment(ctx, int64(n))
}
