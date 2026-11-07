package livetv

import (
	"errors"
	"io"
	"time"

	"github.com/braedonsaunders/ether/internal/transcode"
)

// The Steady recovery ladder decides which input a provider channel opens
// next, and when. It never parks the channel itself: a failed source is
// reopened at once, twice more within 1.5 s, and then the next backup is
// opened immediately. Health only orders sources (orderedSourceCandidates).
//
// Provider safety bounds the ladder per source, matching the relay's refusal
// pacing: after the opening rungs a source is retried at most once a second,
// and a source that fails steadyRefusalCeiling consecutive opens without
// delivering playback is parked for steadyParked. A source that delivers
// steadyDelivered of aired media is healthy again.
var steadyRungs = []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond}

const (
	steadyRefusalCeiling = 28
	steadyParked         = 2 * time.Minute
	steadySpacing        = time.Second
	steadyDelivered      = 4 * time.Second
	steadyRoundMax       = 8 * time.Second
)

type ladderSource struct {
	fails  int       // consecutive opens that never delivered
	last   time.Time // last open attempt
	parked time.Time // no attempt before this
}

type steadyLadder struct {
	episode  time.Time // start of the current failure episode; zero while healthy
	source   string    // source of the current or most recent attempt
	rung     int       // same-source reopens already used on source this episode
	tried    map[string]bool
	round    int // full passes over every candidate this episode
	liveEdge bool
	next     string
	sources  map[string]*ladderSource
}

func newSteadyLadder() *steadyLadder {
	return &steadyLadder{tried: map[string]bool{}, sources: map[string]*ladderSource{}}
}

func (l *steadyLadder) state(id string) *ladderSource {
	st := l.sources[id]
	if st == nil {
		st = &ladderSource{}
		l.sources[id] = st
	}
	return st
}

// opened records an attempt on id; the ladder's choice is consumed.
func (l *steadyLadder) opened(id string, now time.Time) {
	st := l.state(id)
	st.last = now
	if id != l.source {
		l.rung = 0
	}
	l.source = id
	l.tried[id] = true
	l.next = ""
	l.liveEdge = false
}

// delivered marks id healthy: it aired steadyDelivered of media.
func (l *steadyLadder) delivered(id string) {
	st := l.state(id)
	st.fails, st.parked = 0, time.Time{}
	if id == l.source {
		l.episode = time.Time{}
		l.rung, l.round = 0, 0
		clear(l.tried)
		l.tried[id] = true
	}
}

// choose returns the source to open now from candidates (best first) and
// whether it should join its input's live edge. ok is false when every
// candidate is parked; retry is then the earliest unpark time.
func (l *steadyLadder) choose(candidates []string, now time.Time) (id string, liveEdge, ok bool, retry time.Time) {
	usable := func(id string) bool {
		st := l.sources[id]
		return st == nil || !st.parked.After(now)
	}
	if l.next != "" && usable(l.next) {
		for _, c := range candidates {
			if c == l.next {
				return l.next, l.liveEdge, true, time.Time{}
			}
		}
	}
	for _, c := range candidates {
		if usable(c) {
			return c, false, true, time.Time{}
		}
	}
	for _, c := range candidates {
		if p := l.sources[c].parked; retry.IsZero() || p.Before(retry) {
			retry = p
		}
	}
	return "", false, false, retry
}

// failed records that the input on id ended or never started, and returns
// when the channel should open its next input. aired reports that this input
// delivered steadyDelivered first; the failure then begins a new episode.
// failover forces the next source now (the tap already exhausted same-URL
// reconnects, or the input reached its end). candidates is the current
// health order, best first.
func (l *steadyLadder) failed(id string, err error, aired, failover bool, candidates []string, now time.Time) time.Time {
	if aired || l.episode.IsZero() {
		l.episode, l.rung, l.round = now, 0, 0
		clear(l.tried)
		l.tried[id] = true
	}
	st := l.state(id)
	if !aired {
		st.fails++
		if st.fails >= steadyRefusalCeiling {
			st.parked = now.Add(steadyParked)
			// One probe per parked interval afterwards, never another burst.
			st.fails = steadyRefusalCeiling - 1
		}
	}
	l.source = id
	if errors.Is(err, transcode.ErrInputChanged) && st.parked.Before(now) {
		// A codec or stream-set change: the source is fine, the decoder is
		// not. Rejoin it at the live edge without spending a rung.
		l.next, l.liveEdge = id, true
		return now
	}
	l.liveEdge = false
	if !failover && st.parked.Before(now) && l.rung < len(steadyRungs) {
		at := l.episode.Add(steadyRungs[l.rung])
		if l.round > 0 {
			at = st.last.Add(steadySpacing)
		}
		l.rung++
		l.next = id
		return later(at, now)
	}
	for _, c := range candidates {
		if c == id || l.tried[c] {
			continue
		}
		if p := l.sources[c]; p != nil && p.parked.After(now) {
			continue
		}
		l.next = c
		return now
	}
	// Every candidate was tried this round: start another, backing off
	// between rounds and spacing each source's own attempts.
	l.round++
	clear(l.tried)
	l.rung = len(steadyRungs)
	wait := min(steadyRoundMax, time.Duration(1<<min(l.round-1, 4))*time.Second)
	l.next = ""
	for _, c := range candidates {
		if p := l.sources[c]; p == nil || !p.parked.After(now) {
			l.next = c
			break
		}
	}
	if l.next == "" {
		_, _, _, retry := l.choose(candidates, now)
		return later(retry, now)
	}
	at := now.Add(wait)
	if last := l.state(l.next).last.Add(steadySpacing); last.After(at) {
		at = last
	}
	return at
}

func later(at, now time.Time) time.Time {
	if at.Before(now) {
		return now
	}
	return at
}

// steadyFailover reports errors that mean the input itself is gone rather
// than this decoder: the stream ended, or the shared tap exhausted its own
// same-URL reconnects and closed its readers.
func steadyFailover(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
}
