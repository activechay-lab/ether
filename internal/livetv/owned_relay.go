package livetv

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/braedonsaunders/ether/internal/core"
	"github.com/braedonsaunders/ether/internal/transcode"
)

// The relay (ffmpeg copy or conversion) also publishes onto an owned
// timeline. The ffmpeg session is only a producer: when its source stalls,
// when the session gives up, or when copy mode cannot cut a segment because
// keyframes stopped arriving, Ether replaces the producer (another source,
// or the same input re-encoded) and splices the result in with a
// discontinuity while slate keeps the client's playlist moving.

// localRelayOrigin reads a local relay session's playlist and segments.
type localRelayOrigin struct {
	hls  core.HLSSession
	name string
}

func (o *localRelayOrigin) label() string { return o.name }

func (o *localRelayOrigin) media(ctx context.Context) ([]byte, *url.URL, error) {
	rec := httptest.NewRecorder()
	o.hls.ServePlaylist(rec, httptest.NewRequest(http.MethodGet, "/index.m3u8", nil).WithContext(ctx))
	if rec.Code != http.StatusOK {
		return nil, nil, fmt.Errorf("livetv: relay playlist status %d: %w", rec.Code, errOriginEnded)
	}
	return rec.Body.Bytes(), nil, nil
}

func (o *localRelayOrigin) get(ctx context.Context, uri string, _, _ int64) ([]byte, error) {
	name := uri
	if u, err := url.Parse(uri); err == nil {
		name = u.Path
	}
	n, ok := transcode.ParseSegmentName(name)
	if !ok {
		return nil, fmt.Errorf("livetv: unknown relay resource %q", name)
	}
	rec := httptest.NewRecorder()
	o.hls.ServeSegment(rec, httptest.NewRequest(http.MethodGet, "/"+strings.TrimPrefix(name, "/"), nil).WithContext(ctx), n)
	if rec.Code != http.StatusOK && rec.Code != http.StatusPartialContent {
		return nil, fmt.Errorf("livetv: relay segment %d status %d", n, rec.Code)
	}
	return rec.Body.Bytes(), nil
}

type relayProducer struct {
	s    *Service
	tl   *ownedTimeline
	log  *slog.Logger
	sess atomic.Pointer[session]
	caps core.ClientCaps // the relay's output capabilities

	mu        sync.Mutex
	hls       core.HLSSession
	cancel    context.CancelFunc
	info      core.MediaInfo // the current source's probe
	dec       core.Decision
	cut       time.Duration
	gen       int
	ingest    *hlsIngest
	escalated bool
	starved   bool
	longAt    time.Time // the last overlong copy segment
	// reformat holds the source's new format when copy output stopped
	// matching its own PMT; the producer reopens on the new format.
	reformat  *mediaFormat
	reopened  time.Time
	startedAt time.Time
	progress  time.Time
	closed    bool
}

// bind points the producer at the published session after a staged tune.
func (p *relayProducer) bind(ss *session) { p.sess.Store(ss) }

// current is the relay session producing now (tests and diagnostics).
func (p *relayProducer) current() core.HLSSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.hls
}

// relayTargetFloor leaves a second of headroom over the cut: copy cuts at the
// first source keyframe after it (the first segments' real lengths raise the
// target further for longer GOPs), and the slate deadline (1.25x target)
// needs margin under Apple's 1.5x rule at short targets.
func relayTargetFloor(cut time.Duration) time.Duration {
	return cut + time.Second
}

// startOwnedRelay starts the first relay producer and its timeline.
func (s *Service) startOwnedRelay(ctx context.Context, ss *session, info core.MediaInfo, caps core.ClientCaps, dec core.Decision, cut time.Duration) (*ownedLease, error) {
	p := s.newRelayProducer(ss, info, caps, dec, cut)
	if err := p.start(ctx, info, dec, cut, false); err != nil {
		return nil, err
	}
	return s.runRelay(ss, p), nil
}

