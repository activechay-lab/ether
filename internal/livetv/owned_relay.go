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

func (p *relayProducer) decision() core.Decision {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.dec
}

func relayTargetFloor(dec core.Decision, cut time.Duration) time.Duration {
	if dec.VideoCopy {
		// Copy cuts at the first source keyframe after cut; the first
		// segments' real lengths raise the target further when the
		// source's GOP is longer.
		return cut + time.Second
	}
	return cut
}

// startOwnedRelay starts the first relay producer and its timeline.
func (s *Service) startOwnedRelay(ctx context.Context, ss *session, info core.MediaInfo, caps core.ClientCaps, dec core.Decision, cut time.Duration) (*ownedLease, error) {
	p := s.newRelayProducer(ss, info, caps, dec, cut)
	if err := p.start(ctx, info, dec, cut); err != nil {
		return nil, err
	}
	return s.runRelay(ss, p), nil
}

func (s *Service) newRelayProducer(ss *session, info core.MediaInfo, caps core.ClientCaps, dec core.Decision, cut time.Duration) *relayProducer {
	c := ss.info.Channel
	log := s.sessionLog(ss).With("owned", "relay")
	tl := newOwnedTimeline(ss.info.ID, log, s.o.Now, 0, s.slateFor(c))
	output := liveRelayInfo(info, dec)
	container := "ts"
	if dec.Container == transcode.ContainerFMP4 {
		container = "fmp4"
	}
	tl.setHint(slateProfileFromInfo(c.ID, &output, container))
	tl.expectStart(2, relayTargetFloor(dec, cut))
	p := &relayProducer{s: s, tl: tl, log: log, caps: caps, info: info, dec: dec, cut: cut}
	p.bind(ss)
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
// it the producer, retiring the previous one.
func (p *relayProducer) start(ctx context.Context, info core.MediaInfo, dec core.Decision, cut time.Duration) error {
	ss := p.sess.Load()
	input, err := p.s.relayInput(ss)
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
	p.starved = false
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

// accept drops a copy segment that ran far past the target: keyframes
// stopped arriving, and an Apple player cannot take a segment that long.
// The producer is switched to re-encoding instead.
func (p *relayProducer) accept(seg *ownedSegment) bool {
	td := p.tl.targetDuration()
	if td <= 0 || seg.dur <= td+time.Second {
		return true
	}
	p.mu.Lock()
	copyMode := p.dec.VideoCopy
	if copyMode {
		p.starved = true
	}
	p.mu.Unlock()
	p.log.Warn("owned relay segment exceeds the target duration", "duration", seg.dur, "target", td, "dropped", copyMode)
	return !copyMode
}

func (p *relayProducer) run(ctx context.Context) {
	warmed := false
	failures := 0
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
		p.mu.Lock()
		last, starved, escalated, copyMode := p.progress, p.starved, p.escalated, p.dec.VideoCopy
		p.mu.Unlock()
		td := p.tl.targetDuration()
		if td <= 0 {
			td = relayTargetFloor(p.decision(), p.cut)
		}
		// A new producer gets ffmpeg's start-up time before the deadline
		// applies to it; afterwards the deadline is one target duration.
		limit := td
		from := last
		if last.Before(startedAt) {
			from, limit = startedAt, 12*time.Second
		}
		since := now.Sub(from)
		ended := errors.Is(err, errOriginEnded) || failures >= 6
		switch {
		case ended:
			p.recover(ctx, "relay ended")
		case starved && copyMode && !escalated:
			p.escalate(ctx, "keyframes stopped arriving")
		case copyMode && !escalated && last.Before(startedAt) && since > 6*time.Second && p.inputFlowing(since):
			// A copy producer that cannot cut its first segment within
			// the cold-tune budget has a source GOP far longer than the
			// target: re-encode, which cuts on its own keyframes.
			p.escalate(ctx, "first segment exceeds the tune budget")
		case since > limit:
			h, ok := p.s.inputHealth(p.sess.Load())
			switch {
			case ok && h.Failed:
				p.recover(ctx, "input failed")
			case ok && (h.Stalled || h.DataAge > td):
				// The tap is reconnecting the same source; the reserve and
				// slate cover it. Fall through when that takes too long.
				if since > limit+2*td {
					p.recover(ctx, "input stalled")
				}
			case copyMode && !escalated && (ok || since > limit+td):
				// Data arrives but no segment can be cut: keyframes are
				// missing. Re-encode, which cuts on its own keyframes.
				p.escalate(ctx, "no segment while input is flowing")
			case since > limit+2*td:
				p.recover(ctx, "no segment")
			}
		}
	}
}

// inputFlowing reports provider data arriving (or, without health
// information, enough time passing that the producer is the likelier fault).
func (p *relayProducer) inputFlowing(since time.Duration) bool {
	h, ok := p.s.inputHealth(p.sess.Load())
	if !ok {
		return since > 8*time.Second
	}
	return !h.Stalled && !h.Failed && h.DataAge < 2*time.Second
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
	if err := p.start(sctx, info, next, relaySegment); err != nil {
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
	return p.start(sctx, info, dec, cut)
}
