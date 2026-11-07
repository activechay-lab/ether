package livetv

import (
	"sync/atomic"
	"time"
)

// UpstreamHealth is the state of one session's provider input: what Steady's
// recovery ladder and hot standby, and the Direct deadline watchdog, use to
// tell a stalled source from a slow decoder, and to start recovering while
// the reserve still plays.
type UpstreamHealth struct {
	// Kind is "ts" for a raw transport stream read through the shared tap,
	// "hls" for a provider playlist read through the relay.
	Kind string
	// Connected reports a provider connection that is open and delivering.
	Connected bool
	// DataAge is the time since the provider last delivered: bytes on a TS
	// tap, a new segment on an HLS playlist.
	DataAge time.Duration
	// ReserveDepth is media time (PCR/PTS, not a byte estimate) held ahead of
	// this session's reader: what it can keep playing if the provider stops
	// now.
	ReserveDepth time.Duration
	// Target is the reserve the channel's preset aims for, capped by what the
	// buffer can hold at this bitrate. Zero for readers that cannot use one.
	Target time.Duration
	// Stalled reports that the provider stopped delivering and Ether is
	// reconnecting the same source while the reserve plays.
	Stalled bool
	// Failed reports that reconnecting the same source has failed repeatedly:
	// fail over now. The reserve still plays out before the reader sees EOF.
	Failed bool
	// Reconnects counts spliced same-source reconnects on this input.
	Reconnects int
	// Bitrate is the measured input bitrate in bits per second (TS only).
	Bitrate int64
}

// UpstreamEventKind names a change in an input's health.
type UpstreamEventKind int

const (
	// InputStalled: the provider stopped delivering; a reconnect has begun.
	InputStalled UpstreamEventKind = iota + 1
	// InputRecovered: a reconnect is delivering again, spliced into the same
	// stream.
	InputRecovered
	// InputFailed: same-source reconnects keep failing; fail over.
	InputFailed
)

func (k UpstreamEventKind) String() string {
	switch k {
	case InputStalled:
		return "stalled"
	case InputRecovered:
		return "recovered"
	case InputFailed:
		return "failed"
	}
	return "unknown"
}

// UpstreamEvent is delivered to watchInput callbacks.
type UpstreamEvent struct {
	Kind   UpstreamEventKind
	At     time.Time
	Health UpstreamHealth
	Err    error
}

// inputHealth reports the health of the session's current input grant. ok is
// false when the session has no grant or its input has not been read yet.
//
// It never blocks: it takes no tap, relay, grant or playlist lock, only an
// atomic load of the snapshot the input's own goroutines publish on every
// state change, reader change and 250 ms tick. It is safe to call from a
// frame clock or under a caller's own lock. DataAge is computed at the call
// from an atomic timestamp, so it is exact rather than a tick old.
func (s *Service) inputHealth(ss *session) (UpstreamHealth, bool) {
	g := s.healthGrant(ss)
	if g == nil {
		return UpstreamHealth{}, false
	}
	snap := g.snap.Load()
	if snap == nil {
		return UpstreamHealth{}, false
	}
	return snap.read(), true
}

// healthSnap is one published, immutable view of a grant's input.
type healthSnap struct {
	h UpstreamHealth
	// since is the tap's atomic time of the provider's last bytes (unix
	// nanoseconds) for a transport stream; due is when an HLS playlist's
	// next segment was due. DataAge is derived from them at read time.
	since *atomic.Int64
	due   time.Time
}

func (p *healthSnap) read() UpstreamHealth {
	h := p.h
	switch {
	case p.since != nil:
		if ns := p.since.Load(); ns != 0 {
			h.DataAge = time.Since(time.Unix(0, ns))
		}
	case !p.due.IsZero():
		h.DataAge = max(0, time.Since(p.due))
	}
	return h
}

// healthGrant finds the session's grant without taking the relay's lock.
func (s *Service) healthGrant(ss *session) *inputGrant {
	if ss == nil {
		return nil
	}
	v, ok := s.inputRelay.health.Load(ss)
	if !ok {
		return nil
	}
	g := v.(*inputGrant)
	if g.ctx.Err() != nil {
		return nil
	}
	return g
}

// watchInput calls fn for every health event on the session's current input
// grant until stop is called or the grant ends. fn runs on the tap's own
// goroutine with no locks held and must return quickly. A later grantInput
// for the same session needs a new watchInput.
func (s *Service) watchInput(ss *session, fn func(UpstreamEvent)) (stop func()) {
	g := s.healthGrant(ss)
	if g == nil || fn == nil {
		return func() {}
	}
	g.mu.Lock()
	if g.watchers == nil {
		g.watchers = map[*func(UpstreamEvent)]struct{}{}
	}
	key := &fn
	g.watchers[key] = struct{}{}
	g.mu.Unlock()
	return func() {
		g.mu.Lock()
		delete(g.watchers, key)
		g.mu.Unlock()
	}
}

// relayInputLive is relayInput for a reader that must start at the live
// edge rather than in the reserve: a decoder reopened because the source's
// codecs changed, which must not decode the old format again.
func (s *Service) relayInputLive(ss *session) (string, error) {
	u, err := s.relayInput(ss)
	if err != nil {
		return "", err
	}
	return u + "?" + relayJoinQuery + "=live", nil
}

// relayInputFormat is relayInput for a decoder reopened because the source's
// program changed (a PMT change: codec, streams): it starts at the first
// keyframe of the new format still buffered, so it reads the new format's
// backlog at once instead of waiting at the live edge. Without one it joins
// at the live edge.
func (s *Service) relayInputFormat(ss *session) (string, error) {
	u, err := s.relayInput(ss)
	if err != nil {
		return "", err
	}
	return u + "?" + relayJoinQuery + "=format", nil
}

// relayJoinQuery is the relay URL query naming a reader's join point.
const relayJoinQuery = "join"

func (s *Service) sessionGrant(ss *session) *inputGrant {
	if ss == nil {
		return nil
	}
	r := &s.inputRelay
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.bySess[ss]
	if g == nil || g.ctx.Err() != nil {
		return nil
	}
	return g
}

// notify delivers ev to the grant's watchers outside every lock.
func (g *inputGrant) notify(ev UpstreamEvent) {
	g.mu.Lock()
	fns := make([]func(UpstreamEvent), 0, len(g.watchers))
	for fn := range g.watchers {
		fns = append(fns, *fn)
	}
	g.mu.Unlock()
	for _, fn := range fns {
		fn(ev)
	}
}