func (s *Service) newRelayProducer(ss *session, info core.MediaInfo, caps core.ClientCaps, dec core.Decision, cut time.Duration) *relayProducer {
	c := ss.info.Channel
	log := s.sessionLog(ss).With("owned", "relay")
	// A relay delivers in bursts (an HLS input arrives a provider segment
	// at a time; ffmpeg encodes it at many times real time) and starts
	// from the input's backlog. Publishing at real time from a reserve of
	// at least two segments turns bursts into a steady playlist, and the
	// backlog primes the channel's stability reserve.
	tl := newOwnedTimeline(ss.info.ID, log, nil, max(s.stabilityReserve(c), 2*cut), s.slateFor(c))
	output := liveRelayInfo(info, dec)
	container := "ts"
	if dec.Container == transcode.ContainerFMP4 {
		container = "fmp4"
	}
	tl.setHint(slateProfileFromInfo(c.ID, &output, container))
	tl.expectStart(2, relayTargetFloor(cut))
	tl.startMedia = 2 * cut
	p := &relayProducer{s: s, tl: tl, log: log, caps: caps, info: info, dec: dec, cut: cut}
	p.bind(ss)
	tl.stalled = func() bool {
		h, ok := s.inputHealth(p.sess.Load())
		return ok && (h.Stalled || h.Failed || h.Kind == "ts" && h.DataAge > time.Second)
	}
	p.ingest = newHLSIngest(tl, nil, log, 0, 2)
	p.ingest.live = true
	p.ingest.accept = p.accept
	return p
}

// runRelay starts publishing; the lease's release stops the producer.
func (s *Service) runRelay(ss *session, p *relayProducer) *ownedLease {
	base := ss.ctx
	if base == nil {
		base = s.ctx
	}
	runCtx, stop := context.WithCancel(base)
	go p.tl.run(runCtx)
	go p.run(runCtx)
	return &ownedLease{tl: p.tl, relay: p, release: func() {
		stop()
		p.close()
	}}
}

// start launches a relay session for the session's current input and makes
// it the producer, retiring the previous one. newFormat joins the input at
// the oldest buffered keyframe of its current format (the old format must
// not be read again after a codec change, and the new one is in hand at
// once).
func (p *relayProducer) start(ctx context.Context, info core.MediaInfo, dec core.Decision, cut time.Duration, newFormat bool) error {
	ss := p.sess.Load()
	input, err := p.s.relayInput(ss)
	if newFormat {
		input, err = p.s.relayInputFormat(ss)
	}
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.gen++
	id := "livetv-" + ss.info.ID
	if p.gen > 1 {
		id = fmt.Sprintf("livetv-%s-%d", ss.info.ID, p.gen)
	}
	p.mu.Unlock()
	hls, cancel, err := p.s.startOwnedHLS(ctx, ss, core.HLSRequest{SessionID: id, InputURL: input, LiveInput: true, Info: info, Decision: dec, SegmentDuration: cut})
	if err != nil {
		return err
	}
	return p.adopt(id, hls, cancel, info, dec, cut)
}

// adopt makes a started relay session the producer. Its first segment is
// spliced in with a discontinuity unless it is the first producer.
func (p *relayProducer) adopt(id string, hls core.HLSSession, cancel context.CancelFunc, info core.MediaInfo, dec core.Decision, cut time.Duration) error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		cancel()
		hls.Close()
		return core.ErrNotFound
	}
	old, oldCancel := p.hls, p.cancel
	p.hls, p.cancel, p.info, p.dec, p.cut = hls, cancel, info, dec, cut
	p.starved, p.reformat = false, nil
	p.startedAt = time.Now()
	origin := &localRelayOrigin{hls: hls, name: id}
	if old == nil {
		p.ingest.origin = origin
	} else {
		p.ingest.rebase(origin)
		p.tl.splice(true)
	}
	p.mu.Unlock()
	if old != nil {
		oldCancel()
		old.Close()
	}
	p.log.Info("owned relay producer started", "producer", id, "video_copy", dec.VideoCopy, "audio_copy", dec.AudioCopy, "container", dec.Container, "video_codec", dec.VideoCodec)
	return nil
}

func (p *relayProducer) close() {
	p.mu.Lock()
	p.closed = true
	hls, cancel := p.hls, p.cancel
	p.hls, p.cancel = nil, nil
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if hls != nil {
		hls.Close()
	}
}

