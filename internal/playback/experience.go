package playback

import (
	"github.com/braedonsaunders/ether/internal/core"
	"time"
)

// Viewer-experience accounting: how long a play took to show a picture and
// how often it stalled afterwards. It runs only where client timelines are
// already handled and costs a few comparisons per report.
const (
	// stallMinimum is the shortest inferred stall counted for clients that
	// report positions but no buffering state.
	stallMinimum = 3 * time.Second
	// stallSeekSlack tolerates position jitter before an interval reads
	// as a seek rather than playback.
	stallSeekSlack = 2 * time.Second
	// stallMaxInterval bounds the report gap an inferred stall may span; a
	// longer silence is a missing client, not buffering.
	stallMaxInterval = 2 * time.Minute
	// bufferingCap bounds one explicit buffering spell so a client that
	// vanished mid-buffer cannot add hours.
	bufferingCap = 10 * time.Minute
)

type experienceState struct {
	requestedAt time.Time // earliest play request; zero for a prepared-only source
	reported    bool      // a client timeline arrived
	pictureAt   time.Time
	failure     string

	// First report, the baseline for clients that only report positions.
	baseAt  time.Time
	basePos time.Duration

	// Explicit buffering (Plex timelines). A spell that began with a
	// position jump is a seek, not a stall.
	bufferingSince time.Time
	bufferingSeek  bool
	// Inferred stalls (Jellyfin, Emby, Silo).
	lastAt  time.Time
	lastPos time.Duration
	stalled bool

	events   int
	buffered time.Duration
}

// noteRequest records a play request; the earliest one wins.
func (e *experienceState) noteRequest(at time.Time) {
	if !at.IsZero() && (e.requestedAt.IsZero() || at.Before(e.requestedAt)) {
		e.requestedAt = at
	}
}

// observe accounts one client timeline report.
func (e *experienceState) observe(now time.Time, explicit bool, pos time.Duration, state string) {
	e.reported = true
	if e.pictureAt.IsZero() {
		e.observeStart(now, explicit, pos, state)
		return
	}
	if explicit {
		if !e.bufferingSince.IsZero() && state != "buffering" {
			if d, ok := e.explicitStall(now); ok {
				e.events++
				e.buffered += d
			}
			e.bufferingSince, e.bufferingSeek = time.Time{}, false
		}
		if state == "buffering" && e.bufferingSince.IsZero() {
			e.bufferingSince = now
			// Players rebuffer after every seek or scrub: a buffering
			// report whose position left the playback line is the seek.
			e.bufferingSeek = !e.lastAt.IsZero() && (pos < e.lastPos-time.Second || pos-e.lastPos > now.Sub(e.lastAt)+stallSeekSlack)
		}
		if state == "playing" || state == "paused" {
			e.lastAt, e.lastPos = now, pos
		}
		return
	}
	if state != "playing" {
		e.lastAt, e.stalled = time.Time{}, false
		return
	}
	if e.lastAt.IsZero() {
		e.lastAt, e.lastPos = now, pos
		return
	}
	dt, dp := now.Sub(e.lastAt), pos-e.lastPos
	e.lastAt, e.lastPos = now, pos
	if dt <= 0 || dt > stallMaxInterval || dp < -time.Second || dp > dt+stallSeekSlack {
		// A seek, a clock step or a silent client: not evidence of a stall.
		e.stalled = false
		return
	}
	stall := dt - max(0, dp)
	if stall >= stallMinimum {
		e.buffered += stall
		if !e.stalled {
			e.events++
		}
	}
	e.stalled = stall >= stallMinimum && dp < time.Second
}

// observeStart waits for the first picture. Plex reports "playing" once it
// shows one; other clients report positions, so the first report whose
// position advanced proves a picture and dates it back by the advance.
func (e *experienceState) observeStart(now time.Time, explicit bool, pos time.Duration, state string) {
	if explicit {
		if state == "playing" {
			e.pictureAt = now
		}
		return
	}
	if e.baseAt.IsZero() {
		if state != "paused" {
			e.baseAt, e.basePos = now, pos
		}
		return
	}
	if (state != "playing" && state != "stopped") || pos <= e.basePos {
		return
	}
	at := now.Add(-(pos - e.basePos))
	if at.Before(e.baseAt) {
		at = e.baseAt
	}
	e.pictureAt = at
	if state == "playing" {
		e.lastAt, e.lastPos = now, pos
	}
}

// snapshot reports the accounting; nil until a play request or timeline.
func (e *experienceState) snapshot(now time.Time) *core.PlayExperience {
	if e.requestedAt.IsZero() && !e.reported {
		return nil
	}
	out := &core.PlayExperience{Requested: true, StartTime: -1, RequestedAt: e.requestedAt, StartFailure: e.failure, BufferEvents: e.events, Buffering: e.buffered}
	if !e.pictureAt.IsZero() && e.failure == "" {
		out.Pictured, out.PicturedAt = true, e.pictureAt
		if !e.requestedAt.IsZero() {
			out.StartTime = max(0, e.pictureAt.Sub(e.requestedAt))
		}
	}
	if d, ok := e.explicitStall(now); ok {
		out.BufferEvents++
		out.Buffering += d
	}
	return out
}

// explicitStall reports a Plex buffering spell as a stall: one that began
// mid-play (not with a seek) and lasted at least stallMinimum, the same
// floor inferred stalls use. Shorter blips and post-seek rebuffering are not
// counted.
func (e *experienceState) explicitStall(now time.Time) (time.Duration, bool) {
	if e.bufferingSince.IsZero() || e.bufferingSeek {
		return 0, false
	}
	d := min(max(0, now.Sub(e.bufferingSince)), bufferingCap)
	return d, d >= stallMinimum
}

// NotePlayRequest records when a frontend's play request for a prepared
// source arrived (for example a Jellyfin PlaybackInfo that planned and
// opened it). The earliest request wins.
func (m *Manager) NotePlayRequest(id string, at time.Time) {
	if s := m.lookup(id); s != nil {
		s.mu.Lock()
		s.exp.noteRequest(at)
		s.mu.Unlock()
	}
}

// NoteFailedStart marks a play whose client never showed a picture although
// it reported playing (a stalled start the server ended).
func (m *Manager) NoteFailedStart(id, reason string) {
	if s := m.lookup(id); s != nil && reason != "" {
		s.mu.Lock()
		s.exp.failure = reason
		s.mu.Unlock()
	}
}