// accept vets a relay segment before it airs. A copy segment past the
// target means a keyframe went missing: one modestly long segment (up to
// twice the target) airs, as a transient loss heals at the next keyframe; a
// grossly long one, which an Apple player cannot take, is dropped, and so is
// a second long one within a minute. Either switches the producer to a
// re-encode, which cuts on its own keyframes.
func (p *relayProducer) accept(seg *ownedSegment) bool {
	if seg.format.mislabeled {
		// A copy remux declares the codec it started with; after a source
		// codec change its PMT lies about the payload. Drop it and reopen.
		p.mu.Lock()
		f := seg.format
		p.reformat = &f
		p.mu.Unlock()
		p.log.Warn("owned relay output no longer matches its declared codecs", "payload", seg.format.String())
		return false
	}
	td := p.tl.targetDuration()
	if td <= 0 || seg.dur <= td+td/4 {
		return true
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.dec.VideoCopy {
		return true
	}
	now := time.Now()
	repeat := now.Sub(p.longAt) < time.Minute
	p.longAt = now
	drop := seg.dur > 2*td || repeat
	if drop {
		p.starved = true
	}
	p.log.Warn("owned relay segment exceeds the target duration", "duration", seg.dur, "target", td, "dropped", drop)
	return !drop
}

func (p *relayProducer) run(ctx context.Context) {
	warmed := false
	failures := 0
	var flowing time.Time // since when provider data has arrived without a pause
	for sleepCtx(ctx, 250*time.Millisecond) {
		p.mu.Lock()
		in := p.ingest
		startedAt := p.startedAt
		p.mu.Unlock()
		progress, err := in.step(ctx)
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		if progress {
			failures = 0
			p.mu.Lock()
			p.progress = now
			p.mu.Unlock()
			if !warmed && p.tl.started() {
				warmed = true
				p.tl.warmSlate()
			}
		}
		if err != nil {
			failures++
		}
		h, healthy := p.s.inputHealth(p.sess.Load())
		if healthy && !h.Stalled && !h.Failed && h.DataAge < 2*time.Second {
			if flowing.IsZero() {
				flowing = now
			}
		} else {
			flowing = time.Time{}
		}
		p.mu.Lock()
		last, starved, escalated, copyMode := p.progress, p.starved, p.escalated, p.dec.VideoCopy
		reformat, reopened := p.reformat, p.reopened
		p.mu.Unlock()
		td := p.tl.targetDuration()
		if td <= 0 {
			td = relayTargetFloor(p.cut)
		}
		// A new producer gets ffmpeg's start-up time before the deadline
		// applies to it; afterwards the deadline is one target duration.
		limit, from, cold := td, last, last.Before(startedAt)
		if cold {
			from, limit = startedAt, 12*time.Second
		}
		since := now.Sub(from)
		// Keyframe starvation: input has kept arriving for this long
		// without a copy segment being cut. A cold start gets the tune
		// budget; a running stream a second past the target (sized from
		// the longest segment seen, so a healthy GOP never reaches it,
		// and measured from when input resumed, so a source returning
		// from a stall first gets to its next keyframe).
		starvedFor := time.Duration(0)
		if !flowing.IsZero() {
			starvedFor = now.Sub(later(from, flowing))
		}
		starveLimit := td + time.Second
		if cold {
			starveLimit = 3500 * time.Millisecond
		}
		ended := errors.Is(err, errOriginEnded) || failures >= 6
		switch {
		case reformat != nil && now.Sub(reopened) > 10*time.Second:
			p.reopen(ctx, *reformat)
		case ended:
			p.recover(ctx, "relay ended")
		case healthy && h.Failed && !cold:
			// The tap gave up on this source: fail over now, not after
			// the deadline (a healthy backup should show within ~3 s).
			p.recover(ctx, "input failed")
		case healthy && h.Stalled && !cold && since > p.tl.lastSegmentDuration() && p.tl.depth() < td && p.hasBackup():
			// The tap's same-source reconnects already run; with a backup
			// at hand the next rung starts now, while the client still
			// plays its buffer, not after the deadline.
			p.recover(ctx, "input stalled, backup available")
		case copyMode && !escalated && (starved || starvedFor > starveLimit):
			// Copy cannot cut where the source has no keyframe (lost,
			// or a GOP far longer than the target). Re-encode, which
			// cuts on its own keyframes.
			p.escalate(ctx, "copy cannot cut segments from this source")
		case since > limit:
			switch {
			case healthy && h.Failed:
				p.recover(ctx, "input failed")
			case p.tl.depth() >= td:
				// The reserve still covers the client; ffmpeg's own input
				// retries (and the tap's reconnects) get that long.
			case since > limit+2*td:
				// The tap reconnects the same source while slate covers;
				// fall through when that takes too long.
				p.recover(ctx, "no segment")
			}
		}
	}
}

func later(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

// reopen restarts the producer on the same input, from its new format,
// after the source changed codecs, deciding again for the new audio.
func (p *relayProducer) reopen(ctx context.Context, f mediaFormat) {
	p.mu.Lock()
	p.reopened = time.Now()
	info, cut := p.info, p.cut
	p.mu.Unlock()
	info.Streams = append([]core.StreamInfo(nil), info.Streams...)
	if len(f.audio) > 0 {
		for i := range info.Streams {
			if info.Streams[i].Type == "audio" {
				a := f.audio[0]
				info.Streams[i].Codec, info.Streams[i].CodecString, info.Streams[i].Profile = a.codec, "", ""
				info.Streams[i].Channels, info.Streams[i].SampleRate, info.Streams[i].ChannelLayout = a.channels, a.rate, ""
				break
			}
		}
	}
	dec, _, _ := p.s.relayPlan(p.sess.Load(), info)
	p.log.Warn("owned relay reopening for the source's new format", "format", f.String(), "audio_copy", dec.AudioCopy)
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := p.start(sctx, info, dec, cut, true); err != nil {
		p.log.Warn("owned relay reopen failed", Diagnose("relay_reopen", err).LogAttrs()...)
		p.recover(ctx, "reopen failed")
	}
}

func (p *relayProducer) hasBackup() bool {
	ss := p.sess.Load()
	ss.mu.Lock()
	c, cur := ss.info.Channel, ss.inputSource.ID
	ss.mu.Unlock()
	return len(p.s.orderedSourceCandidates(c, cur)) > 0
}

// escalate replaces a copy producer with a re-encode of the same input.
func (p *relayProducer) escalate(ctx context.Context, why string) {
	p.mu.Lock()
	if p.escalated {
		p.mu.Unlock()
		return
	}
	p.escalated = true
	info, dec := p.info, p.dec
	p.mu.Unlock()
	next := p.s.o.Transcoder.Decide(info, p.caps, core.DecideOptions{DirectStream: false, AudioStream: -1, SubtitleStream: -1})
	if next.VideoCopy {
		return
	}
	if next.Container != transcode.ContainerFMP4 && next.Container != transcode.ContainerMPEGTS {
		next.Container = dec.Container
	}
	p.log.Warn("owned relay switching producer to re-encode", "reason", why)
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := p.start(sctx, info, next, relaySegment, false); err != nil {
		p.log.Warn("owned relay re-encode producer failed", Diagnose("relay_escalate", err).LogAttrs()...)
		p.recover(ctx, "re-encode start failed")
	}
}

// recover restarts the relay on the next source in the channel's health
// order, or on the same one when it is the only source.
func (p *relayProducer) recover(ctx context.Context, why string) {
	ss := p.sess.Load()
	ss.mu.Lock()
	c, cur := ss.info.Channel, ss.inputSource
	ss.mu.Unlock()
	candidates := append(p.s.orderedSourceCandidates(c, cur.ID), cur)
	p.log.Warn("owned relay recovering", "reason", why, "input", cur.ID, "candidates", len(candidates))
	for _, src := range candidates {
		if ctx.Err() != nil {
			return
		}
		if err := p.switchSource(ctx, ss, c, src, src.ID != cur.ID); err != nil {
			if !errors.Is(err, context.Canceled) {
				p.s.recordSourceHealth(c.ID, src, err, false)
			}
			p.log.Warn("owned relay source unavailable", append(Diagnose("relay_failover", err).LogAttrs(), "input", src.ID)...)
			continue
		}
		if src.ID != cur.ID {
			p.s.recordSourceHealth(c.ID, cur, errOriginStalled, false)
			p.log.Warn("owned relay switched source", "from", cur.ID, "to", src.ID)
		}
		return
	}
	// Nothing opened: slate continues; the next deadline tries again.
	p.mu.Lock()
	p.startedAt = time.Now()
	p.mu.Unlock()
}

func (p *relayProducer) switchSource(ctx context.Context, ss *session, c Channel, src StreamSource, probe bool) error {
	sctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	raw, headers, err := p.s.ResolveStreamSource(sctx, c, src)
	if err != nil {
		return err
	}
	grantSrc := src
	grantSrc.URL = raw
	if err := p.s.grantInput(ss, grantSrc, headers); err != nil {
		return err
	}
	ss.mu.Lock()
	ss.inputURL, ss.inputSource, ss.inputHeaders = raw, src, headers.Clone()
	ss.info.ActiveSourceID = src.ID
	ss.mu.Unlock()
	p.mu.Lock()
	info, dec, cut := p.info, p.dec, p.cut
	p.mu.Unlock()
	if probe {
		probed, err := p.s.probeSessionInput(sctx, ss)
		if err != nil {
			return err
		}
		if probed == nil || probed.Video() == nil {
			return &TuneError{Code: "no_video", Stage: "probe", cause: core.ErrNotAvailable}
		}
		info = *probed
		info.Duration = 0
		dec, cut, _ = p.s.relayPlan(ss, info)
	}
	return p.start(sctx, info, dec, cut, false)
}
